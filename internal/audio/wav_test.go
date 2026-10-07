package audio

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJoin(t *testing.T) {
	dir := t.TempDir()
	a := Silence(.25)
	b := Silence(.5)
	pa := filepath.Join(dir, "a.wav")
	pb := filepath.Join(dir, "b.wav")
	if err := os.WriteFile(pa, a, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(pb, b, 0o644); err != nil {
		t.Fatal(err)
	}
	joined, starts, err := Join([]string{pa, pb})
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 2 || starts[0] != 0 || starts[1] < .249 || starts[1] > .251 {
		t.Fatalf("starts=%v", starts)
	}
	w, err := Parse(joined)
	if err != nil {
		t.Fatal(err)
	}
	if got := w.Duration(); got < .749 || got > .751 {
		t.Fatalf("duration=%f", got)
	}
}
