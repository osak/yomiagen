package script

import (
	"context"
	_ "embed"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/cost"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
	openaiapi "github.com/osak/yomiagen/internal/openai"
)

//go:embed prompt.md
var prompt string

//go:embed schema.json
var schemaBytes []byte

func Run(ctx context.Context, j *job.Job, cfg config.Config, force bool) error {
	docPath := j.Path("10_document.json")
	b, err := os.ReadFile(docPath)
	if err != nil {
		return err
	}
	mode := "local"
	key := os.Getenv("OPENAI_API_KEY")
	if key != "" {
		mode = "openai"
	}
	hash := job.Hash(b, []byte(cfg.OpenAI.Model), []byte(mode))
	if !force && j.Fresh("script", hash, "20_script.json") {
		return nil
	}
	var d model.Document
	if err := json.Unmarshal(b, &d); err != nil {
		return err
	}
	var out model.Script
	if key == "" {
		out = local(d)
	} else {
		out, err = remote(ctx, j, d, cfg, key)
		if err != nil {
			return err
		}
	}
	if err := jsonfile.Write(j.Path("20_script.json"), out); err != nil {
		return err
	}
	return j.Done("script", hash)
}

func local(d model.Document) model.Script {
	s := model.Script{Title: d.Title, Language: d.Language}
	for _, b := range d.Blocks {
		if strings.TrimSpace(b.Text) == "" {
			continue
		}
		role := "body"
		if b.Kind == "heading" {
			role = "heading"
		}
		if b.Kind == "code" || b.Kind == "caption" {
			role = "note"
		}
		s.Utterances = append(s.Utterances, model.Utterance{ID: fmt.Sprintf("u%04d", len(s.Utterances)+1), SourceBlock: b.ID, Role: role, Level: b.Level, Text: b.Text})
	}
	return s
}

func remote(ctx context.Context, j *job.Job, d model.Document, cfg config.Config, key string) (model.Script, error) {
	groups := group(d.Blocks, cfg.OpenAI.ChunkChars)
	out := model.Script{Title: d.Title, Language: d.Language}
	previous := ""
	for _, g := range groups {
		payload, _ := json.Marshal(g)
		input := string(payload)
		if previous != "" {
			input = "<previous_context>" + previous + "</previous_context>\n" + input
		}
		part, usage, elapsed, err := request(ctx, cfg, key, input)
		if err != nil {
			if errors.Is(err, openaiapi.ErrRefusal) {
				slog.Warn("OpenAI refused a document part; using source text", "error", err)
				fallback := local(model.Document{Title: d.Title, Language: d.Language, Blocks: g})
				part.Utterances = fallback.Utterances
			} else {
				return out, err
			}
		}
		for _, u := range part.Utterances {
			u.ID = fmt.Sprintf("u%04d", len(out.Utterances)+1)
			out.Utterances = append(out.Utterances, u)
		}
		out.Lexicon = append(out.Lexicon, part.Lexicon...)
		if len(out.Utterances) > 0 {
			previous = tail(out.Utterances[len(out.Utterances)-1].Text, 200)
		}
		price := cfg.Pricing.OpenAI[cfg.OpenAI.Model]
		usd := (float64(usage.Input-usage.Cached)*price.Input + float64(usage.Cached)*price.Cached + float64(usage.Output)*price.Output) / 1e6
		_ = cost.Append(j.Path("cost.jsonl"), cost.Record{JobID: j.Meta.ID, Stage: "script", Provider: "openai", Model: cfg.OpenAI.Model, InputTokens: usage.Input, CachedTokens: usage.Cached, OutputTokens: usage.Output, WallMS: elapsed.Milliseconds(), CostUSD: usd})
	}
	return out, nil
}

type usage struct{ Input, Cached, Output int }

func request(ctx context.Context, cfg config.Config, key, input string) (model.Script, usage, time.Duration, error) {
	result, err := (openaiapi.Client{Model: cfg.OpenAI.Model, BaseURL: cfg.OpenAI.BaseURL, APIKey: key}).Structured(ctx, prompt, input, "script", schemaBytes)
	if err != nil {
		return model.Script{}, usage{}, result.Elapsed, err
	}
	var out model.Script
	if err := json.Unmarshal([]byte(result.Text), &out); err != nil {
		return out, usage{}, result.Elapsed, fmt.Errorf("decode OpenAI structured output: %w", err)
	}
	return out, usage{result.Usage.Input, result.Usage.Cached, result.Usage.Output}, result.Elapsed, nil
}
func group(blocks []model.Block, max int) [][]model.Block {
	if max <= 0 {
		max = 4000
	}
	var out [][]model.Block
	var cur []model.Block
	n := 0
	for _, b := range blocks {
		l := utf8.RuneCountInString(b.Text)
		if len(cur) > 0 && n+l > max {
			out = append(out, cur)
			cur = nil
			n = 0
		}
		cur = append(cur, b)
		n += l
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}
func tail(s string, n int) string {
	r := []rune(s)
	if len(r) > n {
		r = r[len(r)-n:]
	}
	return string(r)
}
