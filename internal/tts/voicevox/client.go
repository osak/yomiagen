package voicevox

import (
	"bytes"
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/osak/yomiagen/internal/tts"
)

type Client struct {
	endpoint string
	http     *http.Client
}

func New(endpoint string) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: &http.Client{Timeout: 2 * time.Minute}}
}
func NewWithClient(endpoint string, client *http.Client) *Client {
	return &Client{endpoint: strings.TrimRight(endpoint, "/"), http: client}
}
func (c *Client) Name() string { return "voicevox" }

func (c *Client) call(ctx context.Context, method, path string, query url.Values, body []byte, contentType string) ([]byte, error) {
	u := c.endpoint + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("VOICEVOX %s: %w", path, err)
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode/100 != 2 {
		if len(b) > 600 {
			b = b[:600]
		}
		return nil, fmt.Errorf("VOICEVOX %s: HTTP %s: %s", path, resp.Status, b)
	}
	return b, nil
}

func (c *Client) Capabilities(ctx context.Context) (tts.CapSet, error) {
	b, err := c.call(ctx, http.MethodGet, "/engine_manifest", nil, nil, "")
	if err != nil {
		return nil, err
	}
	var v struct {
		Supported map[string]bool `json:"supported_features"`
	}
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	out := tts.CapSet{}
	for k, ok := range v.Supported {
		out[tts.Capability(k)] = ok
	}
	return out, nil
}

func (c *Client) Voices(ctx context.Context) ([]tts.Voice, error) {
	b, err := c.call(ctx, http.MethodGet, "/speakers", nil, nil, "")
	if err != nil {
		return nil, err
	}
	var speakers []struct {
		Name   string `json:"name"`
		Styles []struct {
			Name string `json:"name"`
			ID   int    `json:"id"`
		} `json:"styles"`
	}
	if err := json.Unmarshal(b, &speakers); err != nil {
		return nil, err
	}
	var out []tts.Voice
	for _, s := range speakers {
		for _, st := range s.Styles {
			out = append(out, tts.Voice{ID: strconv.Itoa(st.ID), Name: s.Name + " / " + st.Name, Language: "ja"})
		}
	}
	return out, nil
}

func (c *Client) BuildQuery(ctx context.Context, text, voice string, opt tts.QueryOptions) (*tts.Query, error) {
	if opt.Kana != "" {
		if _, err := c.call(ctx, http.MethodPost, "/validate_kana", url.Values{"text": {opt.Kana}}, nil, ""); err != nil {
			return nil, err
		}
	}
	q := url.Values{"text": {text}, "speaker": {voice}, "enable_katakana_english": {strconv.FormatBool(opt.KatakanaEnglish)}}
	b, err := c.call(ctx, http.MethodPost, "/audio_query", q, nil, "")
	if err != nil {
		return nil, err
	}
	if opt.Kana != "" {
		phrases, err := c.call(ctx, http.MethodPost, "/accent_phrases", url.Values{"text": {opt.Kana}, "speaker": {voice}, "is_kana": {"true"}}, nil, "")
		if err != nil {
			return nil, err
		}
		var doc map[string]any
		var ap any
		if err := json.Unmarshal(b, &doc); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(phrases, &ap); err != nil {
			return nil, err
		}
		doc["accent_phrases"] = ap
		b, err = json.Marshal(doc)
		if err != nil {
			return nil, err
		}
	}
	return &tts.Query{Engine: "voicevox", Voice: voice, Doc: append([]byte(nil), b...)}, nil
}

func (c *Client) SynthesizeQuery(ctx context.Context, q *tts.Query, voice string) (*tts.Result, error) {
	up := "false"
	var doc map[string]any
	if json.Unmarshal(q.Doc, &doc) == nil {
		if phrases, ok := doc["accent_phrases"].([]any); ok {
			for _, p := range phrases {
				if m, ok := p.(map[string]any); ok {
					if v, _ := m["is_interrogative"].(bool); v {
						up = "true"
					}
				}
			}
		}
	}
	b, err := c.call(ctx, http.MethodPost, "/synthesis", url.Values{"speaker": {voice}, "enable_interrogative_upspeak": {up}}, q.Doc, "application/json")
	if err != nil {
		return nil, err
	}
	return &tts.Result{WAV: b}, nil
}

func (c *Client) Synthesize(ctx context.Context, req tts.Request) (*tts.Result, error) {
	q, err := c.BuildQuery(ctx, req.Text, req.VoiceID, tts.QueryOptions{KatakanaEnglish: true})
	if err != nil {
		return nil, err
	}
	if req.Speed != 0 || req.Volume != 0 {
		args := map[string]any{}
		if req.Speed != 0 {
			args["speedScale"] = req.Speed
		}
		if req.Volume != 0 {
			args["volumeScale"] = req.Volume
		}
		b, _ := json.Marshal(args)
		_, err = c.Apply(ctx, q, tts.Op{Op: "global", Args: b})
		if err != nil {
			return nil, err
		}
	}
	return c.SynthesizeQuery(ctx, q, req.VoiceID)
}

func (c *Client) Recalculate(ctx context.Context, q *tts.Query) error {
	var doc map[string]any
	if err := json.Unmarshal(q.Doc, &doc); err != nil {
		return err
	}
	phrases, ok := doc["accent_phrases"]
	if !ok {
		return errors.New("AudioQuery has no accent_phrases")
	}
	b, err := json.Marshal(phrases)
	if err != nil {
		return err
	}
	out, err := c.call(ctx, http.MethodPost, "/mora_data", url.Values{"speaker": {q.Voice}}, b, "application/json")
	if err != nil {
		return err
	}
	var replaced any
	if err := json.Unmarshal(out, &replaced); err != nil {
		return err
	}
	doc["accent_phrases"] = replaced
	b, err = json.Marshal(doc)
	if err == nil {
		q.Doc = b
	}
	return err
}

func (c *Client) Initialize(ctx context.Context, voice string) error {
	_, err := c.call(ctx, http.MethodPost, "/initialize_speaker", url.Values{"speaker": {voice}}, nil, "")
	return err
}

type DictEntry struct {
	Surface, Pronunciation, WordType string
	AccentType                       int
}

func (c *Client) AddDictionary(ctx context.Context, d DictEntry) (string, error) {
	current, err := c.call(ctx, http.MethodGet, "/user_dict", nil, nil, "")
	if err != nil {
		return "", err
	}
	var words map[string]struct {
		Surface string `json:"surface"`
	}
	if err := json.Unmarshal(current, &words); err != nil {
		return "", err
	}
	q := url.Values{"surface": {d.Surface}, "pronunciation": {d.Pronunciation}, "accent_type": {strconv.Itoa(d.AccentType)}, "word_type": {d.WordType}, "priority": {"5"}}
	for id, word := range words {
		if word.Surface == d.Surface {
			_, err := c.call(ctx, http.MethodPut, "/user_dict_word/"+url.PathEscape(id), q, nil, "")
			return id, err
		}
	}
	b, err := c.call(ctx, http.MethodPost, "/user_dict_word", q, nil, "")
	if err != nil {
		return "", err
	}
	var id string
	if err := json.Unmarshal(b, &id); err == nil {
		return id, nil
	}
	return strings.Trim(string(b), `"\n `), nil
}
