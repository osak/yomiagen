package openai

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

var ErrRefusal = errors.New("OpenAI refusal")

type Client struct {
	Model, BaseURL, APIKey string
	HTTP                   *http.Client
}

type Usage struct {
	Input, Cached, Output int
}

type Result struct {
	Text    string
	Usage   Usage
	Elapsed time.Duration
}

func (c Client) Structured(ctx context.Context, instructions, input, name string, schemaBytes []byte) (Result, error) {
	var schema any
	if err := json.Unmarshal(schemaBytes, &schema); err != nil {
		return Result{}, err
	}
	body := map[string]any{
		"model":        c.Model,
		"instructions": instructions,
		"input":        input,
		"text": map[string]any{"format": map[string]any{
			"type": "json_schema", "name": name, "schema": schema, "strict": true,
		}},
	}
	b, err := json.Marshal(body)
	if err != nil {
		return Result{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(c.BaseURL, "/")+"/responses", bytes.NewReader(b))
	if err != nil {
		return Result{}, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	httpClient := c.HTTP
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 3 * time.Minute}
	}
	start := time.Now()
	resp, err := httpClient.Do(req)
	elapsed := time.Since(start)
	if err != nil {
		return Result{Elapsed: elapsed}, err
	}
	defer resp.Body.Close()
	rb, _ := io.ReadAll(resp.Body)
	if resp.StatusCode/100 != 2 {
		return Result{Elapsed: elapsed}, fmt.Errorf("OpenAI: HTTP %s: %s", resp.Status, limit(rb, 800))
	}
	var raw struct {
		Output []struct {
			Content []struct {
				Type    string `json:"type"`
				Text    string `json:"text"`
				Refusal string `json:"refusal"`
			} `json:"content"`
		} `json:"output"`
		Usage struct {
			Input   int `json:"input_tokens"`
			Output  int `json:"output_tokens"`
			Details struct {
				Cached int `json:"cached_tokens"`
			} `json:"input_tokens_details"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rb, &raw); err != nil {
		return Result{Elapsed: elapsed}, err
	}
	var text string
	for _, o := range raw.Output {
		for _, content := range o.Content {
			if content.Refusal != "" {
				return Result{Elapsed: elapsed}, fmt.Errorf("%w: %s", ErrRefusal, content.Refusal)
			}
			if content.Type == "output_text" || content.Text != "" {
				text += content.Text
			}
		}
	}
	if text == "" {
		return Result{Elapsed: elapsed}, errors.New("OpenAI response contained no output text")
	}
	return Result{
		Text: text,
		Usage: Usage{
			Input: raw.Usage.Input, Cached: raw.Usage.Details.Cached, Output: raw.Usage.Output,
		},
		Elapsed: elapsed,
	}, nil
}

func limit(b []byte, n int) []byte {
	if len(b) > n {
		return b[:n]
	}
	return b
}
