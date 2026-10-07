package cost

import (
	"bufio"
	json "encoding/json/v2"
	"os"
	"time"
)

type Record struct {
	Time         time.Time `json:"time"`
	JobID        string    `json:"job_id"`
	Stage        string    `json:"stage"`
	Provider     string    `json:"provider"`
	Model        string    `json:"model"`
	InputTokens  int       `json:"input_tokens,omitzero"`
	CachedTokens int       `json:"cached_tokens,omitzero"`
	OutputTokens int       `json:"output_tokens,omitzero"`
	Characters   int       `json:"characters,omitzero"`
	AudioSeconds float64   `json:"audio_seconds,omitzero"`
	WallMS       int64     `json:"wall_ms"`
	CostUSD      float64   `json:"cost_usd"`
}

func Append(path string, r Record) error {
	if r.Time.IsZero() {
		r.Time = time.Now()
	}
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.Write(append(b, '\n'))
	return err
}

func Read(path string) ([]Record, error) {
	f, err := os.Open(path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Record
	s := bufio.NewScanner(f)
	for s.Scan() {
		var r Record
		if err := json.Unmarshal([]byte(s.Text()), &r); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, s.Err()
}
