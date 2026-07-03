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
as upload, so --convert, --normalize and --skip/--skip-end apply. Requires
yt-dlp on PATH.

Playlists often stamp the same intro (and sometimes a closing jingle) on every
video. --trim-intro/--trim-outro detect and cut those with pure signal
analysis: "common" matches the audio all items share at that end and is solid
evidence; "spectral" finds where a single file's spectrum changes character
and is a heuristic. "auto" uses common for playlists, but on a single video it
only reports what it found — cutting on the spectral heuristic requires opting
in explicitly. --auto-trim enables both ends at once. Detection downloads the
whole batch before uploading; per-item cuts are added on top of
--skip/--skip-end.

YouTube radio URLs such as watch?v=...&list=RD...&start_radio=1 can expand to
large auto-generated playlists. By default tonys asks whether to import just the
current video (using a cleaned URL) or the whole radio playlist; pass
--allow-radio to skip the prompt and import the radio playlist directly.

Examples:
  tonys yt "Erna-Tonie" "https://youtu.be/XXXX"
  tonys yt "Erna-Tonie" "https://youtube.com/playlist?list=YYYY" --normalize target
  tonys yt "Erna-Tonie" URL --limit 5 --title-prefix "Mix: " --wait
  tonys yt "Erna-Tonie" URL --skip 30s --skip-end 30s
  tonys yt "Erna-Tonie" PLAYLIST_URL --trim-intro auto
  tonys yt "Erna-Tonie" PLAYLIST_URL --auto-trim
  tonys yt "Erna-Tonie" RADIO_URL --allow-radio`,
		Flags: append(append([]FlagSpec{
			{Name: "household", Usage: "limit lookup to a household (id or name)"},
			{Name: "limit", Usage: "max items to import (0 = all)", Default: "0"},
			{Name: "title-prefix", Usage: "prefix added to each chapter title"},
			{Name: "position", Usage: "where to place imported chapters: end|beginning", Default: "end"},
			{Name: "reverse", Usage: "import items in reverse order", Bool: true},
			{Name: "wait", Usage: "wait for transcoding after importing", Bool: true},
			{Name: "wait-timeout", Usage: "max time to wait for transcoding (with --wait)", Default: "10m"},
			{Name: "continue-on-error", Usage: "keep going if an item fails", Default: "true", Bool: true},
			{Name: "allow-radio", Usage: "suppress the YouTube radio playlist warning", Bool: true},
		}, trimFlags...), processFlags...),
		Run: ytRun,
	}
}

type ytAdded struct {
	Title               string  `json:"title"`
	FileID              string  `json:"fileId"`
	Source              string  `json:"source"`
	IntroTrimmedSeconds float64 `json:"introTrimmedSeconds,omitempty"`
	OutroTrimmedSeconds float64 `json:"outroTrimmedSeconds,omitempty"`
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
	trim, err := trimSpecFromFlags(fs)
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

	if len(items) < 2 && (trim.intro.mode == "common" || trim.outro.mode == "common") {
		return usageErr("common trim mode compares playlist items against each other and needs at least two; use auto or spectral for a single video")
	}

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
	var report *trimReport

	if trim.enabled() {
		added, failed, prs, report, err = a.ytImportTrimmed(ctx, t, yt, items, tmpDir, prefix, keepGoing, spec, trim)
		if err != nil {
			return err
		}
	} else {
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
	if report != nil {
		summary["trim"] = report
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
	return a.processAndUpload(ctx, t, path, title, spec)
}

// processAndUpload runs a downloaded file through the processing pipeline and
// uploads the result.
func (a *App) processAndUpload(ctx context.Context, t toniecloud.CreativeTonie, path, title string, spec processSpec) (toniecloud.UploadResult, *audio.ProcessResult, error) {
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

// trimReport summarizes intro/outro auto-trimming for the JSON output. The
// Suggested fields carry boundaries that were detected but NOT cut (suggest
// mode: auto on a single video), so automation sees them even though the
// human hint only goes to stderr.
type trimReport struct {
	IntroMode             string  `json:"introMode,omitempty"`
	IntroSeconds          float64 `json:"introSeconds,omitempty"`
	IntroSuggestedSeconds float64 `json:"introSuggestedSeconds,omitempty"`
	OutroMode             string  `json:"outroMode,omitempty"`
	OutroSeconds          float64 `json:"outroSeconds,omitempty"`
	OutroSuggestedSeconds float64 `json:"outroSuggestedSeconds,omitempty"`
}

// ytImportTrimmed imports items in three phases — download everything, detect
// intro/outro cuts across the batch, then process + upload each item with its
// cuts folded into --skip/--skip-end. Cross-file detection is why the whole
// batch must be on disk before the first upload.
func (a *App) ytImportTrimmed(ctx context.Context, t toniecloud.CreativeTonie, yt *ytdl.Client, items []ytdl.Item, tmpDir, prefix string, keepGoing bool, spec processSpec, trim trimSpec) (added []ytAdded, failed []ytFailed, prs []*audio.ProcessResult, report *trimReport, err error) {
	conv := audio.NewConverter()
	if !conv.Available() {
		return nil, nil, nil, nil, fmt.Errorf("--trim-intro/--trim-outro require ffmpeg; install it (e.g. `brew install ffmpeg`) or set $TONYS_FFMPEG")
	}
	introMode := resolveTrimMode(trim.intro.mode, len(items))
	outroMode := resolveTrimMode(trim.outro.mode, len(items))
	report = &trimReport{IntroMode: introMode, OutroMode: outroMode}

	type dlItem struct {
		item  ytdl.Item
		title string
		path  string
	}
	var dls []dlItem
	for i, item := range items {
		a.info("(%d/%d) downloading %s", i+1, len(items), item.Title)
		path, derr := yt.DownloadAudio(ctx, item, tmpDir)
		if derr != nil {
			a.info("  ! failed: %v", derr)
			failed = append(failed, ytFailed{Title: item.Title, URL: item.URL, Error: derr.Error()})
			if !keepGoing {
				return added, failed, prs, report, derr
			}
			continue
		}
		dls = append(dls, dlItem{item: item, title: prefix + item.Title, path: path})
	}

	introCuts := make([]float64, len(dls))
	outroCuts := make([]float64, len(dls))
	paths := make([]string, len(dls))
	for i, d := range dls {
		paths[i] = d.path
	}
	if introMode != "off" && len(dls) > 0 {
		var detected float64
		introCuts, detected = a.detectTrimCuts(ctx, conv, paths, trim.intro, introMode, "intro")
		if introMode == "suggest" {
			report.IntroSuggestedSeconds = round2(detected)
		} else {
			report.IntroSeconds = round2(detected)
		}
	}
	if outroMode != "off" && len(dls) > 0 {
		var detected float64
		outroCuts, detected = a.detectTrimCuts(ctx, conv, paths, trim.outro, outroMode, "outro")
		if outroMode == "suggest" {
			report.OutroSuggestedSeconds = round2(detected)
		} else {
			report.OutroSeconds = round2(detected)
		}
	}

	for i, d := range dls {
		a.info("(%d/%d) %s", i+1, len(dls), d.item.Title)
		switch {
		case introCuts[i] > 0 && outroCuts[i] > 0:
			a.info("  trimming %.1fs intro + %.1fs outro", introCuts[i], outroCuts[i])
		case introCuts[i] > 0:
			a.info("  trimming %.1fs intro", introCuts[i])
		case outroCuts[i] > 0:
			a.info("  trimming %.1fs outro", outroCuts[i])
		}
		specI := spec
		specI.skipSeconds += introCuts[i]
		specI.skipEndSeconds += outroCuts[i]

		res, pr, uerr := a.processAndUpload(ctx, t, d.path, d.title, specI)
		os.Remove(d.path)
		if uerr != nil {
			a.info("  ! failed: %v", uerr)
			failed = append(failed, ytFailed{Title: d.item.Title, URL: d.item.URL, Error: uerr.Error()})
			if !keepGoing {
				return added, failed, prs, report, uerr
			}
			continue
		}
		added = append(added, ytAdded{
			Title:               d.title,
			FileID:              res.FileID,
			Source:              d.item.URL,
			IntroTrimmedSeconds: round2(introCuts[i]),
			OutroTrimmedSeconds: round2(outroCuts[i]),
		})
		prs = append(prs, pr)
	}
	return added, failed, prs, report, nil
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
