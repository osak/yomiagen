package job

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/osak/yomiagen/internal/jsonfile"
)

type StageState struct {
	InputHash string    `json:"input_hash"`
	UpdatedAt time.Time `json:"updated_at"`
}

type Meta struct {
	ID       string                `json:"id"`
	Source   string                `json:"source"`
	Preset   string                `json:"preset"`
	Created  time.Time             `json:"created"`
	Stages   map[string]StageState `json:"stages"`
	Chunks   map[string]string     `json:"chunk_hashes"`
	DictUUID map[string]string     `json:"dict_uuids"`
}

type Job struct {
	Dir  string
	Meta Meta
}

func Create(home, source, preset string) (*Job, error) {
	base := strings.TrimSuffix(filepath.Base(source), filepath.Ext(source))
	base = regexp.MustCompile(`[^[:alnum:]_-]+`).ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if base == "" {
		base = "document"
	}
	if len(base) > 40 {
		base = base[:40]
	}
	id := time.Now().Format("20060102-150405") + "-" + strings.ToLower(base)
	dir := filepath.Join(home, "jobs", id)
	for n := 2; ; n++ {
		if _, err := os.Stat(dir); errors.Is(err, os.ErrNotExist) {
			break
		}
		id = fmt.Sprintf("%s-%d", id, n)
		dir = filepath.Join(home, "jobs", id)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	j := &Job{Dir: dir, Meta: Meta{ID: id, Source: source, Preset: preset, Created: time.Now(), Stages: map[string]StageState{}, Chunks: map[string]string{}, DictUUID: map[string]string{}}}
	return j, j.Save()
}

func Open(home, id string) (*Job, error) {
	dir := id
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(home, "jobs", id)
	}
	var m Meta
	if err := jsonfile.Read(filepath.Join(dir, "job.json"), &m); err != nil {
		return nil, err
	}
	if m.Stages == nil {
		m.Stages = map[string]StageState{}
	}
	if m.Chunks == nil {
		m.Chunks = map[string]string{}
	}
	if m.DictUUID == nil {
		m.DictUUID = map[string]string{}
	}
	return &Job{Dir: dir, Meta: m}, nil
}

func (j *Job) Save() error             { return jsonfile.Write(filepath.Join(j.Dir, "job.json"), j.Meta) }
func (j *Job) Path(name string) string { return filepath.Join(j.Dir, name) }
func (j *Job) Fresh(stage, hash string, outputs ...string) bool {
	s, ok := j.Meta.Stages[stage]
	if !ok || s.InputHash != hash {
		return false
	}
	for _, p := range outputs {
		if _, err := os.Stat(j.Path(p)); err != nil {
			return false
		}
	}
	return true
}
func (j *Job) Done(stage, hash string) error {
	j.Meta.Stages[stage] = StageState{InputHash: hash, UpdatedAt: time.Now()}
	return j.Save()
}

func Hash(parts ...[]byte) string {
	h := sha256.New()
	for _, p := range parts {
		h.Write(p)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
func HashFiles(paths ...string) (string, error) {
	var all [][]byte
	for _, p := range paths {
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		all = append(all, b)
	}
	return Hash(all...), nil
}

func List(home string) ([]Meta, error) {
	dirs, err := os.ReadDir(filepath.Join(home, "jobs"))
	if err != nil {
		return nil, err
	}
	var out []Meta
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		j, err := Open(home, d.Name())
		if err == nil {
			out = append(out, j.Meta)
		}
	}
	sort.Slice(out, func(i, k int) bool { return out[i].Created.After(out[k].Created) })
	return out, nil
}
