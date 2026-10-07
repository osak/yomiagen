package ingest

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/osak/yomiagen/internal/model"
)

var headingRE = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)

func parseMarkdown(s string) model.Document {
	d := model.Document{}
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	inCode := false
	var code, para []string
	flush := func() {
		if len(para) > 0 {
			addBlock(&d, "paragraph", 0, strings.Join(para, " "))
			para = nil
		}
	}
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			flush()
			if inCode {
				addBlock(&d, "code", 0, strings.Join(code, "\n"))
				code = nil
			}
			inCode = !inCode
			continue
		}
		if inCode {
			code = append(code, line)
			continue
		}
		if m := headingRE.FindStringSubmatch(line); m != nil {
			flush()
			level := len(m[1])
			addBlock(&d, "heading", level, m[2])
			if d.Title == "" {
				d.Title = m[2]
			}
			continue
		}
		t := strings.TrimSpace(line)
		if t == "" {
			flush()
			continue
		}
		if strings.HasPrefix(t, "- ") || strings.HasPrefix(t, "* ") {
			flush()
			addBlock(&d, "list_item", 0, t[2:])
			continue
		}
		if strings.HasPrefix(t, "> ") {
			flush()
			addBlock(&d, "quote", 0, t[2:])
			continue
		}
		para = append(para, t)
	}
	flush()
	if len(code) > 0 {
		addBlock(&d, "code", 0, strings.Join(code, "\n"))
	}
	return d
}
func parseText(s string) model.Document {
	d := model.Document{}
	for _, p := range regexp.MustCompile(`\n\s*\n`).Split(strings.TrimSpace(s), -1) {
		addBlock(&d, "paragraph", 0, p)
	}
	return d
}
func addBlock(d *model.Document, kind string, level int, text string) {
	text = strings.TrimSpace(text)
	if text == "" {
		return
	}
	d.Blocks = append(d.Blocks, model.Block{ID: fmt.Sprintf("b%04d", len(d.Blocks)+1), Kind: kind, Level: level, Text: text})
}
