package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
)

// trimFlags are the intro/outro auto-trim flags of the youtube importer.
// Detection is pure signal analysis (spectral fingerprints, no AI): "common"
// finds the audio prefix/suffix shared across the batch, "spectral" finds the
// point where one file's spectrum changes character, "auto" picks common for
// playlists and spectral for single videos.
var trimFlags = []FlagSpec{
	{Name: "trim-intro", Usage: "cut a detected intro from each item: off|auto|common|spectral (auto only reports on single videos)", Default: "off"},
	{Name: "trim-outro", Usage: "cut a detected outro/closing from each item: off|auto|common|spectral (auto only reports on single videos)", Default: "off"},
	{Name: "auto-trim", Usage: "shorthand for --trim-intro auto --trim-outro auto", Bool: true},
	{Name: "intro-max", Usage: "longest intro to look for", Default: "1m"},
	{Name: "intro-min", Usage: "shortest intro to cut", Default: "1s"},
	{Name: "outro-max", Usage: "longest outro to look for", Default: "1m"},
	{Name: "outro-min", Usage: "shortest outro to cut", Default: "1s"},
}

// Alignment slack when matching a pair of files: how much their lead-ins
// (intro) or tail silences (outro) may differ. Endings vary more than starts.
const (
	introShiftSeconds = 2
	outroShiftSeconds = 5
)

// trimSide is the resolved configuration for one end of the audio.
type trimSide struct {
	mode string // off | auto | common | spectral
	opts audio.IntroOptions
}

// trimSpec holds both ends.
type trimSpec struct {
	intro, outro trimSide
}

func (t trimSpec) enabled() bool { return t.intro.mode != "off" || t.outro.mode != "off" }

// trimSpecFromFlags resolves the --trim-intro/--trim-outro/--auto-trim flags.
// --auto-trim switches either side to auto unless that side's flag was set
// explicitly (so `--auto-trim --trim-intro off` trims only the outro).
func trimSpecFromFlags(fs *flag.FlagSet) (trimSpec, error) {
	visited := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { visited[f.Name] = true })
	var spec trimSpec
	var err error
	if spec.intro, err = trimSideFromFlags(fs, visited, "intro", introShiftSeconds); err != nil {
		return spec, err
	}
	spec.outro, err = trimSideFromFlags(fs, visited, "outro", outroShiftSeconds)
	return spec, err
}

func trimSideFromFlags(fs *flag.FlagSet, visited map[string]bool, side string, shiftSeconds float64) (trimSide, error) {
	mode := orDefault(fstr(fs, "trim-"+side), "off")
	switch mode {
	case "off", "auto", "common", "spectral":
	default:
		return trimSide{}, usageErr("--trim-%s must be off, auto, common or spectral, got %q", side, mode)
	}
	if mode == "off" && !visited["trim-"+side] && fbool(fs, "auto-trim") {
		mode = "auto"
	}
	maxD, err := positiveDurFlag(fs, side+"-max", time.Minute)
	if err != nil {
		return trimSide{}, err
	}
	minD, err := positiveDurFlag(fs, side+"-min", time.Second)
	if err != nil {
		return trimSide{}, err
	}
	if minD >= maxD {
		return trimSide{}, usageErr("--%s-min (%s) must be shorter than --%s-max (%s)", side, minD, side, maxD)
	}
	return trimSide{
		mode: mode,
		opts: audio.IntroOptions{
			MinSeconds:      minD.Seconds(),
			MaxSeconds:      maxD.Seconds(),
			MaxShiftSeconds: shiftSeconds,
		},
	}, nil
}

// resolveTrimMode turns "auto" into a concrete mode. With a batch, cross-file
// matching gives real evidence and cuts. With a single item the only tool is
// the spectral heuristic, which real-world music fools too easily (song
// sections and fade-outs look like jingles), so auto downgrades to "suggest":
// detect and report, but only cut when the user explicitly opts into
// spectral mode.
func resolveTrimMode(mode string, items int) string {
	if mode != "auto" {
		return mode
	}
	if items >= 2 {
		return "common"
	}
	return "suggest"
}

