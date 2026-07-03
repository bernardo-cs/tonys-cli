package cli

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"io"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/audio"
	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
	"github.com/bernardo-cs/tonys-cli/internal/ytdl"
)

func ytCommand() *Command {
	return &Command{
		Name:    "yt",
		Summary: "Import audio from a YouTube video or playlist (via yt-dlp)",
		Args:    "<tonie> <url>",
		Long: `Download the audio of a video — or every item of a playlist — and add each as
a chapter on the tonie. Items flow through the same convert/normalize pipeline
as upload, so --convert, --normalize and --skip apply. Requires yt-dlp on PATH.

YouTube radio URLs such as watch?v=...&list=RD...&start_radio=1 can expand to
large auto-generated playlists. By default tonys asks whether to import just the
current video (using a cleaned URL) or the whole radio playlist; pass
--allow-radio to skip the prompt and import the radio playlist directly.

Examples:
  tonys yt "Erna-Tonie" "https://youtu.be/XXXX"
  tonys yt "Erna-Tonie" "https://youtube.com/playlist?list=YYYY" --normalize target
  tonys yt "Erna-Tonie" URL --limit 5 --title-prefix "Mix: " --wait
  tonys yt "Erna-Tonie" URL --skip 30s --skip-end 30s
  tonys yt "Erna-Tonie" RADIO_URL --allow-radio`,
		Flags: append([]FlagSpec{
			{Name: "household", Usage: "limit lookup to a household (id or name)"},
			{Name: "limit", Usage: "max items to import (0 = all)", Default: "0"},
			{Name: "title-prefix", Usage: "prefix added to each chapter title"},
			{Name: "position", Usage: "where to place imported chapters: end|beginning", Default: "end"},
			{Name: "reverse", Usage: "import items in reverse order", Bool: true},
			{Name: "wait", Usage: "wait for transcoding after importing", Bool: true},
			{Name: "wait-timeout", Usage: "max time to wait for transcoding (with --wait)", Default: "10m"},
			{Name: "continue-on-error", Usage: "keep going if an item fails", Default: "true", Bool: true},
			{Name: "allow-radio", Usage: "suppress the YouTube radio playlist warning", Bool: true},
		}, processFlags...),
		Run: ytRun,
	}
}

type ytAdded struct {
	Title  string `json:"title"`
	FileID string `json:"fileId"`
	Source string `json:"source"`
}

type ytFailed struct {
	Title string `json:"title"`
	URL   string `json:"url"`
	Error string `json:"error"`
}

func ytRun(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
	if len(args) < 2 {
		return usageErr("yt requires <tonie> and <url>")
	}
	tonieRef, rawURL := args[0], args[1]
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

	yt := ytdl.New()
	if !yt.Available() {
		return fmt.Errorf("yt-dlp not found: install it (e.g. `brew install yt-dlp`) or set $TONYS_YTDLP")
	}

	promptedRadio := false
	if radio := parseYouTubeRadioURL(rawURL); radio.isRadio && !fbool(fs, "allow-radio") {
		promptedRadio = true
		choice, err := a.askRadioChoice(radio)
		if err != nil {
			return err
		}
		if choice == radioChoiceOne {
			rawURL = radio.cleanURL
			a.info("Using single-video URL: %s", rawURL)
		}
	}

	a.info("Resolving %s…", rawURL)
	items, err := yt.List(ctx, rawURL)
	if err != nil {
		return err
	}
	if isYouTubeRadioURL(rawURL) && len(items) > 1 && !fbool(fs, "allow-radio") && !promptedRadio {
		a.info("warning: URL looks like a YouTube radio playlist and expanded to %d items", len(items))
		a.info("warning: use a clean video URL, --limit 1, or pass --allow-radio to hide this warning")
	}
	if fbool(fs, "reverse") {
		for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
			items[i], items[j] = items[j], items[i]
		}
	}
	lim := atoiDefault(fstr(fs, "limit"), 0)
	if lim < 0 {
		return usageErr("--limit must be >= 0")
	}
	if lim > 0 && lim < len(items) {
		items = items[:lim]
	}
	a.info("Found %d item(s) to import", len(items))

	tmpDir, err := os.MkdirTemp("", "tonys-yt-*")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmpDir)

	prefix := fstr(fs, "title-prefix")
	keepGoing := fbool(fs, "continue-on-error")
	var added []ytAdded
	var failed []ytFailed
	var prs []*audio.ProcessResult

	for i, item := range items {
		title := prefix + item.Title
		a.info("(%d/%d) %s", i+1, len(items), item.Title)

		res, pr, ierr := a.importOne(ctx, t, yt, item, tmpDir, title, spec)
		if ierr != nil {
			a.info("  ! failed: %v", ierr)
			failed = append(failed, ytFailed{Title: item.Title, URL: item.URL, Error: ierr.Error()})
			if keepGoing {
				continue
			}
			return ierr
		}
		added = append(added, ytAdded{Title: title, FileID: res.FileID, Source: item.URL})
		prs = append(prs, pr)
	}

	updated := t
	if fbool(fs, "wait") && len(added) > 0 {
		a.info("Waiting for transcoding…")
		u, werr := a.Client().WaitForTranscoding(ctx, t.HouseholdID, t.ID, 3*time.Second, durFlag(fs, "wait-timeout", 10*time.Minute))
		if werr != nil {
			a.info("warning: transcoding wait did not complete: %v", werr)
		}
		// WaitForTranscoding returns the last-polled tonie (which includes the
		// newly-added chapters) even on a timeout/transcoding error, so use it
		// whenever it is populated; only fall back to a fresh read if it isn't.
		if u.ID != "" {
			updated = u
		} else if u2, gerr := a.Client().CreativeTonie(ctx, t.HouseholdID, t.ID); gerr == nil {
			updated = u2
		}
	} else if u, gerr := a.Client().CreativeTonie(ctx, t.HouseholdID, t.ID); gerr == nil {
		updated = u
	}
	if len(added) > 0 {
		if u, perr := a.placeNewChapters(ctx, t, updated, position); perr != nil {
			return perr
		} else {
			updated = u
		}
	}
	// Record loudness of the newly-added chapters (in import order) for `match`.
	a.recordChapterLoudness(t, t.Chapters, updated, prs)

	summary := map[string]any{
		"tonie":      updated,
		"added":      added,
		"failed":     failed,
		"addedCount": len(added),
	}
	return a.emit(summary, func(w io.Writer) {
		fmt.Fprintf(w, "Imported %d/%d item(s) into %q\n", len(added), len(items), t.Name)
		for _, f := range failed {
			fmt.Fprintf(w, "  FAILED: %s (%s)\n", f.Title, f.Error)
		}
		fmt.Fprintln(w)
		renderTonieDetail(w, updated)
	})
}

