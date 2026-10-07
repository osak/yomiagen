package pronounce

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"

	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
	openaiapi "github.com/osak/yomiagen/internal/openai"
)

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSplitKeepsDecimalTogether(t *testing.T) {
	got := split("バージョン 1.26.2 です。 次です？", 200)
	if len(got) != 2 {
		t.Fatalf("got %#v", got)
	}
	if got[0] != "バージョン 1.26.2 です。" {
		t.Fatalf("got %q", got[0])
	}
}
func TestSplitLongPrefersComma(t *testing.T) {
	got := splitLong("あいうえお、かきくけこさしすせそ", 8)
	if len(got) < 2 || got[0] != "あいうえお、" {
		t.Fatalf("got %#v", got)
	}
}

func TestRunAddsOpenAIPronunciationPlan(t *testing.T) {
	var requestBody string
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		body, _ := io.ReadAll(r.Body)
		requestBody = string(body)
		response := `{"output":[{"content":[{"type":"output_text","text":"{\"chunks\":[{\"id\":\"c0001\",\"kana\":\"コレワ/テ'ストデス\"}]}"}]}],"usage":{"input_tokens":30,"input_tokens_details":{"cached_tokens":5},"output_tokens":12}}`
		return &http.Response{StatusCode: http.StatusOK, Status: "200 OK", Header: make(http.Header), Body: io.NopCloser(strings.NewReader(response)), Request: r}, nil
	})}
	oldClient := newOpenAIClient
	newOpenAIClient = func(cfg config.Config, key string) openaiapi.Client {
		return openaiapi.Client{Model: cfg.OpenAI.Model, BaseURL: cfg.OpenAI.BaseURL, APIKey: key, HTTP: httpClient}
	}
	t.Cleanup(func() { newOpenAIClient = oldClient })
	t.Setenv("OPENAI_API_KEY", "test-key")

	home := t.TempDir()
	j, err := job.Create(home, "source.md", "voicevox-zundamon")
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(j.Path("20_script.json"), model.Script{Utterances: []model.Utterance{{ID: "u0001", Role: "body", Text: "これはテストです"}}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OpenAI.BaseURL = "http://openai.test"
	cfg.OpenAI.Model = "test-model"
	if err := Run(context.Background(), j, cfg, cfg.Presets[0], true); err != nil {
		t.Fatal(err)
	}
	var speech model.Speech
	if err := jsonfile.Read(j.Path("30_speech.json"), &speech); err != nil {
		t.Fatal(err)
	}
	if got := speech.Chunks[0].Kana; got != "コレワ/テ'ストデス" {
		t.Fatalf("kana = %q", got)
	}
	if !strings.Contains(requestBody, `"name":"pronunciation_plan"`) || !strings.Contains(requestBody, "c0001") {
		t.Fatalf("unexpected request: %s", requestBody)
	}
}

func TestRunFallsBackWhenOpenAIPronunciationFails(t *testing.T) {
	httpClient := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusServiceUnavailable, Status: "503 Service Unavailable", Header: make(http.Header), Body: io.NopCloser(strings.NewReader("temporary failure")), Request: r}, nil
	})}
	oldClient := newOpenAIClient
	newOpenAIClient = func(cfg config.Config, key string) openaiapi.Client {
		return openaiapi.Client{Model: cfg.OpenAI.Model, BaseURL: cfg.OpenAI.BaseURL, APIKey: key, HTTP: httpClient}
	}
	t.Cleanup(func() { newOpenAIClient = oldClient })
	t.Setenv("OPENAI_API_KEY", "test-key")

	home := t.TempDir()
	j, err := job.Create(home, "source.md", "voicevox-zundamon")
	if err != nil {
		t.Fatal(err)
	}
	if err := jsonfile.Write(j.Path("20_script.json"), model.Script{Utterances: []model.Utterance{{ID: "u0001", Role: "body", Text: "これはテストです"}}}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.OpenAI.BaseURL = "http://openai.test"
	if err := Run(context.Background(), j, cfg, cfg.Presets[0], true); err != nil {
		t.Fatal(err)
	}
	var speech model.Speech
	if err := jsonfile.Read(j.Path("30_speech.json"), &speech); err != nil {
		t.Fatal(err)
	}
	if speech.Chunks[0].Kana != "" {
		t.Fatalf("kana = %q, want fallback", speech.Chunks[0].Kana)
	}
}