// detectTrimCuts fingerprints paths and returns the per-file cut in seconds
// (from the start for intros, from the end for outros) plus the detected
// jingle length: the consensus for common mode, or the confident boundary in
// suggest mode (which reports without cutting). Files that cannot be analyzed
// simply get no cut; only having fewer than two analyzable files disables
// common mode.
func (a *App) detectTrimCuts(ctx context.Context, conv *audio.Converter, paths []string, cfg trimSide, mode, side string) ([]float64, float64) {
	cuts := make([]float64, len(paths))
	kind := "spectral"
	if mode == "common" {
		kind = "shared"
	}
	a.info("Analyzing %d file(s) for a %s %s…", len(paths), kind, side)

	fps := make([]audio.Fingerprint, len(paths))
	ok := make([]bool, len(paths))
	for i, p := range paths {
		fp, err := fingerprintSide(ctx, conv, p, side, cfg.opts)
		if err != nil {
			a.info("warning: %s analysis failed for item %d: %v", side, i+1, err)
			continue
		}
		if fp.Frames() == 0 {
			continue
		}
		fps[i], ok[i] = fp, true
	}

	switch mode {
	case "common":
		var sel []audio.Fingerprint
		var idx []int
		for i := range paths {
			if ok[i] {
				sel = append(sel, fps[i])
				idx = append(idx, i)
			}
		}
		if len(sel) < 2 {
			a.info("warning: fewer than two items could be analyzed; skipping %s trim", side)
			return cuts, 0
		}
		res := detectCommonSide(side, sel, cfg.opts)
		for k, item := range res.Items {
			cuts[idx[k]] = item.CutSeconds
		}
		if res.IntroSeconds > 0 {
			a.info("Found a shared %s of %.1fs", side, res.IntroSeconds)
		} else {
			a.info("No shared %s detected", side)
		}
		return cuts, res.IntroSeconds
	default: // spectral (cuts) or suggest (reports only)
		apply := mode == "spectral"
		found := 0
		var suggested float64
		for i := range paths {
			if !ok[i] {
				continue
			}
			r := detectSoloSide(side, fps[i], cfg.opts)
			if r.Confident {
				found++
				if apply {
					cuts[i] = r.CutSeconds
				} else {
					suggested = r.BoundarySeconds
					flag, unit := "--trim-intro spectral", "--skip"
					if side == "outro" {
						flag, unit = "--trim-outro spectral", "--skip-end"
					}
					a.info("Possible %s of %.1fs detected (score %.2f); re-run with %s or %s %.1fs to cut it", side, r.BoundarySeconds, r.Score, flag, unit, r.BoundarySeconds)
				}
			}
			if a.Verbose {
				a.info("  item %d %s boundary %.1fs (score %.2f, cohesion %.2f, confident=%v)", i+1, side, r.BoundarySeconds, r.Score, r.Cohesion, r.Confident)
			}
		}
		if apply {
			a.info("Spectral %s detection: confident boundary in %d/%d item(s)", side, found, len(paths))
		} else if found == 0 {
			a.info("No confident %s boundary detected", side)
		}
		return cuts, suggested
	}
}

// introCommand and outroCommand expose the same detection used by `yt` for
// local files, so cut points can be inspected (or fed to --skip/--skip-end)
// without touching the cloud.
func introCommand() *Command {
	return &Command{
		Name:    "intro",
		Summary: "Detect intros in local audio files (signal analysis, offline)",
		Long: `Find how many seconds of intro local files share — or, for a single file,
where its spectrum changes character — without uploading anything. The same
detection powers ` + "`tonys yt --trim-intro`" + `.

Examples:
  tonys intro detect a.mp3 b.mp3 c.mp3
  tonys intro detect --mode spectral song.mp3
  tonys intro detect *.webm --max 30s --json`,
		Sub: []*Command{trimDetectCommand("intro")},
	}
}

func outroCommand() *Command {
	return &Command{
		Name:    "outro",
		Summary: "Detect outros/closings in local audio files (signal analysis, offline)",
		Long: `Find how many seconds of shared closing jingle local files end with — or, for
a single file, where the spectrum changes character near the end. Cut points
are measured from the END of each file (feed them to --skip-end). The same
detection powers ` + "`tonys yt --trim-outro`" + `.

Examples:
  tonys outro detect a.mp3 b.mp3 c.mp3
  tonys outro detect --mode spectral song.mp3 --max 30s`,
		Sub: []*Command{trimDetectCommand("outro")},
	}
}

func trimDetectCommand(side string) *Command {
	edge := edgeOf(side)
	return &Command{
		Name:    "detect",
		Summary: fmt.Sprintf("Report the %s cut point (seconds from the %s) for each file", side, edge),
		Args:    "<file>...",
		Flags: []FlagSpec{
			{Name: "mode", Usage: "detection mode: auto|common|spectral", Default: "auto"},
			{Name: "max", Usage: fmt.Sprintf("longest %s to look for", side), Default: "1m"},
			{Name: "min", Usage: fmt.Sprintf("shortest %s to report", side), Default: "1s"},
		},
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			return runTrimDetect(ctx, a, fs, args, side)
		},
	}
}

