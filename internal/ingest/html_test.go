package ingest

import "testing"

func TestParseHTML(t *testing.T) {
	src := []byte(`<!doctype html><html><head><title>Example</title></head><body><nav>skip</nav><article><h1>見出し</h1><p>Hello <strong>world</strong>.</p><table><tr><th>A</th><th>B</th></tr><tr><td>1</td><td>2</td></tr></table></article></body></html>`)
	d, err := parseHTML(src)
	if err != nil {
		t.Fatal(err)
	}
	if d.Title != "Example" {
		t.Fatalf("title=%q", d.Title)
	}
	if len(d.Blocks) != 3 {
		t.Fatalf("blocks=%#v", d.Blocks)
	}
	if d.Blocks[1].Text != "Hello world ." {
		t.Fatalf("text=%q", d.Blocks[1].Text)
	}
	if d.Blocks[2].Text != "A\tB\n1\t2" {
		t.Fatalf("table=%q", d.Blocks[2].Text)
	}
}
