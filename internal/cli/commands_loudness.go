package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
)

func loudnessCommand() *Command {
	return &Command{
		Name:    "loudness",
		Summary: "Measure audio loudness and inspect the per-tonie loudness ledger",
		Long: `Loudness tools backing --normalize match. The CLI records the loudness of
files it uploads (and of any cloud chapters it can download and measure) so a
new upload can be matched to what is already on the tonie.`,
		Sub: []*Command{
			{
				Name:    "measure",
				Summary: "Measure the integrated loudness (LUFS) of a local file",
				Args:    "<file>",
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 1 {
						return usageErr("loudness measure requires a file")
					}
					conv := audio.NewConverter()
					if !conv.Available() {
						return fmt.Errorf("ffmpeg not found; install it (e.g. `brew install ffmpeg`)")
					}
					stats, err := conv.Measure(ctx, args[0])
					if err != nil {
						return err
					}
					return a.emit(stats, func(w io.Writer) {
						tw := table(w)
						fmt.Fprintf(tw, "INTEGRATED_LUFS\t%.1f\n", stats.IntegratedLUFS)
						fmt.Fprintf(tw, "TRUE_PEAK_DB\t%.1f\n", stats.TruePeakDB)
						fmt.Fprintf(tw, "LRA\t%.1f\n", stats.LRA)
						tw.Flush()
					})
				},
			},
			{
				Name:    "show",
				Summary: "Show recorded loudness for a tonie's chapters",
				Args:    "<tonie>",
				Flags:   []FlagSpec{{Name: "household", Usage: "limit lookup to a household (id or name)"}},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					t, err := requireTonie(ctx, a, fs, args, "loudness show")
					if err != nil {
						return err
					}
					records := a.loudness().forTonie(t.ID)
					return a.emit(records, func(w io.Writer) {
						if len(records) == 0 {
							a.info("No recorded loudness for %q yet", t.Name)
							return
						}
						tw := table(w)
						fmt.Fprintf(tw, "LUFS\tSOURCE\tTITLE\tCHAPTER_ID\n")
						for _, r := range records {
							fmt.Fprintf(tw, "%.1f\t%s\t%s\t%s\n", r.IntegratedLUFS, r.Source, r.Title, r.ChapterID)
						}
						tw.Flush()
					})
				},
			},
			{
				Name:    "measure-tonie",
				Summary: "Download (if possible) and measure a tonie's existing chapters",
				Args:    "<tonie>",
				Flags:   []FlagSpec{{Name: "household", Usage: "limit lookup to a household (id or name)"}},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					t, err := requireTonie(ctx, a, fs, args, "loudness measure-tonie")
					if err != nil {
						return err
					}
					type row struct {
						Title          string  `json:"title"`
						ChapterID      string  `json:"chapterId"`
						IntegratedLUFS float64 `json:"integratedLufs"`
						Measured       bool    `json:"measured"`
						Note           string  `json:"note,omitempty"`
					}
					var rows []row
					for _, ch := range t.Chapters {
						r := row{Title: ch.Title, ChapterID: ch.ID}
						if rec, ok := a.loudness().get(ch.ID); ok {
							r.IntegratedLUFS = rec.IntegratedLUFS
							r.Measured = true
							r.Note = "cached"
						} else if lufs, ok := a.measureCloudChapter(ctx, t, ch); ok {
							a.loudness().put(loudnessRecord{ChapterID: ch.ID, TonieID: t.ID, Title: ch.Title, IntegratedLUFS: lufs, Source: "cloud"})
							r.IntegratedLUFS = lufs
							r.Measured = true
						} else {
							r.Note = "not downloadable"
						}
						rows = append(rows, r)
					}
					return a.emit(rows, func(w io.Writer) {
						tw := table(w)
						fmt.Fprintf(tw, "LUFS\tMEASURED\tTITLE\tNOTE\n")
						for _, r := range rows {
							fmt.Fprintf(tw, "%.1f\t%v\t%s\t%s\n", r.IntegratedLUFS, r.Measured, r.Title, r.Note)
						}
						tw.Flush()
					})
				},
			},
		},
	}
}
