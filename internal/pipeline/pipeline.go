package pipeline

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/osak/yomiagen/internal/audio"
	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/cost"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
	"github.com/osak/yomiagen/internal/tts"
	"github.com/osak/yomiagen/internal/tts/voicevox"
)

type Pipeline struct {
	Config  config.Config
	Preset  config.Preset
	Job     *job.Job
	Backend *voicevox.Client
}

func New(cfg config.Config, preset config.Preset, j *job.Job) (*Pipeline, error) {
	if preset.Backend != "voicevox" {
		return nil, fmt.Errorf("backend %q is not implemented", preset.Backend)
	}
	return &Pipeline{Config: cfg, Preset: preset, Job: j, Backend: voicevox.New(preset.Endpoint)}, nil
}

func (p *Pipeline) RegisterDictionary(ctx context.Context) error {
	var s model.Script
	if err := jsonfile.Read(p.Job.Path("20_script.json"), &s); err != nil {
		return err
	}
	for _, e := range s.Lexicon {
		if _, ok := p.Job.Meta.DictUUID[e.Surface]; ok {
			continue
		}
		id, err := p.Backend.AddDictionary(ctx, voicevox.DictEntry{Surface: e.Surface, Pronunciation: e.Pronunciation, AccentType: e.AccentType, WordType: e.WordType})
		if err != nil {
			return fmt.Errorf("register dictionary word %q: %w", e.Surface, err)
		}
		p.Job.Meta.DictUUID[e.Surface] = id
	}
	return p.Job.Save()
}

func (p *Pipeline) Query(ctx context.Context, force bool) error {
	b, err := os.ReadFile(p.Job.Path("30_speech.json"))
	if err != nil {
		return err
	}
	hash := job.Hash(b, []byte(p.Preset.Endpoint))
	dir := p.Job.Path("35_query")
	if !force && p.Job.Fresh("query", hash, "35_query") {
		entries, _ := os.ReadDir(dir)
		if len(entries) > 0 {
			return nil
		}
	}
	var speech model.Speech
	if err := json.Unmarshal(b, &speech); err != nil {
		return err
	}
	caps, err := p.Backend.Capabilities(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	for _, c := range speech.Chunks {
		q, err := p.Backend.BuildQuery(ctx, c.Text, speech.Voice, tts.QueryOptions{KatakanaEnglish: true, Kana: c.Kana})
		if err != nil && c.Kana != "" {
			slog.Warn("generated kana was rejected; using VOICEVOX analysis", "chunk", c.ID, "error", err)
			q, err = p.Backend.BuildQuery(ctx, c.Text, speech.Voice, tts.QueryOptions{KatakanaEnglish: true})
		}
		if err != nil {
			return fmt.Errorf("query %s: %w", c.ID, err)
		}
		ops := append(append([]tts.Op{}, speech.Global...), c.Ops...)
		var early, late []tts.Op
		for _, op := range ops {
			if !tts.Supported(caps, op) {
				if p.Config.DegradePolicy == tts.DegradeError {
					return fmt.Errorf("query %s: engine lacks capability for %s", c.ID, op.Op)
				}
				slog.Warn("unsupported prosody operation skipped", "chunk", c.ID, "op", op.Op)
				continue
			}
			switch op.Op {
			case "emphasize", "lengthen", "devoice", "patch":
				late = append(late, op)
			default:
				early = append(early, op)
			}
		}
		recalc := false
		for _, op := range early {
			r, err := p.Backend.Apply(ctx, q, op)
			if err != nil {
				return fmt.Errorf("query %s op %s: %w", c.ID, op.Op, err)
			}
			recalc = recalc || r
		}
		if recalc {
			if err := p.Backend.Recalculate(ctx, q); err != nil {
				return fmt.Errorf("recalculate %s: %w", c.ID, err)
			}
		}
		for _, op := range late {
			if _, err := p.Backend.Apply(ctx, q, op); err != nil {
				return fmt.Errorf("query %s op %s: %w", c.ID, op.Op, err)
			}
		}
		if err := jsonfile.Write(filepath.Join(dir, c.ID+".json"), q); err != nil {
			return err
		}
	}
	return p.Job.Done("query", hash)
}

func (p *Pipeline) Synthesize(ctx context.Context, force bool, only map[string]bool) error {
	var speech model.Speech
	if err := jsonfile.Read(p.Job.Path("30_speech.json"), &speech); err != nil {
		return err
	}
	if err := os.MkdirAll(p.Job.Path("40_chunks"), 0o755); err != nil {
		return err
	}
	if err := p.Backend.Initialize(ctx, speech.Voice); err != nil {
		slog.Debug("speaker initialization returned an error; synthesis may still work", "error", err)
	}
	workers := p.Config.Concurrency.TTS
	if workers < 1 {
		workers = 1
	}
	sem := make(chan struct{}, workers)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error
	for _, chunk := range speech.Chunks {
		c := chunk
		if len(only) > 0 && !only[c.ID] {
			continue
		}
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			qpath := p.Job.Path(filepath.Join("35_query", c.ID+".json"))
			qb, err := os.ReadFile(qpath)
			hasQuery := err == nil
			input := []byte(c.Text)
			if hasQuery {
				input = qb
			}
			h := job.Hash(input, []byte(speech.Voice))
			out := p.Job.Path(filepath.Join("40_chunks", c.ID+".wav"))
			mu.Lock()
			old := p.Job.Meta.Chunks[c.ID]
			mu.Unlock()
			if !force && old == h {
				if _, err := os.Stat(out); err == nil {
					return
				}
			}
			start := time.Now()
			var result *tts.Result
			for attempt := 0; attempt < 3; attempt++ {
				if hasQuery {
					var q tts.Query
					if err = json.Unmarshal(qb, &q); err == nil {
						result, err = p.Backend.SynthesizeQuery(ctx, &q, speech.Voice)
					}
				} else {
					result, err = p.Backend.Synthesize(ctx, tts.Request{Text: c.Text, VoiceID: speech.Voice})
				}
				if err == nil {
					break
				}
				time.Sleep(time.Duration(1<<attempt) * 200 * time.Millisecond)
			}
			if err != nil {
				slog.Error("chunk synthesis failed; inserting silence", "chunk", c.ID, "error", err)
				result = &tts.Result{WAV: audio.Silence(1)}
			}
			if err = os.WriteFile(out, result.WAV, 0o644); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
				return
			}
			duration := 0.0
			if w, e := audio.Parse(result.WAV); e == nil {
				duration = w.Duration()
			}
			mu.Lock()
			p.Job.Meta.Chunks[c.ID] = h
			_ = cost.Append(p.Job.Path("cost.jsonl"), cost.Record{JobID: p.Job.Meta.ID, Stage: "synthesize", Provider: p.Preset.Backend, Model: p.Preset.VoiceName, Characters: utf8.RuneCountInString(c.Text), AudioSeconds: duration, WallMS: time.Since(start).Milliseconds(), CostUSD: result.CostUSD})
			mu.Unlock()
		})
	}
	wg.Wait()
	if len(errs) > 0 {
		return errors.Join(errs...)
	}
	if err := p.Job.Save(); err != nil {
		return err
	}
	return nil
}

