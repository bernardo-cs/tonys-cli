package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

func rawCommand() *Command {
	return &Command{
		Name:    "raw",
		Summary: "Call any API endpoint directly (escape hatch for full coverage)",
		Args:    "<METHOD> <path>",
		Long: `Issue an authenticated request to an arbitrary TonieCloud endpoint and print
the JSON response. The path is relative to the API base (leading slash
optional). Use --data to send a JSON body.

Examples:
  tonys raw GET me
  tonys raw GET households/abcd-123/creativetonies
  tonys raw PATCH households/abcd-123/creativetonies/XYZ --data '{"name":"New"}'`,
		Flags: []FlagSpec{
			{Name: "data", Usage: "JSON request body; @file to read a file, - for stdin"},
		},
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			if len(args) < 2 {
				return usageErr("raw requires <METHOD> and <path>")
			}
			method := strings.ToUpper(args[0])
			path := args[1]

			var body any
			if d := fstr(fs, "data"); d != "" {
				raw, err := readData(a, d)
				if err != nil {
					return err
				}
				if len(bytes.TrimSpace(raw)) > 0 {
					if !json.Valid(raw) {
						return usageErr("--data is not valid JSON")
					}
					body = json.RawMessage(raw)
				}
			}

			resp, err := a.Client().Raw(ctx, method, path, body)
			if err != nil {
				// Even on HTTP error, show the server's JSON body if present.
				if len(bytes.TrimSpace(resp)) > 0 {
					a.printRaw(resp)
				}
				return err
			}
			a.printRaw(resp)
			return nil
		},
	}
}

func fileCommand() *Command {
	return &Command{
		Name:    "file",
		Summary: "Low-level file-upload helpers",
		Sub: []*Command{
			{
				Name:    "create",
				Summary: "Request a presigned S3 upload slot (POST /file)",
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					fr, err := a.Client().CreateFileUpload(ctx)
					if err != nil {
						return err
					}
					return a.emit(fr, func(w io.Writer) {
						tw := table(w)
						fmt.Fprintf(tw, "FILE_ID\t%s\n", fr.FileID)
						fmt.Fprintf(tw, "UPLOAD_URL\t%s\n", fr.Request.URL)
						tw.Flush()
					})
				},
			},
		},
	}
}

// readData resolves a --data value: literal JSON, @file, or - for stdin.
func readData(a *App, d string) ([]byte, error) {
	switch {
	case d == "-":
		return io.ReadAll(a.Stdin)
	case strings.HasPrefix(d, "@"):
		return os.ReadFile(d[1:])
	default:
		return []byte(d), nil
	}
}

// printRaw pretty-prints a JSON response (or echoes it verbatim if unparseable).
func (a *App) printRaw(raw json.RawMessage) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return
	}
	if a.format() == "jsonl" {
		fmt.Fprintf(a.Stdout, "%s\n", trimmed)
		return
	}
	var buf bytes.Buffer
	if json.Indent(&buf, trimmed, "", "  ") == nil {
		buf.WriteByte('\n')
		a.Stdout.Write(buf.Bytes())
		return
	}
	fmt.Fprintf(a.Stdout, "%s\n", trimmed)
}

var _ = toniecloud.DefaultAPIURL // keep import used if helpers change
