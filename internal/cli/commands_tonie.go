package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

func tonieCommand() *Command {
	return &Command{
		Name:    "tonie",
		Summary: "List, inspect and rename creative tonies",
		Sub: []*Command{
			{
				Name:    "list",
				Summary: "List creative tonies (optionally for one household)",
				Args:    "",
				Flags: []FlagSpec{
					{Name: "household", Usage: "limit to a household (id or name)"},
				},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					tonies, err := a.listTonies(ctx, fstr(fs, "household"))
					if err != nil {
						return err
					}
					return a.emit(tonies, func(w io.Writer) { renderTonieList(w, tonies) })
				},
			},
			{
				Name:    "get",
				Summary: "Show a single creative tonie and its chapters",
				Args:    "<tonie>",
				Flags: []FlagSpec{
					{Name: "household", Usage: "limit lookup to a household (id or name)"},
				},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 1 {
						return usageErr("tonie get requires a tonie id or name")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					return a.emit(t, func(w io.Writer) { renderTonieDetail(w, t) })
				},
			},
			{
				Name:    "rename",
				Summary: "Rename a creative tonie",
				Args:    "<tonie> <new-name>",
				Flags: []FlagSpec{
					{Name: "household", Usage: "limit lookup to a household (id or name)"},
				},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 2 {
						return usageErr("tonie rename requires <tonie> and <new-name>")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					updated, err := a.Client().RenameTonie(ctx, t, args[1])
					if err != nil {
						return err
					}
					a.info("Renamed %q → %q", t.Name, updated.Name)
					return a.emit(updated, func(w io.Writer) { renderTonieDetail(w, updated) })
				},
			},
		},
	}
}

// uploadFlags are shared by `upload` and `chapter add`. They include the
// convert/normalize processing flags.
var uploadFlags = append([]FlagSpec{
	{Name: "title", Usage: "chapter title (default: file name)"},
	{Name: "household", Usage: "limit lookup to a household (id or name)"},
	{Name: "position", Usage: "where to place the new chapter: end|beginning", Default: "end"},
	{Name: "wait", Usage: "wait until transcoding completes", Bool: true},
	{Name: "wait-timeout", Usage: "max time to wait for transcoding (with --wait)", Default: "5m"},
	{Name: "content-type", Usage: "MIME type for stdin uploads"},
}, processFlags...)

func uploadCommand() *Command {
	return &Command{
		Name:    "upload",
		Summary: "Upload an audio file as a new chapter (the main command)",
		Args:    "<tonie> <file>",
		Long: `Upload a local audio file (or - for stdin) to a creative tonie as a new
chapter. The tonie may be given by id or by name. With --wait the command blocks
until TonieCloud finishes transcoding.

Examples:
  tonys upload "Erna-Tonie" bedtime.mp3
  tonys upload 9B5AC304E0 story.mp3 --title "Bedtime story" --wait
  cat song.mp3 | tonys upload "Erna-Tonie" - --title "Song"
  tonys upload "Erna-Tonie" podcast.mp3 --skip 1m30s
  tonys upload "Erna-Tonie" podcast.mp3 --skip 1m30s --skip-end 30s`,
		Flags: uploadFlags,
		Run:   uploadRun,
	}
}