func (p *Pipeline) Assemble(force bool, output string) (string, error) {
	var speech model.Speech
	if err := jsonfile.Read(p.Job.Path("30_speech.json"), &speech); err != nil {
		return "", err
	}
	var files []string
	for _, c := range speech.Chunks {
		files = append(files, p.Job.Path(filepath.Join("40_chunks", c.ID+".wav")))
	}
	hash, err := job.HashFiles(files...)
	if err != nil {
		return "", err
	}
	hash = job.Hash([]byte(hash), []byte(p.Config.Output.Format), []byte(p.Config.Output.Bitrate))
	final := audio.OutputPath(p.Job.Dir, p.Config.Output.Format)
	if !force && p.Job.Fresh("assemble", hash, filepath.Base(final)) {
		if output != "" {
			return output, copyFile(final, output)
		}
		return final, nil
	}
	wav, starts, err := audio.Join(files)
	if err != nil {
		return "", err
	}
	wavPath := p.Job.Path("50_output.wav")
	if err := os.WriteFile(wavPath, wav, 0o644); err != nil {
		return "", err
	}
	utter := map[string]model.Utterance{}
	var script model.Script
	if jsonfile.Read(p.Job.Path("20_script.json"), &script) == nil {
		for _, u := range script.Utterances {
			utter[u.ID] = u
		}
	}
	var chapters []audio.Chapter
	for i, c := range speech.Chunks {
		u := utter[c.UtteranceID]
		if u.Role == "heading" {
			chapters = append(chapters, audio.Chapter{Title: u.Text, StartMS: int64(starts[i] * 1000)})
		}
	}
	total := 0.0
	if w, e := audio.Parse(wav); e == nil {
		total = w.Duration()
	}
	if len(chapters) == 0 {
		chapters = append(chapters, audio.Chapter{Title: script.Title, StartMS: 0})
	}
	for i := range chapters {
		if i+1 < len(chapters) {
			chapters[i].EndMS = chapters[i+1].StartMS
		} else {
			chapters[i].EndMS = int64(total * 1000)
		}
	}
	meta := p.Job.Path("50_chapters.txt")
	if err := audio.WriteChapters(meta, script.Title, chapters); err != nil {
		return "", err
	}
	if p.Config.Output.Format != "" && p.Config.Output.Format != "wav" {
		if err := audio.Encode(wavPath, meta, final, p.Config.Output.Bitrate, p.Config.Output.Loudnorm); err != nil {
			slog.Warn("ffmpeg unavailable or failed; keeping WAV output", "error", err)
			final = wavPath
		}
	}
	if err := p.Job.Done("assemble", hash); err != nil {
		return "", err
	}
	if output != "" {
		if err := copyFile(final, output); err != nil {
			return "", err
		}
		final = output
	}
	return final, nil
}
func copyFile(from, to string) error {
	b, err := os.ReadFile(from)
	if err != nil {
		return err
	}
	if dir := filepath.Dir(to); dir != "." {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	return os.WriteFile(to, b, 0o644)
}
func Only(value string) map[string]bool {
	out := map[string]bool{}
	for _, s := range strings.Split(value, ",") {
		if s = strings.TrimSpace(s); s != "" {
			out[s] = true
		}
	}
	return out
}
func SortedCaps(c tts.CapSet) []string {
	var out []string
	for k, v := range c {
		if v {
			out = append(out, string(k))
		}
	}
	sort.Strings(out)
	return out
}
