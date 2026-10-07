package pronounce

import (
	"context"
	_ "embed"
	json "encoding/json/v2"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"unicode/utf8"

	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/cost"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
	openaiapi "github.com/osak/yomiagen/internal/openai"
	"github.com/osak/yomiagen/internal/tts"
)

//go:embed prompt.md
var prompt string

//go:embed schema.json
var schemaBytes []byte

var newOpenAIClient = func(cfg config.Config, key string) openaiapi.Client {
	return openaiapi.Client{Model: cfg.OpenAI.Model, BaseURL: cfg.OpenAI.BaseURL, APIKey: key}
}

func Run(ctx context.Context, j *job.Job, cfg config.Config, preset config.Preset, force bool) error {
	b, err := os.ReadFile(j.Path("20_script.json"))
	if err != nil {
		return err
	}
	profile := cfg.Prosody[preset.Prosody]
	pb, _ := json.Marshal(profile)
	key := os.Getenv("OPENAI_API_KEY")
	mode := "local"
	if key != "" && cfg.OpenAI.Pronunciation != "off" {
		mode = "openai"
	}
	hash := job.Hash(b, pb, []byte(preset.Name), []byte(cfg.OpenAI.Model), []byte(cfg.OpenAI.Pronunciation), []byte(mode), []byte(prompt), schemaBytes)
	if !force && j.Fresh("pronounce", hash, "30_speech.json") {
		return nil
	}
	var s model.Script
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}
	out := model.Speech{Backend: preset.Backend, Voice: preset.VoiceID, Global: profile.Global}
	for _, u := range s.Utterances {
		parts := split(u.Text, cfg.Chunk.MaxChars)
		for pi, text := range parts {
			c := model.Chunk{ID: fmt.Sprintf("c%04d", len(out.Chunks)+1), UtteranceID: u.ID, Text: text}
			pause := profile.PauseMS["sentence"]
			if pi == len(parts)-1 {
				pause = profile.PauseMS["paragraph"]
			}
			if u.Role == "heading" {
				c.Ops = append(c.Ops, profile.Heading...)
				pause = profile.PauseMS["after_heading"]
				before := profile.PauseMS["before_heading"]
				if u.Level == 2 && profile.PauseMS["before_section"] > 0 {
					before = profile.PauseMS["before_section"]
				}
				c.Ops = append(c.Ops, globalNumber("prePhonemeLength", float64(before)/1000))
			}
			c.Ops = append(c.Ops, globalNumber("postPhonemeLength", float64(pause)/1000))
			if strings.HasSuffix(strings.TrimSpace(text), "?") || strings.HasSuffix(strings.TrimSpace(text), "？") {
				c.Ops = append(c.Ops, tts.Op{Op: "interrogative", Target: tts.Target{Phrase: -1, Mora: -1}})
			}
			out.Chunks = append(out.Chunks, c)
		}
	}
	if mode == "openai" {
		plan(ctx, j, &out, s.Lexicon, cfg, key)
	}
	if err := jsonfile.Write(j.Path("30_speech.json"), out); err != nil {
		return err
	}
	return j.Done("pronounce", hash)
}

type plannedChunk struct {
	ID   string `json:"id"`
	Kana string `json:"kana"`
}

type pronunciationPlan struct {
	Chunks []plannedChunk `json:"chunks"`
}