func uploadRun(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
	if len(args) < 2 {
		return usageErr("upload requires <tonie> and <file> (use - for stdin)")
	}
	tonieRef, file := args[0], args[1]
	t, err := a.resolveTonie(ctx, tonieRef, fstr(fs, "household"))
	if err != nil {
		return err
	}
	spec, err := processSpecFromFlags(fs)
	if err != nil {
		return err
	}
	position, err := chapterPositionFromFlags(fs)
	if err != nil {
		return err
	}

	title := fstr(fs, "title")
	var res toniecloud.UploadResult
	var pr *audio.ProcessResult

	switch {
	case file == "-" && !spec.forcesProcessing():
		if title == "" {
			title = "audio"
		}
		a.info("Uploading from stdin to %q…", t.Name)
		res, err = a.Client().UploadReader(ctx, t, a.Stdin, title, fstr(fs, "content-type"))
	case file == "-":
		if title == "" {
			title = "audio"
		}
		tmp, cleanup, berr := bufferToTemp(a.Stdin)
		if berr != nil {
			return berr
		}
		defer cleanup()
		path, cl2, p, perr := a.prepareForUpload(ctx, t, tmp, spec)
		if perr != nil {
			return perr
		}
		defer cl2()
		pr = p
		a.info("Uploading processed stdin to %q…", t.Name)
		res, err = a.Client().UploadFile(ctx, t, path, title)
	default:
		if title == "" {
			title = strings.TrimSuffix(filepath.Base(file), filepath.Ext(file))
		}
		path, cleanup, p, perr := a.prepareForUpload(ctx, t, file, spec)
		if perr != nil {
			return perr
		}
		defer cleanup()
		pr = p
		a.info("Uploading %s to %q…", file, t.Name)
		res, err = a.Client().UploadFile(ctx, t, path, title)
	}
	if err != nil {
		return err
	}
	a.info("Uploaded; added chapter %q (fileId %s)", res.Title, res.FileID)

	updated := t
	if fbool(fs, "wait") {
		a.info("Waiting for transcoding…")
		updated, err = a.Client().WaitForTranscoding(ctx, t.HouseholdID, t.ID, 2*time.Second, durFlag(fs, "wait-timeout", 5*time.Minute))
	} else {
		updated, err = a.Client().CreativeTonie(ctx, t.HouseholdID, t.ID)
	}
	if err != nil {
		// Upload succeeded; only the follow-up read failed. Report the result.
		a.info("note: could not re-read tonie: %v", err)
		return a.emit(res, nil)
	}
	updated, err = a.placeNewChapters(ctx, t, updated, position)
	if err != nil {
		return err
	}
	// Now that the new chapter id is known, record its loudness for `match`.
	a.recordChapterLoudness(t, t.Chapters, updated, []*audio.ProcessResult{pr})
	return a.emit(updated, func(w io.Writer) { renderTonieDetail(w, updated) })
}

// renderTonieList writes a tonie summary table.
func renderTonieList(w io.Writer, tonies []toniecloud.CreativeTonie) {
	tw := table(w)
	fmt.Fprintf(tw, "ID\tNAME\tCHAPTERS\tSECONDS_LEFT\tTRANSCODING\n")
	for _, t := range tonies {
		fmt.Fprintf(tw, "%s\t%s\t%d/%d\t%.0f\t%v\n",
			t.ID, t.Name, t.ChaptersPresent, t.ChaptersPresent+t.ChaptersRemaining,
			t.SecondsRemaining, t.Transcoding)
	}
	tw.Flush()
}

// renderTonieDetail writes a single tonie plus its chapters.
func renderTonieDetail(w io.Writer, t toniecloud.CreativeTonie) {
	tw := table(w)
	fmt.Fprintf(tw, "ID\t%s\n", t.ID)
	fmt.Fprintf(tw, "NAME\t%s\n", t.Name)
	fmt.Fprintf(tw, "HOUSEHOLD\t%s\n", t.HouseholdID)
	fmt.Fprintf(tw, "CHAPTERS\t%d (of %d)\n", t.ChaptersPresent, t.ChaptersPresent+t.ChaptersRemaining)
	fmt.Fprintf(tw, "SECONDS\t%.0f used, %.0f free\n", t.SecondsPresent, t.SecondsRemaining)
	fmt.Fprintf(tw, "TRANSCODING\t%v\n", t.Transcoding)
	if t.LastUpdate != nil {
		fmt.Fprintf(tw, "LAST_UPDATE\t%s\n", t.LastUpdate.Local().Format(time.RFC3339))
	}
	tw.Flush()
	if len(t.Chapters) > 0 {
		fmt.Fprintln(w)
		renderChapters(w, t.Chapters)
	}
}

// renderChapters writes a numbered chapter table.
func renderChapters(w io.Writer, chapters []toniecloud.Chapter) {
	tw := table(w)
	fmt.Fprintf(tw, "#\tTITLE\tSECONDS\tTRANSCODING\tID\n")
	for i, ch := range chapters {
		fmt.Fprintf(tw, "%d\t%s\t%.0f\t%v\t%s\n", i+1, ch.Title, ch.Seconds, ch.Transcoding, ch.ID)
	}
	tw.Flush()
}
