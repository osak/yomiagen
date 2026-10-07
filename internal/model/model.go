package model

import "github.com/osak/yomiagen/internal/tts"

type Document struct {
	Title    string  `json:"title"`
	Source   string  `json:"source"`
	Language string  `json:"language"`
	Blocks   []Block `json:"blocks"`
}

type Block struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Level int    `json:"level,omitzero"`
	Text  string `json:"text"`
	Lang  string `json:"lang,omitzero"`
}

type Script struct {
	Title      string      `json:"title"`
	Language   string      `json:"language"`
	Utterances []Utterance `json:"utterances"`
	Lexicon    []LexEntry  `json:"lexicon"`
}

type Utterance struct {
	ID          string `json:"id"`
	SourceBlock string `json:"source_block"`
	Role        string `json:"role"`
	Level       int    `json:"level,omitzero"`
	Text        string `json:"text"`
}

type LexEntry struct {
	Surface       string `json:"surface"`
	Pronunciation string `json:"pronunciation"`
	AccentType    int    `json:"accent_type"`
	WordType      string `json:"word_type"`
	Note          string `json:"note,omitzero"`
}

type Speech struct {
	Backend string   `json:"backend"`
	Voice   string   `json:"voice"`
	Global  []tts.Op `json:"global"`
	Chunks  []Chunk  `json:"chunks"`
}

type Chunk struct {
	ID          string   `json:"id"`
	UtteranceID string   `json:"utterance_id"`
	Text        string   `json:"text"`
	Kana        string   `json:"kana,omitzero"`
	Ops         []tts.Op `json:"ops,omitzero"`
}
