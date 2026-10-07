package ingest

import (
	"bytes"
	"fmt"
	"strings"
	"unicode"

	"github.com/osak/yomiagen/internal/model"
	"golang.org/x/net/html"
)

var blockTags = map[string]string{"p": "paragraph", "li": "list_item", "blockquote": "quote", "pre": "code", "table": "table", "figcaption": "caption"}
var discardTags = map[string]bool{"script": true, "style": true, "nav": true, "header": true, "footer": true, "aside": true, "form": true, "noscript": true}

func parseHTML(b []byte) (model.Document, error) {
	root, err := html.Parse(bytes.NewReader(b))
	if err != nil {
		return model.Document{}, err
	}
	d := model.Document{Title: findTitle(root)}
	content := firstTag(root, "article", "main")
	if content == nil {
		content = firstRoleMain(root)
	}
	if content == nil {
		content = firstTag(root, "body")
	}
	if content == nil {
		content = root
	}
	var add func(*html.Node, bool)
	add = func(n *html.Node, blocked bool) {
		if n.Type == html.ElementNode && discardTags[n.Data] {
			return
		}
		kind, isBlock := blockTags[n.Data]
		level := 0
		if n.Type == html.ElementNode && len(n.Data) == 2 && n.Data[0] == 'h' && n.Data[1] >= '1' && n.Data[1] <= '6' {
			kind = "heading"
			level = int(n.Data[1] - '0')
			isBlock = true
		}
		if isBlock && !blocked {
			text := nodeText(n)
			if n.Data == "table" {
				text = tableText(n)
			}
			if text != "" {
				d.Blocks = append(d.Blocks, model.Block{ID: fmt.Sprintf("b%04d", len(d.Blocks)+1), Kind: kind, Level: level, Text: text})
			}
			return
		}
		if n.Type == html.ElementNode && n.Data == "img" && !blocked {
			for _, a := range n.Attr {
				if a.Key == "alt" && strings.TrimSpace(a.Val) != "" {
					d.Blocks = append(d.Blocks, model.Block{ID: fmt.Sprintf("b%04d", len(d.Blocks)+1), Kind: "caption", Text: clean(a.Val)})
					break
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			add(c, blocked || isBlock)
		}
	}
	add(content, false)
	return d, nil
}
func findTitle(n *html.Node) string {
	if t := firstTag(n, "title"); t != nil {
		if s := nodeText(t); s != "" {
			return s
		}
	}
	if h := firstTag(n, "h1"); h != nil {
		if s := nodeText(h); s != "" {
			return s
		}
	}
	var out string
	walk(n, func(x *html.Node) {
		if out != "" || x.Type != html.ElementNode || x.Data != "meta" {
			return
		}
		var prop, val string
		for _, a := range x.Attr {
			if a.Key == "property" {
				prop = a.Val
			}
			if a.Key == "content" {
				val = a.Val
			}
		}
		if prop == "og:title" {
			out = clean(val)
		}
	})
	return out
}
func firstTag(n *html.Node, tags ...string) *html.Node {
	var out *html.Node
	wanted := map[string]bool{}
	for _, t := range tags {
		wanted[t] = true
	}
	walk(n, func(x *html.Node) {
		if out == nil && x.Type == html.ElementNode && wanted[x.Data] {
			out = x
		}
	})
	return out
}
func firstRoleMain(n *html.Node) *html.Node {
	var out *html.Node
	walk(n, func(x *html.Node) {
		if out != nil {
			return
		}
		for _, a := range x.Attr {
			if a.Key == "role" && a.Val == "main" {
				out = x
			}
		}
	})
	return out
}
func walk(n *html.Node, f func(*html.Node)) {
	f(n)
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		walk(c, f)
	}
}
func nodeText(n *html.Node) string {
	var b strings.Builder
	walk(n, func(x *html.Node) {
		if x.Type == html.TextNode {
			b.WriteString(x.Data)
			b.WriteByte(' ')
		}
	})
	return clean(b.String())
}
func clean(s string) string {
	return strings.TrimSpace(strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, strings.Join(strings.Fields(s), " ")))
}
func tableText(n *html.Node) string {
	var rows []string
	walk(n, func(x *html.Node) {
		if x.Type != html.ElementNode || x.Data != "tr" {
			return
		}
		var cells []string
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				cells = append(cells, nodeText(c))
			}
		}
		if len(cells) > 0 {
			rows = append(rows, strings.Join(cells, "\t"))
		}
	})
	return strings.Join(rows, "\n")
}
