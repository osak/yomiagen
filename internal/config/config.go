package config

import (
	"errors"
	"os"
	"path/filepath"

	"github.com/osak/yomiagen/internal/jsonfile"
	"github.com/osak/yomiagen/internal/tts"
)

type Config struct {
	DefaultPreset string                    `json:"default_preset"`
	OpenAI        OpenAI                    `json:"openai"`
	Presets       []Preset                  `json:"presets"`
	Prosody       map[string]ProsodyProfile `json:"prosody_profiles"`
	Chunk         Chunk                     `json:"chunk"`
	Concurrency   Concurrency               `json:"concurrency"`
	DegradePolicy tts.DegradePolicy         `json:"degrade_policy"`
	Output        Output                    `json:"output"`
	Pricing       Pricing                   `json:"pricing"`
}

type OpenAI struct {
	Model         string `json:"model"`
	BaseURL       string `json:"base_url"`
	ChunkChars    int    `json:"chunk_chars"`
	Pronunciation string `json:"pronunciation"`
}

type Preset struct {
	Name      string `json:"name"`
	Backend   string `json:"backend"`
	Endpoint  string `json:"endpoint"`
	VoiceID   string `json:"voice_id"`
	VoiceName string `json:"voice_name"`
	Prosody   string `json:"prosody"`
}

type ProsodyProfile struct {
	Global  []tts.Op       `json:"global"`
	PauseMS map[string]int `json:"pause_ms"`
	Heading []tts.Op       `json:"heading"`
}

type Chunk struct {
	MaxChars int `json:"max_chars"`
}
type Concurrency struct {
	TTS int `json:"tts"`
}
type Output struct {
	Format   string `json:"format"`
	Bitrate  string `json:"bitrate"`
	Loudnorm bool   `json:"loudnorm"`
}
type Price struct {
	Input  float64 `json:"input"`
	Cached float64 `json:"cached"`
	Output float64 `json:"output"`
}
type Pricing struct {
	OpenAI map[string]Price `json:"openai"`
}

func Home() (string, error) {
	if p := os.Getenv("YOMIAGEN_HOME"); p != "" {
		return p, nil
	}
	if p := os.Getenv("XDG_DATA_HOME"); p != "" {
		return filepath.Join(p, "yomiagen"), nil
	}
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(h, ".local", "share", "yomiagen"), nil
}

func Default() Config {
	return Config{
		DefaultPreset: "voicevox-zundamon",
		OpenAI:        OpenAI{Model: "gpt-5.6-luna", BaseURL: "https://api.openai.com/v1", ChunkChars: 4000, Pronunciation: "auto"},
		Presets:       []Preset{{Name: "voicevox-zundamon", Backend: "voicevox", Endpoint: "http://127.0.0.1:50021", VoiceID: "3", VoiceName: "ずんだもん / ノーマル", Prosody: "narration-ja"}},
		Prosody: map[string]ProsodyProfile{"narration-ja": {
			Global:  []tts.Op{{Op: "global", Args: tts.Raw(`{"speedScale":1.15,"intonationScale":1.1,"volumeScale":1.0,"pauseLengthScale":1.3}`)}},
			PauseMS: map[string]int{"sentence": 300, "paragraph": 600, "before_heading": 1000, "after_heading": 500, "before_section": 1500},
			Heading: []tts.Op{{Op: "global", Args: tts.Raw(`{"speedScale":1.0}`)}},
		}},
		Chunk: Chunk{MaxChars: 200}, Concurrency: Concurrency{TTS: 1}, DegradePolicy: tts.DegradeSkip,
		Output:  Output{Format: "m4a", Bitrate: "64k", Loudnorm: true},
		Pricing: Pricing{OpenAI: map[string]Price{"gpt-5.6-luna": {Input: .20, Cached: .02, Output: 1.20}, "gpt-5.6-terra": {Input: 2, Cached: .20, Output: 12}}},
	}
}

func Load() (Config, string, error) {
	home, err := Home()
	if err != nil {
		return Config{}, "", err
	}
	if err := os.MkdirAll(filepath.Join(home, "jobs"), 0o755); err != nil {
		return Config{}, "", err
	}
	path := filepath.Join(home, "config.json")
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		c := Default()
		if err := jsonfile.Write(path, c); err != nil {
			return Config{}, "", err
		}
		return c, home, nil
	}
	var c Config
	if err := jsonfile.Read(path, &c); err != nil {
		return Config{}, "", err
	}
	return c, home, nil
}

func (c Config) Preset(name string) (Preset, error) {
	if name == "" {
		name = c.DefaultPreset
	}
	for _, p := range c.Presets {
		if p.Name == name {
			return p, nil
		}
	}
	return Preset{}, errors.New("unknown preset: " + name)
}
