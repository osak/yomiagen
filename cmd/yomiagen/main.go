package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/osak/yomiagen/internal/config"
	"github.com/osak/yomiagen/internal/cost"
	"github.com/osak/yomiagen/internal/ingest"
	"github.com/osak/yomiagen/internal/job"
	"github.com/osak/yomiagen/internal/pipeline"
	"github.com/osak/yomiagen/internal/pronounce"
	scriptstage "github.com/osak/yomiagen/internal/script"
	"github.com/osak/yomiagen/internal/tts"
	"github.com/osak/yomiagen/internal/tts/voicevox"
)

func main() {
	if err := run(context.Background(), os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
func run(ctx context.Context, args []string) error {
	var filtered []string
	verbose := false
	for _, arg := range args {
		if arg == "-v" {
			verbose = true
			continue
		}
		filtered = append(filtered, arg)
	}
	args = filtered
	if verbose {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	if len(args) == 0 {
		return usage(nil)
	}
	cfg, home, err := config.Load()
	if err != nil {
		return err
	}
	cmd := args[0]
	args = args[1:]
	switch cmd {
	case "run":
		return runAll(ctx, cfg, home, args)
	case "ingest":
		fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
		preset := fs.String("preset", "", "preset name")
		pos, err := parse(fs, args)
		if err != nil || len(pos) != 1 {
			return usage(err)
		}
		p, err := cfg.Preset(*preset)
		if err != nil {
			return err
		}
		j, err := job.Create(home, pos[0], p.Name)
		if err != nil {
			return err
		}
		if err := ingest.Run(ctx, j, false); err != nil {
			return err
		}
		fmt.Println(j.Meta.ID)
		return nil
	case "script", "pronounce", "query", "synthesize", "assemble":
		return stageCommand(ctx, cfg, home, cmd, args)
	case "jobs":
		items, err := job.List(home)
		if err != nil {
			return err
		}
		for _, j := range items {
			var stages []string
			for s := range j.Stages {
				stages = append(stages, s)
			}
			sort.Strings(stages)
			fmt.Printf("%s  %-20s  %s\n", j.ID, strings.Join(stages, ","), j.Source)
		}
		return nil
	case "path":
		return pathCommand(home, args)
	case "show":
		return showCommand(home, args)
	case "cost":
		return costCommand(home, args)
	case "voices", "caps", "say":
		return utilityCommand(ctx, cfg, cmd, args)
	case "help", "-h", "--help":
		return usage(nil)
	default:
		return fmt.Errorf("unknown command %q", cmd)
	}
}

func runAll(ctx context.Context, cfg config.Config, home string, args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	presetName := fs.String("preset", "", "preset name")
	from := fs.String("from", "", "first stage")
	to := fs.String("to", "assemble", "last stage")
	force := fs.Bool("force", false, "rerun stages")
	output := fs.String("o", "", "output file")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return usage(err)
	}
	j, openErr := job.Open(home, pos[0])
	var preset config.Preset
	if openErr == nil {
		preset, err = cfg.Preset(j.Meta.Preset)
	} else {
		preset, err = cfg.Preset(*presetName)
		if err == nil {
			j, err = job.Create(home, pos[0], preset.Name)
		}
	}
	if err != nil {
		return err
	}
	p, err := pipeline.New(cfg, preset, j)
	if err != nil {
		return err
	}
	stages := []string{"ingest", "script", "pronounce", "query", "synthesize", "assemble"}
	start := 0
	end := len(stages) - 1
	if *from != "" {
		start = index(stages, *from)
		if start < 0 {
			return fmt.Errorf("unknown --from stage %q", *from)
		}
	}
	if *to != "" {
		end = index(stages, *to)
		if end < 0 {
			return fmt.Errorf("unknown --to stage %q", *to)
		}
	}
	if start > end {
		return errors.New("--from must not come after --to")
	}
	for i := start; i <= end; i++ {
		slog.Info("running stage", "stage", stages[i], "job", j.Meta.ID)
		switch stages[i] {
		case "ingest":
			err = ingest.Run(ctx, j, *force)
		case "script":
			err = scriptstage.Run(ctx, j, cfg, *force)
		case "pronounce":
			err = pronounce.Run(ctx, j, cfg, preset, *force)
			if err == nil {
				err = p.RegisterDictionary(ctx)
			}
		case "query":
			err = p.Query(ctx, *force)
		case "synthesize":
			err = p.Synthesize(ctx, *force, nil)
		case "assemble":
			var out string
			out, err = p.Assemble(*force, *output)
			if err == nil {
				fmt.Println(out)
			}
		}
		if err != nil {
			return fmt.Errorf("%s: %w", stages[i], err)
		}
	}
	if end < len(stages)-1 {
		fmt.Println(j.Meta.ID)
	}
	return nil
}

func stageCommand(ctx context.Context, cfg config.Config, home, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	force := fs.Bool("force", false, "rerun stage")
	only := fs.String("only", "", "comma-separated chunk IDs")
	output := fs.String("o", "", "output file")
	pos, err := parse(fs, args)
	if err != nil || len(pos) != 1 {
		return usage(err)
	}
	j, err := job.Open(home, pos[0])
	if err != nil {
		return err
	}
	preset, err := cfg.Preset(j.Meta.Preset)
	if err != nil {
		return err
	}
	p, err := pipeline.New(cfg, preset, j)
	if err != nil {
		return err
	}
	switch cmd {
	case "script":
		err = scriptstage.Run(ctx, j, cfg, *force)
	case "pronounce":
		err = pronounce.Run(ctx, j, cfg, preset, *force)
		if err == nil {
			err = p.RegisterDictionary(ctx)
		}
	case "query":
		err = p.Query(ctx, *force)
	case "synthesize":
		err = p.Synthesize(ctx, *force, pipeline.Only(*only))
	case "assemble":
		var out string
		out, err = p.Assemble(*force, *output)
		if err == nil {
			fmt.Println(out)
		}
	}
	return err
}

func pathCommand(home string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usage(nil)
	}
	j, err := job.Open(home, args[0])
	if err != nil {
		return err
	}
	stage := "job"
	if len(args) == 2 {
		stage = args[1]
	}
	source := firstExisting(j.Dir, "00_source.html", "00_source.md", "00_source.txt")
	output := firstExisting(j.Dir, "50_output.m4a", "50_output.mp3", "50_output.wav")
	paths := map[string]string{"job": "job.json", "source": source, "document": "10_document.json", "script": "20_script.json", "speech": "30_speech.json", "query": "35_query", "chunks": "40_chunks", "output": output, "chapters": "50_chapters.txt"}
	p, ok := paths[stage]
	if !ok {
		return fmt.Errorf("unknown stage %q", stage)
	}
	fmt.Println(j.Path(p))
	return nil
}

func firstExisting(dir string, names ...string) string {
	for _, name := range names {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return names[0]
}
func showCommand(home string, args []string) error {
	if len(args) < 1 || len(args) > 2 {
		return usage(nil)
	}
	j, err := job.Open(home, args[0])
	if err != nil {
		return err
	}
	stage := "job"
	if len(args) == 2 {
		stage = args[1]
	}
	paths := map[string]string{"job": "job.json", "document": "10_document.json", "script": "20_script.json", "speech": "30_speech.json"}
	p, ok := paths[stage]
	if !ok {
		return fmt.Errorf("show supports job, document, script, or speech")
	}
	b, err := os.ReadFile(j.Path(p))
	if err == nil {
		_, err = os.Stdout.Write(b)
	}
	return err
}
func costCommand(home string, args []string) error {
	var path string
	if len(args) == 0 {
		items, err := job.List(home)
		if err != nil {
			return err
		}
		var total float64
		for _, m := range items {
			rs, _ := cost.Read(filepath.Join(home, "jobs", m.ID, "cost.jsonl"))
			for _, r := range rs {
				total += r.CostUSD
			}
			fmt.Printf("%s  $%.6f  %d records\n", m.ID, sum(rs), len(rs))
		}
		fmt.Printf("total  $%.6f\n", total)
		return nil
	}
	j, err := job.Open(home, args[0])
	if err != nil {
		return err
	}
	path = j.Path("cost.jsonl")
	rs, err := cost.Read(path)
	if err != nil {
		return err
	}
	for _, r := range rs {
		fmt.Printf("%s  %-10s %-10s %6d chars %8.2fs %6dms $%.6f\n", r.Time.Format("2006-01-02 15:04"), r.Stage, r.Provider, r.Characters, r.AudioSeconds, r.WallMS, r.CostUSD)
	}
	fmt.Printf("total $%.6f\n", sum(rs))
	return nil
}
func sum(rs []cost.Record) float64 {
	var n float64
	for _, r := range rs {
		n += r.CostUSD
	}
	return n
}

func utilityCommand(ctx context.Context, cfg config.Config, cmd string, args []string) error {
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	presetName := fs.String("preset", "", "preset name")
	output := fs.String("o", "say.wav", "output file")
	pos, err := parse(fs, args)
	if err != nil {
		return err
	}
	preset, err := cfg.Preset(*presetName)
	if err != nil {
		return err
	}
	v := voicevox.New(preset.Endpoint)
	switch cmd {
	case "voices":
		voices, err := v.Voices(ctx)
		if err != nil {
			return err
		}
		for _, x := range voices {
			fmt.Printf("%s\t%s\n", x.ID, x.Name)
		}
	case "caps":
		caps, err := v.Capabilities(ctx)
		if err != nil {
			return err
		}
		for _, x := range pipeline.SortedCaps(caps) {
			fmt.Println(x)
		}
	case "say":
		if len(pos) != 1 {
			return usage(nil)
		}
		r, err := v.Synthesize(ctx, tts.Request{Text: pos[0], VoiceID: preset.VoiceID})
		if err != nil {
			return err
		}
		if err := os.WriteFile(*output, r.WAV, 0o644); err != nil {
			return err
		}
		fmt.Println(*output)
	}
	return nil
}

func parse(fs *flag.FlagSet, args []string) ([]string, error) {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			name := strings.TrimLeft(strings.SplitN(a, "=", 2)[0], "-")
			f := fs.Lookup(name)
			if f == nil {
				return nil, fmt.Errorf("unknown flag %s", a)
			}
			if !strings.Contains(a, "=") {
				if _, ok := f.Value.(interface{ IsBoolFlag() bool }); !ok {
					if i+1 >= len(args) {
						return nil, fmt.Errorf("flag %s needs a value", a)
					}
					i++
					flags = append(flags, args[i])
				}
			}
		} else {
			pos = append(pos, a)
		}
	}
	return pos, fs.Parse(flags)
}
func index(xs []string, s string) int {
	for i, x := range xs {
		if x == s {
			return i
		}
	}
	return -1
}
func usage(cause error) error {
	if cause != nil {
		return cause
	}
	fmt.Print(`Usage:
  yomiagen [-v] <command> ...
  yomiagen run <input|job-id> [--preset NAME] [--from STAGE] [--to STAGE] [--force] [-o FILE]
  yomiagen ingest <input> | script|pronounce|query|synthesize|assemble <job-id>
  yomiagen jobs | show <job-id> [stage] | path <job-id> [stage] | cost [job-id]
  yomiagen voices | caps | say "text" [-o say.wav]
`)
	return nil
}