func runTrimDetect(ctx context.Context, a *App, fs *flag.FlagSet, args []string, side string) error {
	if len(args) == 0 {
		return usageErr("%s detect requires at least one file", side)
	}
	mode := orDefault(fstr(fs, "mode"), "auto")
	switch mode {
	case "auto", "common", "spectral":
	default:
		return usageErr("--mode must be auto, common or spectral, got %q", mode)
	}
	mode = resolveTrimMode(mode, len(args))
	if mode == "suggest" {
		// The detect command only reports, so the report-only downgrade
		// that protects yt imports has no meaning here.
		mode = "spectral"
	}
	if mode == "common" && len(args) < 2 {
		return usageErr("common mode compares files against each other and needs at least two; use --mode spectral for a single file")
	}
	conv := audio.NewConverter()
	if !conv.Available() {
		return fmt.Errorf("intro/outro detection requires ffmpeg; install it (e.g. `brew install ffmpeg`) or set $TONYS_FFMPEG")
	}
	maxD, err := positiveDurFlag(fs, "max", time.Minute)
	if err != nil {
		return err
	}
	minD, err := positiveDurFlag(fs, "min", time.Second)
	if err != nil {
		return err
	}
	if minD >= maxD {
		return usageErr("--min (%s) must be shorter than --max (%s)", minD, maxD)
	}
	shift := float64(introShiftSeconds)
	if side == "outro" {
		shift = outroShiftSeconds
	}
	opts := audio.IntroOptions{MinSeconds: minD.Seconds(), MaxSeconds: maxD.Seconds(), MaxShiftSeconds: shift}

	fps := make([]audio.Fingerprint, len(args))
	for i, p := range args {
		var ferr error
		if fps[i], ferr = fingerprintSide(ctx, conv, p, side, opts); ferr != nil {
			return fmt.Errorf("%s: %w", p, ferr)
		}
		if fps[i].Frames() == 0 {
			return fmt.Errorf("%s: audio is too short to analyze (needs at least ~0.2s)", p)
		}
	}

	// Each mode gets its own fixed row shape (no omitempty): automation must
	// be able to rely on confident:false and pairs:0 being present.
	out := map[string]any{"side": side, "mode": mode}

	if mode == "common" {
		type row struct {
			File       string  `json:"file"`
			CutSeconds float64 `json:"cutSeconds"`
			Pairs      int     `json:"pairs"`
		}
		res := detectCommonSide(side, fps, opts)
		rows := make([]row, len(res.Items))
		for i, item := range res.Items {
			rows[i] = row{File: args[i], CutSeconds: round2(item.CutSeconds), Pairs: item.Pairs}
		}
		consensus := round2(res.IntroSeconds)
		out["consensusSeconds"] = consensus
		out["files"] = rows
		return a.emit(out, func(w io.Writer) {
			tw := table(w)
			fmt.Fprintf(tw, "CUT_SECONDS\tPAIRS\tFILE\n")
			for _, r := range rows {
				fmt.Fprintf(tw, "%.2f\t%d\t%s\n", r.CutSeconds, r.Pairs, r.File)
			}
			tw.Flush()
			if consensus > 0 {
				fmt.Fprintf(w, "\nShared %s: %.2fs (cut is measured from the %s of each file)\n", side, consensus, edgeOf(side))
			} else {
				fmt.Fprintf(w, "\nNo shared %s detected\n", side)
			}
		})
	}

	type row struct {
		File            string  `json:"file"`
		CutSeconds      float64 `json:"cutSeconds"`
		BoundarySeconds float64 `json:"boundarySeconds"`
		Score           float64 `json:"score"`
		Cohesion        float64 `json:"cohesion"`
		Confident       bool    `json:"confident"`
	}
	rows := make([]row, len(args))
	for i := range args {
		r := detectSoloSide(side, fps[i], opts)
		rows[i] = row{
			File:            args[i],
			CutSeconds:      round2(r.CutSeconds),
			BoundarySeconds: round2(r.BoundarySeconds),
			Score:           round2(r.Score),
			Cohesion:        round2(r.Cohesion),
			Confident:       r.Confident,
		}
	}
	out["files"] = rows
	return a.emit(out, func(w io.Writer) {
		tw := table(w)
		fmt.Fprintf(tw, "CUT_SECONDS\tBOUNDARY\tSCORE\tCOHESION\tCONFIDENT\tFILE\n")
		for _, r := range rows {
			fmt.Fprintf(tw, "%.2f\t%.2f\t%.2f\t%.2f\t%v\t%s\n", r.CutSeconds, r.BoundarySeconds, r.Score, r.Cohesion, r.Confident, r.File)
		}
		tw.Flush()
	})
}

// fingerprintSide fingerprints the end of the file the side cares about: the
// head for intros, the tail for outros.
func fingerprintSide(ctx context.Context, conv *audio.Converter, path, side string, opts audio.IntroOptions) (audio.Fingerprint, error) {
	if side == "outro" {
		return conv.FingerprintFileTail(ctx, path, opts.FingerprintSeconds())
	}
	return conv.FingerprintFile(ctx, path, opts.FingerprintSeconds())
}

func detectCommonSide(side string, fps []audio.Fingerprint, opts audio.IntroOptions) audio.CommonIntroResult {
	if side == "outro" {
		return audio.DetectCommonOutro(fps, opts)
	}
	return audio.DetectCommonIntro(fps, opts)
}

func detectSoloSide(side string, fp audio.Fingerprint, opts audio.IntroOptions) audio.SoloIntroResult {
	if side == "outro" {
		return audio.DetectSoloOutro(fp, opts)
	}
	return audio.DetectSoloIntro(fp, opts)
}

func edgeOf(side string) string {
	if side == "outro" {
		return "end"
	}
	return "start"
}

// round2 rounds to two decimals for stable, readable output.
func round2(v float64) float64 { return math.Round(v*100) / 100 }
