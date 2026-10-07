package ingest

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"

	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/model"
)

func Run(ctx context.Context, j *job.Job, force bool) error {
	b, kind, err := readSource(ctx, j.Meta.Source)
	if err != nil {
		return err
	}
	hash := job.Hash(b, []byte(kind))
	if !force && j.Fresh("ingest", hash, "10_document.json") {
		return nil
	}
	ext := ".txt"
	if kind == "html" {
		ext = ".html"
	} else if kind == "markdown" {
		ext = ".md"
	}
	if err := os.WriteFile(j.Path("00_source"+ext), b, 0o644); err != nil {
		return err
	}
	var d model.Document
	switch kind {
	case "html":
		d, err = parseHTML(b)
	case "markdown":
		d = parseMarkdown(string(b))
	default:
		d = parseText(string(b))
	}
	if err != nil {
		return err
	}
	d.Source = j.Meta.Source
	d.Language = language(d)
	if d.Title == "" {
		d.Title = strings.TrimSuffix(filepath.Base(j.Meta.Source), filepath.Ext(j.Meta.Source))
	}
	if err := jsonfile.Write(j.Path("10_document.json"), d); err != nil {
		return err
	}
	return j.Done("ingest", hash)
}

func readSource(ctx context.Context, source string) ([]byte, string, error) {
	u, _ := url.Parse(source)
	if u != nil && (u.Scheme == "http" || u.Scheme == "https") {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, source, nil)
		if err != nil {
			return nil, "", err
		}
		req.Header.Set("User-Agent", "yomiagen/0.1")
		client := &http.Client{Timeout: 30 * time.Second}
		resp, err := client.Do(req)
		if err != nil {
			return nil, "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode/100 != 2 {
			return nil, "", fmt.Errorf("GET %s: %s", source, resp.Status)
		}
		b, err := io.ReadAll(resp.Body)
		if err != nil {
			return nil, "", err
		}
		return b, detect(source, resp.Header.Get("Content-Type")), nil
	}
	b, err := os.ReadFile(source)
	if err != nil {
		return nil, "", err
	}
	return b, detect(source, ""), nil
}
func detect(name, contentType string) string {
	x := strings.ToLower(filepath.Ext(name))
	ct := strings.ToLower(contentType)
	if x == ".html" || x == ".htm" || strings.Contains(ct, "html") {
		return "html"
	}
	if x == ".md" || x == ".markdown" || strings.Contains(ct, "markdown") {
		return "markdown"
	}
	return "text"
}
func language(d model.Document) string {
	var jp, letters int
	for _, b := range d.Blocks {
		for _, r := range b.Text {
			if unicode.IsLetter(r) {
				letters++
			}
			if unicode.In(r, unicode.Hiragana, unicode.Katakana) {
				jp++
			}
		}
	}
	if letters > 0 && float64(jp)/float64(letters) >= .05 {
		return "ja"
	}
	return "en"
}
