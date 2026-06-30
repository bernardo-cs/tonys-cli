package cli

import (
	"context"
	"flag"
	"fmt"
	"io"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

func chapterCommand() *Command {
	householdFlag := []FlagSpec{{Name: "household", Usage: "limit lookup to a household (id or name)"}}
	return &Command{
		Name:    "chapter",
		Summary: "List and edit the chapters on a tonie",
		Long:    "Chapters are matched by id, by 1-based number (e.g. 3), or by exact title.",
		Sub: []*Command{
			{
				Name:    "list",
				Summary: "List the chapters of a tonie",
				Args:    "<tonie>",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					t, err := requireTonie(ctx, a, fs, args, "chapter list")
					if err != nil {
						return err
					}
					return a.emit(t.Chapters, func(w io.Writer) {
						if len(t.Chapters) == 0 {
							a.info("%q has no chapters", t.Name)
							return
						}
						renderChapters(w, t.Chapters)
					})
				},
			},
			{
				Name:    "add",
				Summary: "Upload a file as a new chapter (alias of `upload`)",
				Args:    "<tonie> <file>",
				Flags:   uploadFlags,
				Run:     uploadRun,
			},
			{
				Name:    "add-existing",
				Summary: "Append an already-uploaded fileId as a chapter",
				Args:    "<tonie> <fileId>",
				Flags: append([]FlagSpec{
					{Name: "title", Usage: "chapter title"},
					{Name: "position", Usage: "where to place the new chapter: end|beginning", Default: "end"},
				}, householdFlag...),
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 2 {
						return usageErr("chapter add-existing requires <tonie> and <fileId>")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					position, err := chapterPositionFromFlags(fs)
					if err != nil {
						return err
					}
					title := fstr(fs, "title")
					if title == "" {
						title = args[1]
					}
					if err := a.Client().AddChapter(ctx, t, args[1], title); err != nil {
						return err
					}
					updated, err := a.Client().CreativeTonie(ctx, t.HouseholdID, t.ID)
					if err != nil {
						return a.emit(map[string]string{"fileId": args[1], "title": title}, nil)
					}
					updated, err = a.placeNewChapters(ctx, t, updated, position)
					if err != nil {
						return err
					}
					return a.emit(updated, func(w io.Writer) { renderTonieDetail(w, updated) })
				},
			},
			{
				Name:    "rm",
				Summary: "Remove one or more chapters",
				Args:    "<tonie> <chapter>...",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 2 {
						return usageErr("chapter rm requires <tonie> and at least one chapter")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					remove := map[int]bool{}
					for _, ref := range args[1:] {
						idx, err := resolveChapter(t, ref)
						if err != nil {
							return err
						}
						remove[idx] = true
					}
					var kept []toniecloud.Chapter
					for i, ch := range t.Chapters {
						if !remove[i] {
							kept = append(kept, ch)
						}
					}
					return a.patchChapters(ctx, t, kept, fmt.Sprintf("Removed %d chapter(s)", len(remove)))
				},
			},
			{
				Name:    "rename",
				Summary: "Rename a chapter",
				Args:    "<tonie> <chapter> <new-title>",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 3 {
						return usageErr("chapter rename requires <tonie> <chapter> <new-title>")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					idx, err := resolveChapter(t, args[1])
					if err != nil {
						return err
					}
					chapters := cloneChapters(t.Chapters)
					chapters[idx].Title = args[2]
					return a.patchChapters(ctx, t, chapters, fmt.Sprintf("Renamed chapter %d to %q", idx+1, args[2]))
				},
			},
			{
				Name:    "move",
				Summary: "Move a chapter to a 1-based position",
				Args:    "<tonie> <chapter> <position>",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 3 {
						return usageErr("chapter move requires <tonie> <chapter> <position>")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					from, err := resolveChapter(t, args[1])
					if err != nil {
						return err
					}
					var to int
					if _, err := fmt.Sscanf(args[2], "%d", &to); err != nil || to < 1 || to > len(t.Chapters) {
						return usageErr("position must be between 1 and %d", len(t.Chapters))
					}
					chapters := cloneChapters(t.Chapters)
					moved := chapters[from]
					chapters = append(chapters[:from], chapters[from+1:]...)
					dst := to - 1
					chapters = append(chapters[:dst], append([]toniecloud.Chapter{moved}, chapters[dst:]...)...)
					return a.patchChapters(ctx, t, chapters, fmt.Sprintf("Moved chapter to position %d", to))
				},
			},
			{
				Name:    "sort",
				Summary: "Reorder chapters to the given order (unlisted ones keep their order at the end)",
				Args:    "<tonie> <chapter>...",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if len(args) < 2 {
						return usageErr("chapter sort requires <tonie> and at least one chapter")
					}
					t, err := a.resolveTonie(ctx, args[0], fstr(fs, "household"))
					if err != nil {
						return err
					}
					used := map[int]bool{}
					var ordered []toniecloud.Chapter
					for _, ref := range args[1:] {
						idx, err := resolveChapter(t, ref)
						if err != nil {
							return err
						}
						if used[idx] {
							return usageErr("chapter %q listed more than once", ref)
						}
						used[idx] = true
						ordered = append(ordered, t.Chapters[idx])
					}
					for i, ch := range t.Chapters {
						if !used[i] {
							ordered = append(ordered, ch)
						}
					}
					return a.patchChapters(ctx, t, ordered, "Reordered chapters")
				},
			},
			{
				Name:    "clear",
				Summary: "Remove all chapters from a tonie",
				Args:    "<tonie>",
				Flags:   householdFlag,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					t, err := requireTonie(ctx, a, fs, args, "chapter clear")
					if err != nil {
						return err
					}
					return a.patchChapters(ctx, t, []toniecloud.Chapter{}, "Cleared all chapters")
				},
			},
		},
	}
}

