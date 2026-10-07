package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"os"
)

type WAV struct {
	Format         []byte
	Data           []byte
	SampleRate     uint32
	Channels, Bits uint16
}

func Parse(b []byte) (WAV, error) {
	if len(b) < 12 || string(b[:4]) != "RIFF" || string(b[8:12]) != "WAVE" {
		return WAV{}, errors.New("not a RIFF/WAVE file")
	}
	var w WAV
	for pos := 12; pos+8 <= len(b); {
		name := string(b[pos : pos+4])
		n := int(binary.LittleEndian.Uint32(b[pos+4 : pos+8]))
		start := pos + 8
		end := start + n
		if end > len(b) {
			return WAV{}, errors.New("truncated WAV chunk")
		}
		switch name {
		case "fmt ":
			w.Format = append([]byte(nil), b[start:end]...)
			if n >= 16 {
				w.Channels = binary.LittleEndian.Uint16(w.Format[2:4])
				w.SampleRate = binary.LittleEndian.Uint32(w.Format[4:8])
				w.Bits = binary.LittleEndian.Uint16(w.Format[14:16])
			}
		case "data":
			w.Data = append(w.Data, b[start:end]...)
		}
		pos = end + n%2
	}
	if len(w.Format) == 0 || w.SampleRate == 0 {
		return WAV{}, errors.New("WAV missing fmt chunk")
	}
	return w, nil
}
func (w WAV) Duration() float64 {
	den := float64(w.SampleRate) * float64(w.Channels) * float64(w.Bits) / 8
	if den == 0 {
		return 0
	}
	return float64(len(w.Data)) / den
}
func Build(format, data []byte) []byte {
	size := 4 + (8 + len(format) + len(format)%2) + (8 + len(data) + len(data)%2)
	var b bytes.Buffer
	b.WriteString("RIFF")
	_ = binary.Write(&b, binary.LittleEndian, uint32(size))
	b.WriteString("WAVE")
	writeChunk(&b, "fmt ", format)
	writeChunk(&b, "data", data)
	return b.Bytes()
}
func writeChunk(b *bytes.Buffer, name string, data []byte) {
	b.WriteString(name)
	_ = binary.Write(b, binary.LittleEndian, uint32(len(data)))
	b.Write(data)
	if len(data)%2 == 1 {
		b.WriteByte(0)
	}
}
func Join(files []string) ([]byte, []float64, error) {
	var format, data []byte
	var starts []float64
	elapsed := 0.0
	for _, p := range files {
		b, err := os.ReadFile(p)
		if err != nil {
			return nil, nil, err
		}
		w, err := Parse(b)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", p, err)
		}
		if format == nil {
			format = w.Format
		} else if !bytes.Equal(format, w.Format) {
			return nil, nil, fmt.Errorf("%s: WAV format differs from previous chunks", p)
		}
		starts = append(starts, elapsed)
		elapsed += w.Duration()
		data = append(data, w.Data...)
	}
	if format == nil {
		return nil, nil, errors.New("no WAV chunks")
	}
	return Build(format, data), starts, nil
}
func Silence(seconds float64) []byte {
	rate := uint32(24000)
	channels := uint16(1)
	bits := uint16(16)
	byteRate := rate * uint32(channels) * uint32(bits) / 8
	block := channels * bits / 8
	fmtb := make([]byte, 16)
	binary.LittleEndian.PutUint16(fmtb[0:2], 1)
	binary.LittleEndian.PutUint16(fmtb[2:4], channels)
	binary.LittleEndian.PutUint32(fmtb[4:8], rate)
	binary.LittleEndian.PutUint32(fmtb[8:12], byteRate)
	binary.LittleEndian.PutUint16(fmtb[12:14], block)
	binary.LittleEndian.PutUint16(fmtb[14:16], bits)
	return Build(fmtb, make([]byte, int(float64(byteRate)*seconds)))
}
