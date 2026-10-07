package pipeline

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"github.com/osak/yomiagen/internal/audio"
	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
	"github.com/osak/yomiagen/internal/tts"
	"github.com/osak/yomiagen/internal/tts/voicevox"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestEditedQueryResynthesizesOnlyChangedChunk(t *testing.T) {
	var synthCalls atomic.Int32
	wav := audio.Silence(.05)
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		var body []byte
		switch r.URL.Path {
		case "/engine_manifest":
			body = []byte(`{"supported_features":{"adjust_speed_scale":true,"adjust_intonation_scale":true}}`)
		case "/audio_query":
			body = []byte(`{"accent_phrases":[{"moras":[{"text":"テ","vowel":"e","vowel_length":0.1,"pitch":5.5}],"accent":1,"pause_mora":null,"is_interrogative":false}],"speedScale":1,"intonationScale":1,"volumeScale":1,"prePhonemeLength":0.1,"postPhonemeLength":0.1}`)
		case "/initialize_speaker":
			status = http.StatusNoContent
		case "/synthesis":
			synthCalls.Add(1)
			body = wav
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})}

	home := t.TempDir()
	j, err := job.Create(home, "source.md", "test")
	if err != nil {
		t.Fatal(err)
	}
	speech := model.Speech{Backend: "voicevox", Voice: "3", Chunks: []model.Chunk{{ID: "c0001", UtteranceID: "u0001", Text: "テスト"}}}
	if err := jsonfile.Write(j.Path("30_speech.json"), speech); err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(j.Path("20_script.json"), model.Script{Title: "test", Utterances: []model.Utterance{{ID: "u0001", Role: "body", Text: "テスト"}}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Output.Format = "wav"
	preset := config.Preset{Name: "test", Backend: "voicevox", Endpoint: "http://voicevox.test", VoiceID: "3"}
	p, err := New(cfg, preset, j)
	if err != nil {
		t.Fatal(err)
	}
	p.Backend = voicevox.NewWithClient(preset.Endpoint, client)
	ctx := context.Background()
	if err := p.Query(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := p.Synthesize(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := p.Synthesize(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if synthCalls.Load() != 1 {
		t.Fatalf("synthesis calls before edit = %d", synthCalls.Load())
	}
	qpath := filepath.Join(j.Dir, "35_query", "c0001.json")
	var q tts.Query
	if err := jsonfile.Read(qpath, &q); err != nil {
		t.Fatal(err)
	}
	q.Doc = tts.Raw(`{"accent_phrases":[],"speedScale":0.9}`)
	if err := jsonfile.Write(qpath, q); err != nil {
		t.Fatal(err)
	}
	if err := p.Synthesize(ctx, false, nil); err != nil {
		t.Fatal(err)
	}
	if synthCalls.Load() != 2 {
		t.Fatalf("synthesis calls after edit = %d", synthCalls.Load())
	}
	out, err := p.Assemble(false, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(out); err != nil {
		t.Fatal(err)
	}
}

func TestQueryFallsBackWhenGeneratedKanaIsRejected(t *testing.T) {
	var audioQueryCalls atomic.Int32
	var validateCalls atomic.Int32
	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		status := http.StatusOK
		var body []byte
		switch r.URL.Path {
		case "/engine_manifest":
			body = []byte(`{"supported_features":{"adjust_mora_pitch":true}}`)
		case "/audio_query":
			audioQueryCalls.Add(1)
			body = []byte(`{"accent_phrases":[{"moras":[{"text":"テ","vowel":"e","vowel_length":0.1,"pitch":5.5}],"accent":1,"pause_mora":null,"is_interrogative":false}],"speedScale":1}`)
		case "/validate_kana":
			validateCalls.Add(1)
			status = http.StatusBadRequest
			body = []byte(`{"detail":{"error_name":"ACCENT_NOTFOUND"}}`)
		default:
			status = http.StatusNotFound
		}
		return &http.Response{StatusCode: status, Status: http.StatusText(status), Header: make(http.Header), Body: io.NopCloser(bytes.NewReader(body)), Request: r}, nil
	})}

	home := t.TempDir()
	j, err := job.Create(home, "source.md", "test")
	if err != nil {
		t.Fatal(err)
	}
	speech := model.Speech{Backend: "voicevox", Voice: "3", Chunks: []model.Chunk{{ID: "c0001", UtteranceID: "u0001", Text: "テスト", Kana: "テスト"}}}
	if err := jsonfile.Write(j.Path("30_speech.json"), speech); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	preset := config.Preset{Name: "test", Backend: "voicevox", Endpoint: "http://voicevox.test", VoiceID: "3"}
	p, err := New(cfg, preset, j)
	if err != nil {
		t.Fatal(err)
	}
	p.Backend = voicevox.NewWithClient(preset.Endpoint, client)
	if err := p.Query(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if validateCalls.Load() != 1 || audioQueryCalls.Load() != 1 {
		t.Fatalf("validate calls = %d, audio query calls = %d", validateCalls.Load(), audioQueryCalls.Load())
	}
	if _, err := os.Stat(filepath.Join(j.Dir, "35_query", "c0001.json")); err != nil {
		t.Fatal(err)
	}
}
