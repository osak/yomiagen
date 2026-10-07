package voicevox

import (
	"context"
	json "encoding/json/v2"
	"testing"

	"github.com/osak/yomiagen/internal/tts"
)

func TestRefineAccentAndEmphasize(t *testing.T) {
	q := &tts.Query{Engine: "voicevox", Voice: "3", Doc: tts.Raw(`{"accent_phrases":[{"moras":[{"text":"テ","vowel":"e","vowel_length":0.1,"pitch":5.5}],"accent":1,"pause_mora":null,"is_interrogative":false}],"speedScale":1}`)}
	c := New("http://unused")
	recalc, err := c.Apply(context.Background(), q, tts.Op{Op: "accent", Target: tts.Target{Phrase: 0, Mora: -1}, Args: tts.Raw(`{"accent":2}`)})
	if err != nil || !recalc {
		t.Fatalf("recalc=%v err=%v", recalc, err)
	}
	_, err = c.Apply(context.Background(), q, tts.Op{Op: "emphasize", Target: tts.Target{Phrase: 0, Mora: 0}, Args: tts.Raw(`{"amount":0.2}`)})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(q.Doc, &doc); err != nil {
		t.Fatal(err)
	}
	p := doc["accent_phrases"].([]any)[0].(map[string]any)
	if p["accent"].(float64) != 2 {
		t.Fatalf("accent=%v", p["accent"])
	}
	m := p["moras"].([]any)[0].(map[string]any)
	if m["pitch"].(float64) < 5.69 {
		t.Fatalf("pitch=%v", m["pitch"])
	}
}