// requireTonie resolves args[0] to a tonie, erroring if absent.
func requireTonie(ctx context.Context, a *App, fs *flag.FlagSet, args []string, cmd string) (toniecloud.CreativeTonie, error) {
	if len(args) < 1 {
		return toniecloud.CreativeTonie{}, usageErr("%s requires a tonie id or name", cmd)
	}
	return a.resolveTonie(ctx, args[0], fstr(fs, "household"))
}

// patchChapters writes a new chapter list and renders the updated tonie.
func (a *App) patchChapters(ctx context.Context, t toniecloud.CreativeTonie, chapters []toniecloud.Chapter, msg string) error {
	updated, err := a.Client().SetChapters(ctx, t, chapters)
	if err != nil {
		return err
	}
	a.info("%s on %q", msg, t.Name)
	return a.emit(updated, func(w io.Writer) { renderTonieDetail(w, updated) })
}

func cloneChapters(in []toniecloud.Chapter) []toniecloud.Chapter {
	out := make([]toniecloud.Chapter, len(in))
	copy(out, in)
	return out
}

type chapterPosition string

const (
	chapterPositionEnd       chapterPosition = "end"
	chapterPositionBeginning chapterPosition = "beginning"
)

func chapterPositionFromFlags(fs *flag.FlagSet) (chapterPosition, error) {
	switch fstr(fs, "position") {
	case "", "end", "last":
		return chapterPositionEnd, nil
	case "beginning", "start", "front", "first":
		return chapterPositionBeginning, nil
	default:
		return "", usageErr("--position must be end or beginning")
	}
}

func (a *App) placeNewChapters(ctx context.Context, before, after toniecloud.CreativeTonie, position chapterPosition) (toniecloud.CreativeTonie, error) {
	if position == chapterPositionEnd {
		return after, nil
	}

	chapters, changed := chaptersWithNewFirst(before.Chapters, after.Chapters)
	if !changed {
		return after, nil
	}
	updated, err := a.Client().SetChapters(ctx, after, chapters)
	if err != nil {
		return after, err
	}
	a.info("Moved new chapter(s) to the beginning of %q", after.Name)
	return updated, nil
}

func chaptersWithNewFirst(before, after []toniecloud.Chapter) ([]toniecloud.Chapter, bool) {
	seen := make(map[string]bool, len(before))
	for _, ch := range before {
		seen[ch.ID] = true
	}

	var newChapters []toniecloud.Chapter
	var existing []toniecloud.Chapter
	for _, ch := range after {
		if !seen[ch.ID] {
			newChapters = append(newChapters, ch)
			continue
		}
		existing = append(existing, ch)
	}
	if len(newChapters) == 0 || len(existing) == 0 {
		return after, false
	}

	reordered := append(append([]toniecloud.Chapter{}, newChapters...), existing...)
	if sameChapterOrder(after, reordered) {
		return after, false
	}
	return reordered, true
}

func sameChapterOrder(a, b []toniecloud.Chapter) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i].ID != b[i].ID {
			return false
		}
	}
	return true
}
