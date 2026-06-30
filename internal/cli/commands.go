package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
)

// rootCommands is the authoritative command registry. It drives dispatch, help
// and the `schema` command.
func rootCommands() []*Command {
	return []*Command{
		meCommand(),
		configCommand(),
		authCommand(),
		householdCommand(),
		tonieCommand(),
		uploadCommand(),
		chapterCommand(),
		ytCommand(),
		loudnessCommand(),
		fileCommand(),
		rawCommand(),
		doctorCommand(),
		schemaCommand(),
		versionCommand(),
		helpCommand(),
	}
}

func versionCommand() *Command {
	return &Command{
		Name:    "version",
		Summary: "Print the CLI version",
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			info := map[string]string{
				"version":  Version,
				"apiUrl":   a.APIURL,
				"tokenUrl": a.TokenURL,
			}
			return a.emit(info, func(w io.Writer) {
				fmt.Fprintf(w, "tonys %s\n", Version)
			})
		},
	}
}

func helpCommand() *Command {
	return &Command{
		Name:    "help",
		Summary: "Show help for tonys or a command",
		Args:    "[command]",
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			if len(args) == 0 {
				a.printRootHelp()
				return nil
			}
			cmds := rootCommands()
			path := "tonys"
			var cur *Command
			for _, name := range args {
				var pool []*Command
				if cur == nil {
					pool = cmds
				} else {
					pool = cur.Sub
				}
				next := findCommand(pool, name)
				if next == nil {
					return usageErr("unknown command %q", name)
				}
				cur = next
				path += " " + name
			}
			a.printCommandHelp(cur, path)
			return nil
		},
	}
}

// cmdSchema is the machine-readable description emitted by `tonys schema`.
type cmdSchema struct {
	Name        string      `json:"name"`
	Path        string      `json:"path"`
	Summary     string      `json:"summary"`
	Args        string      `json:"args,omitempty"`
	Flags       []FlagSpec  `json:"flags,omitempty"`
	Subcommands []cmdSchema `json:"subcommands,omitempty"`
}

func describe(cmd *Command, path string) cmdSchema {
	cs := cmdSchema{
		Name:    cmd.Name,
		Path:    path,
		Summary: cmd.Summary,
		Args:    cmd.Args,
		Flags:   cmd.Flags,
	}
	for _, sub := range cmd.Sub {
		cs.Subcommands = append(cs.Subcommands, describe(sub, path+" "+sub.Name))
	}
	return cs
}

func schemaCommand() *Command {
	return &Command{
		Name:    "schema",
		Summary: "Emit a machine-readable description of every command (for agents)",
		Long: `Print a JSON tree describing all commands, their arguments and flags, plus
global flags and environment variables. Intended for agents to introspect the
CLI. This command always outputs JSON.`,
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			var cmds []cmdSchema
			for _, c := range rootCommands() {
				cmds = append(cmds, describe(c, "tonys "+c.Name))
			}
			doc := map[string]any{
				"name":    "tonys",
				"version": Version,
				"summary": "Agent-friendly CLI for the TonieCloud API",
				"globalFlags": []FlagSpec{
					{Name: "json", Usage: "output as JSON", Bool: true},
					{Name: "output", Usage: "output format: table|json|jsonl", Default: "table"},
					{Name: "username", Usage: "TonieCloud username ($TONIE_USERNAME)"},
					{Name: "password", Usage: "TonieCloud password ($TONIE_PASSWORD)"},
					{Name: "api-url", Usage: "API base URL", Default: a.APIURL},
					{Name: "token-url", Usage: "OpenID token URL"},
					{Name: "no-cache", Usage: "ignore the token cache", Bool: true},
					{Name: "timeout", Usage: "per-request timeout", Default: "3m0s"},
					{Name: "quiet", Usage: "suppress stderr status", Bool: true},
					{Name: "verbose", Usage: "verbose stderr logging", Bool: true},
				},
				"env": map[string]string{
					"TONIE_USERNAME":    "username for password-grant login (aliases: TONIES_USERNAME, TONIE_CLOUD_USERNAME)",
					"TONIE_PASSWORD":    "password for password-grant login (aliases: TONIES_PASSWORD, TONIE_CLOUD_PASSWORD)",
					"TONYS_OUTPUT":      "default output format (table|json|jsonl)",
					"TONYS_CONFIG":      "path to config.json",
					"TONYS_CACHE":       "path to the token cache",
					"TONYS_LOUDNESS_DB": "path to the loudness ledger",
					"TONIE_API_URL":     "override API base URL",
					"TONIE_TOKEN_URL":   "override OpenID token URL",
					"TONYS_FFMPEG":      "ffmpeg binary path (alias: TONIE_FFMPEG)",
					"TONYS_YTDLP":       "yt-dlp binary path (alias: TONIE_YTDLP)",
					"XDG_CONFIG_HOME":   "base dir for config (else ~/.config)",
					"XDG_CACHE_HOME":    "base dir for the token cache (else ~/.cache)",
					"XDG_DATA_HOME":     "base dir for the loudness ledger (else ~/.local/share)",
				},
				"exitCodes": map[string]string{
					"0": "success",
					"1": "error",
					"2": "usage error",
					"3": "authentication error",
					"4": "resource not found",
				},
				"commands": cmds,
			}
			return a.printJSON(doc)
		},
	}
}
