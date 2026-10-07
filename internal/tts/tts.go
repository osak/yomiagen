package tts

import (
	"context"
	"encoding/json/jsontext"
)

type Backend interface {
	Name() string
	Capabilities(context.Context) (CapSet, error)
	Voices(context.Context) ([]Voice, error)
	Synthesize(context.Context, Request) (*Result, error)
}
type QueryBackend interface {
	Backend
	BuildQuery(context.Context, string, string, QueryOptions) (*Query, error)
	SynthesizeQuery(context.Context, *Query, string) (*Result, error)
}
type Refiner interface {
	Apply(context.Context, *Query, Op) (recalculate bool, err error)
}
type QueryRecalculator interface {
	Recalculate(context.Context, *Query) error
}

type Request struct {
	Text, VoiceID string
	Speed, Volume float64
}
type Result struct {
	WAV                   []byte
	Characters            int
	AudioSeconds, CostUSD float64
}
type Voice struct{ ID, Name, Language string }
type Query struct {
	Engine string         `json:"engine"`
	Voice  string         `json:"voice"`
	Doc    jsontext.Value `json:"doc"`
}
type QueryOptions struct {
	KatakanaEnglish bool
	Kana            string
}
type Op struct {
	Op     string         `json:"op"`
	Engine string         `json:"engine,omitzero"`
	Needs  []Capability   `json:"needs,omitzero"`
	Target Target         `json:"target,omitzero"`
	Args   jsontext.Value `json:"args,omitzero"`
}
type Target struct {
	Phrase int    `json:"phrase,omitzero"`
	Mora   int    `json:"mora,omitzero"`
	Match  string `json:"match,omitzero"`
}
type Capability string
type CapSet map[Capability]bool
type DegradePolicy string

const (
	DegradeSkip  DegradePolicy = "skip"
	DegradeError DegradePolicy = "error"
)

func Raw(s string) jsontext.Value { return jsontext.Value([]byte(s)) }

func Required(op Op) []Capability {
	if len(op.Needs) > 0 {
		return op.Needs
	}
	switch op.Op {
	case "accent", "emphasize", "devoice":
		return []Capability{"adjust_mora_pitch"}
	case "lengthen":
		return []Capability{"adjust_phoneme_length"}
	case "pause":
		return []Capability{"adjust_pause_length"}
	case "interrogative":
		return []Capability{"interrogative_upspeak"}
	default:
		return nil
	}
}

func Supported(c CapSet, op Op) bool {
	for _, need := range Required(op) {
		if !c[need] {
			return false
		}
	}
	return true
}
