package cli

import (
	"fmt"
	"sort"
	"strings"
)

const rootIntro = `tonys — agent-friendly CLI for TonieCloud (creative tonies).

Send audio to your kids' creative tonies from scripts and bots. Tables by
default; pass --json (or -o json) for machine-readable output.

Usage:
  tonys [global flags] <command> [subcommand] [args] [flags]

Auth (env is the recommended path for bots):
  export TONIE_USERNAME=you@example.com
  export TONIE_PASSWORD=secret
  tonys upload "Erna-Tonie" bedtime.mp3`

func (a *App) printRootHelp() {
	w := a.Stderr
	fmt.Fprintln(w, rootIntro)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	tw := table(w)
	for _, c := range rootCommands() {
		fmt.Fprintf(tw, "  %s\t%s\n", c.Name, c.Summary)
	}
	tw.Flush()
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Global flags:")
	fmt.Fprintln(w, "  --json, -o FORMAT   output as json|jsonl|table (default table)")
	fmt.Fprintln(w, "  --username, --password, --api-url, --token-url")
	fmt.Fprintln(w, "  --no-cache          ignore the token cache")
	fmt.Fprintln(w, "  --timeout DURATION  per-request timeout (default 3m)")
	fmt.Fprintln(w, "  -q/--quiet, -v/--verbose")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Run `tonys <command> -h` for command help, or `tonys schema` for a")
	fmt.Fprintln(w, "machine-readable description of every command.")
}

func (a *App) printCommandHelp(cmd *Command, path string) {
	w := a.Stderr
	usage := path
	if len(cmd.Sub) > 0 {
		usage += " <subcommand>"
	}
	if cmd.Args != "" {
		usage += " " + cmd.Args
	}
	fmt.Fprintf(w, "Usage:\n  %s [flags]\n\n", usage)
	if cmd.Long != "" {
		fmt.Fprintln(w, strings.TrimSpace(cmd.Long))
		fmt.Fprintln(w)
	} else if cmd.Summary != "" {
		fmt.Fprintln(w, cmd.Summary)
		fmt.Fprintln(w)
	}
	if len(cmd.Sub) > 0 {
		fmt.Fprintln(w, "Subcommands:")
		tw := table(w)
		for _, s := range cmd.Sub {
			fmt.Fprintf(tw, "  %s\t%s\n", s.Name, s.Summary)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}
	if len(cmd.Flags) > 0 {
		fmt.Fprintln(w, "Flags:")
		tw := table(w)
		flags := append([]FlagSpec(nil), cmd.Flags...)
		sort.Slice(flags, func(i, j int) bool { return flags[i].Name < flags[j].Name })
		for _, f := range flags {
			def := ""
			if f.Default != "" && f.Default != "false" {
				def = fmt.Sprintf(" (default %q)", f.Default)
			}
			fmt.Fprintf(tw, "  --%s\t%s%s\n", f.Name, f.Usage, def)
		}
		tw.Flush()
		fmt.Fprintln(w)
	}
	fmt.Fprintln(w, "Global flags (e.g. --json, -o, --no-cache) are also accepted.")
}