// importOne downloads, processes and uploads a single item.
func (a *App) importOne(ctx context.Context, t toniecloud.CreativeTonie, yt *ytdl.Client, item ytdl.Item, tmpDir, title string, spec processSpec) (toniecloud.UploadResult, *audio.ProcessResult, error) {
	path, derr := yt.DownloadAudio(ctx, item, tmpDir)
	if derr != nil {
		return toniecloud.UploadResult{}, nil, derr
	}
	defer os.Remove(path)

	ppath, cleanup, pr, perr := a.prepareForUpload(ctx, t, path, spec)
	if perr != nil {
		return toniecloud.UploadResult{}, nil, perr
	}
	defer cleanup()

	res, uerr := a.Client().UploadFile(ctx, t, ppath, title)
	if uerr != nil {
		return toniecloud.UploadResult{}, nil, uerr
	}
	return res, pr, nil
}

func atoiDefault(s string, def int) int {
	if v, err := strconv.Atoi(s); err == nil {
		return v
	}
	return def
}

func isYouTubeRadioURL(raw string) bool {
	return parseYouTubeRadioURL(raw).isRadio
}

type youtubeRadioURL struct {
	isRadio  bool
	cleanURL string
}

func parseYouTubeRadioURL(raw string) youtubeRadioURL {
	u, err := url.Parse(raw)
	if err != nil {
		return youtubeRadioURL{}
	}
	host := strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
	if host != "youtube.com" && host != "music.youtube.com" && host != "m.youtube.com" {
		return youtubeRadioURL{}
	}
	q := u.Query()
	isRadio := q.Get("start_radio") == "1" || strings.HasPrefix(strings.ToUpper(q.Get("list")), "RD")
	if !isRadio {
		return youtubeRadioURL{}
	}
	clean := ""
	if videoID := q.Get("v"); videoID != "" {
		clean = "https://www.youtube.com/watch?v=" + url.QueryEscape(videoID)
	}
	return youtubeRadioURL{isRadio: true, cleanURL: clean}
}

type radioChoice string

const (
	radioChoiceOne radioChoice = "one"
	radioChoiceAll radioChoice = "all"
)

func (a *App) askRadioChoice(radio youtubeRadioURL) (radioChoice, error) {
	if radio.cleanURL == "" {
		a.info("warning: URL looks like a YouTube radio playlist, but no current video id was found")
		a.info("warning: pass --allow-radio to hide this warning")
		return radioChoiceAll, nil
	}

	fmt.Fprintln(a.Stderr, "warning: URL looks like a YouTube radio playlist.")
	fmt.Fprintf(a.Stderr, "Import just the current video (%s) or the whole radio playlist?\n", radio.cleanURL)
	fmt.Fprint(a.Stderr, "Choose [o]ne/[a]ll: ")

	line, err := bufio.NewReader(a.Stdin).ReadString('\n')
	if err != nil && err != io.EOF {
		return "", err
	}
	switch strings.ToLower(strings.TrimSpace(line)) {
	case "", "o", "one", "1":
		return radioChoiceOne, nil
	case "a", "all":
		return radioChoiceAll, nil
	default:
		return "", usageErr("expected one of: one, all")
	}
}
