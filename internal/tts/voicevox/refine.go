package voicevox

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"strings"

	"github.com/osak/yomiagen/internal/tts"
)

func (c *Client) Apply(_ context.Context, q *tts.Query, op tts.Op) (bool, error) {
	if op.Engine != "" && op.Engine != "voicevox" {
		return false, nil
	}
	var doc map[string]any
	if err := json.Unmarshal(q.Doc, &doc); err != nil {
		return false, err
	}
	var args map[string]any
	if len(op.Args) > 0 {
		if err := json.Unmarshal(op.Args, &args); err != nil {
			return false, fmt.Errorf("%s args: %w", op.Op, err)
		}
	}
	recalc := false
	switch op.Op {
	case "global":
		for k, v := range args {
			doc[k] = v
		}
	case "patch":
		merge(doc, args)
	case "accent":
		p, err := phrase(doc, op.Target.Phrase)
		if err != nil {
			return false, err
		}
		v, ok := number(args["accent"])
		if !ok {
			return false, errors.New("accent op requires numeric args.accent")
		}
		p["accent"] = v
		recalc = true
	case "emphasize":
		amount := .15
		if v, ok := number(args["amount"]); ok {
			amount = v
		}
		for _, m := range selectMoras(doc, op.Target) {
			if p, ok := number(m["pitch"]); ok && p > 0 {
				m["pitch"] = p + amount
			}
		}
	case "lengthen":
		factor := 1.2
		if v, ok := number(args["factor"]); ok {
			factor = v
		}
		for _, m := range selectMoras(doc, op.Target) {
			for _, k := range []string{"vowel_length", "consonant_length"} {
				if v, ok := number(m[k]); ok {
					m[k] = v * factor
				}
			}
		}
	case "devoice":
		for _, m := range selectMoras(doc, op.Target) {
			if v, ok := m["vowel"].(string); ok {
				m["vowel"] = strings.ToUpper(v)
				m["pitch"] = 0.0
			}
		}
	case "pause":
		p, err := phrase(doc, op.Target.Phrase)
		if err != nil {
			return false, err
		}
		seconds := .3
		if v, ok := number(args["seconds"]); ok {
			seconds = v
		}
		pm, _ := p["pause_mora"].(map[string]any)
		if pm == nil {
			pm = map[string]any{"text": "、", "consonant": nil, "consonant_length": nil, "vowel": "pau", "pitch": 0.0}
			p["pause_mora"] = pm
		}
		pm["vowel_length"] = seconds
	case "interrogative":
		p, err := phrase(doc, op.Target.Phrase)
		if err != nil {
			return false, err
		}
		p["is_interrogative"] = true
	case "kana":
		return false, errors.New("kana must be set on speech chunk before query generation")
	default:
		return false, fmt.Errorf("unknown voicevox op %q", op.Op)
	}
	b, err := json.Marshal(doc)
	if err == nil {
		q.Doc = b
	}
	return recalc, err
}

func phrase(doc map[string]any, n int) (map[string]any, error) {
	ps, ok := doc["accent_phrases"].([]any)
	if !ok || len(ps) == 0 {
		return nil, errors.New("AudioQuery has no accent phrases")
	}
	if n < 0 {
		n = len(ps) - 1
	}
	if n >= len(ps) {
		return nil, fmt.Errorf("phrase index %d out of range", n)
	}
	p, ok := ps[n].(map[string]any)
	if !ok {
		return nil, errors.New("invalid accent phrase")
	}
	return p, nil
}

func selectMoras(doc map[string]any, target tts.Target) []map[string]any {
	ps, _ := doc["accent_phrases"].([]any)
	var out []map[string]any
	for pi, pv := range ps {
		if target.Phrase >= 0 && pi != target.Phrase {
			continue
		}
		p, _ := pv.(map[string]any)
		ms, _ := p["moras"].([]any)
		for mi, mv := range ms {
			if target.Mora >= 0 && mi != target.Mora {
				continue
			}
			m, _ := mv.(map[string]any)
			if m == nil {
				continue
			}
			if target.Match != "" {
				s, _ := m["text"].(string)
				if !strings.Contains(target.Match, s) {
					continue
				}
			}
			out = append(out, m)
		}
	}
	return out
}
func number(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}
func merge(dst, src map[string]any) {
	for k, v := range src {
		if sm, ok := v.(map[string]any); ok {
			if dm, ok := dst[k].(map[string]any); ok {
				merge(dm, sm)
				continue
			}
		}
		dst[k] = v
	}
}
