package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"reflect"
	"text/tabwriter"
)

// format returns the effective output format: "json", "jsonl" or "table".
func (a *App) format() string {
	if a.JSON {
		return "json"
	}
	switch a.Output {
	case "json", "jsonl", "table":
		return a.Output
	case "":
		return "table"
	default:
		return "table"
	}
}

// emit renders v in the selected format. table is a callback that writes the
// human-readable representation; it is only invoked for table output.
func (a *App) emit(v any, table func(w io.Writer)) error {
	switch a.format() {
	case "json":
		return a.printJSON(v)
	case "jsonl":
		return a.printJSONL(v)
	default:
		if table == nil {
			return a.printJSON(v)
		}
		table(a.Stdout)
		return nil
	}
}

func (a *App) printJSON(v any) error {
	// A nil slice marshals to `null`; emit `[]` so agents can always iterate.
	if rv := reflect.ValueOf(v); rv.IsValid() && rv.Kind() == reflect.Slice && rv.IsNil() {
		_, err := fmt.Fprintln(a.Stdout, "[]")
		return err
	}
	enc := json.NewEncoder(a.Stdout)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(v)
}

// printJSONL writes one JSON document per line. For slices/arrays each element
// is its own line; everything else is a single line.
func (a *App) printJSONL(v any) error {
	rv := reflect.ValueOf(v)
	if rv.IsValid() && (rv.Kind() == reflect.Slice || rv.Kind() == reflect.Array) {
		for i := 0; i < rv.Len(); i++ {
			if err := a.encodeLine(rv.Index(i).Interface()); err != nil {
				return err
			}
		}
		return nil
	}
	return a.encodeLine(v)
}

func (a *App) encodeLine(v any) error {
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	_, err = fmt.Fprintf(a.Stdout, "%s\n", b)
	return err
}

// table returns a configured tabwriter writing to w.
func table(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// info prints a status line to stderr unless --quiet is set. stderr is used so
// it never pollutes machine-readable stdout.
func (a *App) info(format string, args ...any) {
	if a.Quiet {
		return
	}
	fmt.Fprintf(a.Stderr, format+"\n", args...)
}
