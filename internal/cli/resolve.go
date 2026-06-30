package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

// notFoundError signals that a referenced resource could not be found → exit 4.
type notFoundError struct{ msg string }

func (e *notFoundError) Error() string { return e.msg }

func notFoundErr(format string, args ...any) error {
	return &notFoundError{fmt.Sprintf(format, args...)}
}

// listTonies returns creative tonies, optionally restricted to one household
// (referenced by id or name).
func (a *App) listTonies(ctx context.Context, householdRef string) ([]toniecloud.CreativeTonie, error) {
	if householdRef == "" {
		return a.Client().CreativeTonies(ctx)
	}
	h, err := a.resolveHousehold(ctx, householdRef)
	if err != nil {
		return nil, err
	}
	return a.Client().CreativeToniesByHousehold(ctx, h.ID)
}

// resolveHousehold finds a household by exact id or case-insensitive name.
func (a *App) resolveHousehold(ctx context.Context, ref string) (toniecloud.Household, error) {
	if ref == "" {
		return toniecloud.Household{}, usageErr("a household (id or name) is required")
	}
	households, err := a.Client().Households(ctx)
	if err != nil {
		return toniecloud.Household{}, err
	}
	var byName []toniecloud.Household
	for _, h := range households {
		if h.ID == ref {
			return h, nil
		}
		if strings.EqualFold(h.Name, ref) {
			byName = append(byName, h)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return toniecloud.Household{}, notFoundErr("no household matches %q", ref)
	default:
		return toniecloud.Household{}, usageErr("%q is ambiguous: %d households share that name; use the id", ref, len(byName))
	}
}

// resolveTonie finds a single creative tonie by exact id or case-insensitive
// name, optionally scoped to a household. Names are matched after ids so an id
// always wins.
func (a *App) resolveTonie(ctx context.Context, ref, householdRef string) (toniecloud.CreativeTonie, error) {
	if ref == "" {
		return toniecloud.CreativeTonie{}, usageErr("a tonie (id or name) is required")
	}
	tonies, err := a.listTonies(ctx, householdRef)
	if err != nil {
		return toniecloud.CreativeTonie{}, err
	}
	var byName []toniecloud.CreativeTonie
	for _, t := range tonies {
		if t.ID == ref {
			return t, nil
		}
		if strings.EqualFold(t.Name, ref) {
			byName = append(byName, t)
		}
	}
	switch len(byName) {
	case 1:
		return byName[0], nil
	case 0:
		return toniecloud.CreativeTonie{}, notFoundErr("no creative tonie matches %q", ref)
	default:
		ids := make([]string, len(byName))
		for i, t := range byName {
			ids[i] = t.ID
		}
		return toniecloud.CreativeTonie{}, usageErr("%q is ambiguous: matches %s; use the id", ref, strings.Join(ids, ", "))
	}
}

// resolveChapter finds a chapter within a tonie by exact id, or by 1-based index
// (e.g. "3"), or by exact title.
func resolveChapter(t toniecloud.CreativeTonie, ref string) (int, error) {
	for i, ch := range t.Chapters {
		if ch.ID == ref {
			return i, nil
		}
	}
	// 1-based numeric index
	var idx int
	if _, err := fmt.Sscanf(ref, "%d", &idx); err == nil && fmt.Sprintf("%d", idx) == ref {
		if idx >= 1 && idx <= len(t.Chapters) {
			return idx - 1, nil
		}
		return -1, notFoundErr("chapter index %d out of range (1..%d)", idx, len(t.Chapters))
	}
	for i, ch := range t.Chapters {
		if strings.EqualFold(ch.Title, ref) {
			return i, nil
		}
	}
	return -1, notFoundErr("no chapter matches %q on tonie %q", ref, t.Name)
}
