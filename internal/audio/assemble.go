package audio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Chapter struct {
	Title          string
	StartMS, EndMS int64
}

func WriteChapters(path, title string, chapters []Chapter) error {
	var b strings.Builder
	b.WriteString(";FFMETADATA1\n")
	b.WriteString("title=" + escape(title) + "\n")
	for _, c := range chapters {
		fmt.Fprintf(&b, "[CHAPTER]\nTIMEBASE=1/1000\nSTART=%d\nEND=%d\ntitle=%s\n", c.StartMS, c.EndMS, escape(c.Title))
	}
	return os.WriteFile(path, []byte(b.String()), 0o644)
}
func Encode(wav, metadata, out, bitrate string, loudnorm bool) error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return err
	}
	args := []string{"-y", "-hide_banner", "-loglevel", "error", "-i", wav, "-i", metadata, "-map_metadata", "1"}
	if loudnorm {
		args = append(args, "-af", "loudnorm")
	}
	codec := "aac"
	if strings.EqualFold(filepath.Ext(out), ".mp3") {
		codec = "libmp3lame"
	}
	args = append(args, "-c:a", codec, "-b:a", bitrate, out)
	cmd := exec.Command("ffmpeg", args...)
	if b, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("ffmpeg: %w: %s", err, b)
	}
	return nil
}
func OutputPath(dir, format string) string {
	if format == "" || format == "wav" {
		return filepath.Join(dir, "50_output.wav")
	}
	return filepath.Join(dir, "50_output."+format)
}
func escape(s string) string {
	r := strings.NewReplacer("\\", "\\\\", "=", "\\=", ";", "\\;", "#", "\\#", "\n", " ")
	return r.Replace(s)
}