func plan(ctx context.Context, j *job.Job, speech *model.Speech, lexicon []model.LexEntry, cfg config.Config, key string) {
	client := newOpenAIClient(cfg, key)
	byID := make(map[string]*model.Chunk, len(speech.Chunks))
	for i := range speech.Chunks {
		byID[speech.Chunks[i].ID] = &speech.Chunks[i]
	}
	for _, group := range groupChunks(speech.Chunks, cfg.OpenAI.ChunkChars) {
		chunks := make([]map[string]string, 0, len(group))
		var groupText strings.Builder
		for _, chunk := range group {
			chunks = append(chunks, map[string]string{"id": chunk.ID, "text": chunk.Text})
			groupText.WriteString(chunk.Text)
		}
		var relevantLexicon []model.LexEntry
		for _, entry := range lexicon {
			if entry.Surface != "" && strings.Contains(groupText.String(), entry.Surface) {
				relevantLexicon = append(relevantLexicon, entry)
			}
		}
		input := struct {
			Lexicon []model.LexEntry    `json:"lexicon"`
			Chunks  []map[string]string `json:"chunks"`
		}{relevantLexicon, chunks}
		payload, _ := json.Marshal(input)
		result, err := client.Structured(ctx, prompt, string(payload), "pronunciation_plan", schemaBytes)
		if err != nil {
			slog.Warn("OpenAI pronunciation planning failed; using VOICEVOX analysis", "error", err)
			continue
		}
		var planned pronunciationPlan
		if err := json.Unmarshal([]byte(result.Text), &planned); err != nil {
			slog.Warn("invalid OpenAI pronunciation plan; using VOICEVOX analysis", "error", err)
			continue
		}
		for _, item := range planned.Chunks {
			if chunk := byID[item.ID]; chunk != nil && strings.TrimSpace(item.Kana) != "" {
				chunk.Kana = strings.TrimSpace(item.Kana)
			}
		}
		price := cfg.Pricing.OpenAI[cfg.OpenAI.Model]
		usd := (float64(result.Usage.Input-result.Usage.Cached)*price.Input + float64(result.Usage.Cached)*price.Cached + float64(result.Usage.Output)*price.Output) / 1e6
		_ = cost.Append(j.Path("cost.jsonl"), cost.Record{JobID: j.Meta.ID, Stage: "pronounce", Provider: "openai", Model: cfg.OpenAI.Model, InputTokens: result.Usage.Input, CachedTokens: result.Usage.Cached, OutputTokens: result.Usage.Output, WallMS: result.Elapsed.Milliseconds(), CostUSD: usd})
	}
}

func groupChunks(chunks []model.Chunk, max int) [][]model.Chunk {
	if max <= 0 {
		max = 4000
	}
	var groups [][]model.Chunk
	var current []model.Chunk
	chars := 0
	for _, chunk := range chunks {
		n := utf8.RuneCountInString(chunk.Text)
		if len(current) > 0 && chars+n > max {
			groups = append(groups, current)
			current = nil
			chars = 0
		}
		current = append(current, chunk)
		chars += n
	}
	if len(current) > 0 {
		groups = append(groups, current)
	}
	return groups
}
func globalNumber(name string, value float64) tts.Op {
	b, _ := json.Marshal(map[string]float64{name: value})
	return tts.Op{Op: "global", Args: b}
}
func split(s string, max int) []string {
	if max <= 0 {
		max = 200
	}
	var sentences []string
	var b strings.Builder
	r := []rune(strings.TrimSpace(s))
	for i, x := range r {
		b.WriteRune(x)
		boundary := strings.ContainsRune("。！？!?", x)
		if x == '.' {
			prevDigit := i > 0 && r[i-1] >= '0' && r[i-1] <= '9'
			nextDigit := i+1 < len(r) && r[i+1] >= '0' && r[i+1] <= '9'
			boundary = !prevDigit || !nextDigit
		}
		if boundary && (i+1 == len(r) || isSpace(r[i+1])) {
			if t := strings.TrimSpace(b.String()); t != "" {
				sentences = append(sentences, t)
			}
			b.Reset()
		}
	}
	if t := strings.TrimSpace(b.String()); t != "" {
		sentences = append(sentences, t)
	}
	var out []string
	for _, sentence := range sentences {
		if utf8.RuneCountInString(sentence) <= max {
			out = append(out, sentence)
			continue
		}
		out = append(out, splitLong(sentence, max)...)
	}
	return out
}
func splitLong(s string, max int) []string {
	var out []string
	for utf8.RuneCountInString(s) > max {
		r := []rune(s)
		cut := max
		for i := max - 1; i > max/2; i-- {
			if strings.ContainsRune("、, ", r[i]) {
				cut = i + 1
				break
			}
		}
		out = append(out, strings.TrimSpace(string(r[:cut])))
		s = strings.TrimSpace(string(r[cut:]))
	}
	if s != "" {
		out = append(out, s)
	}
	return out
}
func isSpace(r rune) bool { return r == ' ' || r == '\n' || r == '\t' || r == '\r' }
