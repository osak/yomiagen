package job

import (
	"os"
	"testing"
)

func TestFreshUsesInputHash(t *testing.T) {
	j, err := Create(t.TempDir(), "article.md", "preset")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(j.Path("out"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := j.Done("stage", "one"); err != nil {
		t.Fatal(err)
	}
	if !j.Fresh("stage", "one", "out") {
		t.Fatal("expected fresh")
	}
	if j.Fresh("stage", "two", "out") {
		t.Fatal("expected stale")
	}
}
