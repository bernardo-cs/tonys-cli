// Package cli implements the `tonys` command-line interface: an agent-friendly
// wrapper around the TonieCloud API (package toniecloud).
package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

// Version is the CLI version, overridable at build time with -ldflags.
var Version = "0.1.0"

// Command is one node in the command tree. A node with Sub children is a group;
// a node with Run is a leaf (groups may also have Run as a default action).
type Command struct {
	Name    string
	Summary string     // one-line description (shown in lists & schema)
	Long    string     // longer help text
	Args    string     // positional-argument spec for help, e.g. "<tonie> <file>"
	Flags   []FlagSpec // command-specific flags
	Run     func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error
	Sub     []*Command
}

// FlagSpec declaratively describes a flag so the same data drives parsing, help
// and the machine-readable `schema` command.
type FlagSpec struct {
	Name    string `json:"name"`
	Usage   string `json:"usage"`
	Default string `json:"default,omitempty"`
	Bool    bool   `json:"bool,omitempty"`
}

// App holds resolved configuration and I/O streams for one invocation.
type App struct {
	Username string
	Password string
	APIURL   string
	TokenURL string

	ConfigPath string
	CachePath  string
	NoCache    bool
	Timeout    time.Duration

	JSON    bool
	Output  string
	Verbose bool
	Quiet   bool

	Stdin  io.Reader
	Stdout io.Writer
	Stderr io.Writer

	ctx    context.Context
	client *toniecloud.Client
	loud   *loudnessCache
}

// loudness returns the memoized loudness cache (ledger).
func (a *App) loudness() *loudnessCache {
	if a.loud == nil {
		a.loud = newLoudnessCache(a.loudnessCachePath())
	}
	return a.loud
}

// fileConfig is the on-disk config file schema (~/.config/tonys/config.json).
type fileConfig struct {
	Username string `json:"username"`
	Password string `json:"password"`
	APIURL   string `json:"api_url"`
	TokenURL string `json:"token_url"`
	Output   string `json:"output"`
}

// NewApp builds an App with configuration resolved in precedence order:
// built-in defaults < config file < environment (flags are applied later by the
// dispatcher).
func NewApp() *App {
	a := &App{
		APIURL:   toniecloud.DefaultAPIURL,
		TokenURL: toniecloud.DefaultTokenURL,
		Output:   "table",
		Timeout:  3 * time.Minute,
		Stdin:    os.Stdin,
		Stdout:   os.Stdout,
		Stderr:   os.Stderr,
	}

	a.ConfigPath = firstNonEmpty(os.Getenv("TONYS_CONFIG"), filepath.Join(configDir(), "config.json"))
	a.CachePath = firstNonEmpty(os.Getenv("TONYS_CACHE"), filepath.Join(cacheDir(), "token.json"))

	// Config file (lowest precedence after defaults).
	if fc, ok := loadFileConfig(a.ConfigPath); ok {
		applyIf(&a.Username, fc.Username)
		applyIf(&a.Password, fc.Password)
		applyIf(&a.APIURL, fc.APIURL)
		applyIf(&a.TokenURL, fc.TokenURL)
		applyIf(&a.Output, fc.Output)
	}

	// Environment overrides config.
	applyIf(&a.Username, firstNonEmpty(os.Getenv("TONIE_USERNAME"), os.Getenv("TONIES_USERNAME"), os.Getenv("TONIE_CLOUD_USERNAME")))
	applyIf(&a.Password, firstNonEmpty(os.Getenv("TONIE_PASSWORD"), os.Getenv("TONIES_PASSWORD"), os.Getenv("TONIE_CLOUD_PASSWORD")))
	applyIf(&a.APIURL, os.Getenv("TONIE_API_URL"))
	applyIf(&a.TokenURL, os.Getenv("TONIE_TOKEN_URL"))
	applyIf(&a.Output, os.Getenv("TONYS_OUTPUT"))

	return a
}

// Client lazily builds (and memoizes) the TonieCloud client.
func (a *App) Client() *toniecloud.Client {
	if a.client != nil {
		return a.client
	}
	var cache *toniecloud.TokenCache
	if !a.NoCache {
		cache = toniecloud.NewTokenCache(a.CachePath)
	}
	auth := &toniecloud.Authenticator{
		TokenURL: a.TokenURL,
		Username: a.Username,
		Password: a.Password,
		HTTP:     &http.Client{Timeout: a.Timeout},
		Cache:    cache,
	}
	a.client = toniecloud.New(a.APIURL, auth, &http.Client{Timeout: a.Timeout})
	return a.client
}

// authenticator builds a standalone authenticator (used by the auth commands).
func (a *App) authenticator() *toniecloud.Authenticator {
	var cache *toniecloud.TokenCache
	if !a.NoCache {
		cache = toniecloud.NewTokenCache(a.CachePath)
	}
	return &toniecloud.Authenticator{
		TokenURL: a.TokenURL,
		Username: a.Username,
		Password: a.Password,
		HTTP:     &http.Client{Timeout: a.Timeout},
		Cache:    cache,
	}
}

// Execute parses args and runs the matching command.
func (a *App) Execute(args []string) error {
	toniecloud.UserAgent = "tonys-cli/" + Version

	gfs := flag.NewFlagSet("tonys", flag.ContinueOnError)
	gfs.SetOutput(io.Discard)
	gfs.Usage = func() {}
	a.bindGlobals(gfs)
	if err := gfs.Parse(args); err != nil {
		if err == flag.ErrHelp {
			a.printRootHelp()
			return nil
		}
		return usageErr("%v", err)
	}
	rest := gfs.Args()
	if len(rest) == 0 {
		a.printRootHelp()
		return nil
	}

	if a.ctx == nil {
		a.ctx = context.Background()
	}

	cmd := findCommand(rootCommands(), rest[0])
	if cmd == nil {
		return usageErr("unknown command %q (run `tonys help`)", rest[0])
	}
	return a.runCommand(cmd, rest[1:], "tonys "+rest[0])
}

func (a *App) runCommand(cmd *Command, args []string, path string) error {
	for len(cmd.Sub) > 0 {
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			if cmd.Run != nil {
				break
			}
			a.printCommandHelp(cmd, path)
			return nil
		}
		sub := findCommand(cmd.Sub, args[0])
		if sub == nil {
			return usageErr("unknown subcommand %q for `%s`", args[0], path)
		}
		path += " " + args[0]
		args = args[1:]
		cmd = sub
	}
	if cmd.Run == nil {
		a.printCommandHelp(cmd, path)
		return nil
	}

	fs := flag.NewFlagSet(path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}
	a.bindGlobals(fs)
	bindFlags(fs, cmd.Flags)
	// Go's flag package stops at the first positional argument; reorder so flags
	// may be interspersed with positionals (e.g. `upload ID file --title X`).
	if err := fs.Parse(reorderArgs(fs, args)); err != nil {
		if err == flag.ErrHelp {
			a.printCommandHelp(cmd, path)
			return nil
		}
		return usageErr("%s: %v", path, err)
	}

	// Per-request timeouts are enforced by the HTTP client (a.Timeout); the
	// command context is only cancelled on interrupt, so long operations like
	// playlist imports or large uploads are not artificially bounded.
	return cmd.Run(a.ctx, a, fs, fs.Args())
}

// SetContext installs the root context (used by main for signal cancellation).
func (a *App) SetContext(ctx context.Context) { a.ctx = ctx }

// bindGlobals registers global flags on fs, bound to App fields so they may
// appear before or after the command name.
func (a *App) bindGlobals(fs *flag.FlagSet) {
	fs.StringVar(&a.Username, "username", a.Username, "TonieCloud username (or $TONIE_USERNAME)")
	fs.StringVar(&a.Password, "password", a.Password, "TonieCloud password (or $TONIE_PASSWORD)")
	fs.StringVar(&a.APIURL, "api-url", a.APIURL, "API base URL")
	fs.StringVar(&a.TokenURL, "token-url", a.TokenURL, "OpenID token URL")
	fs.StringVar(&a.Output, "output", a.Output, "output format: table|json|jsonl")
	fs.StringVar(&a.Output, "o", a.Output, "output format (shorthand)")
	fs.BoolVar(&a.JSON, "json", a.JSON, "shorthand for --output json")
	fs.BoolVar(&a.Verbose, "verbose", a.Verbose, "verbose logging to stderr")
	fs.BoolVar(&a.Verbose, "v", a.Verbose, "verbose (shorthand)")
	fs.BoolVar(&a.Quiet, "quiet", a.Quiet, "suppress status messages on stderr")
	fs.BoolVar(&a.Quiet, "q", a.Quiet, "quiet (shorthand)")
	fs.BoolVar(&a.NoCache, "no-cache", a.NoCache, "do not read or write the token cache")
	fs.DurationVar(&a.Timeout, "timeout", a.Timeout, "per-request timeout")
}

func bindFlags(fs *flag.FlagSet, specs []FlagSpec) {
	for _, s := range specs {
		if fs.Lookup(s.Name) != nil {
			continue // don't clobber a global flag of the same name
		}
		if s.Bool {
			fs.Bool(s.Name, s.Default == "true", s.Usage)
		} else {
			fs.String(s.Name, s.Default, s.Usage)
		}
	}
}

// reorderArgs moves flags (and their values) ahead of positional arguments so
// the stdlib flag parser, which stops at the first positional, still sees every
// flag. fs must already have all flags registered. "--" ends flag scanning; a
// lone "-" is treated as a positional (stdin).
func reorderArgs(fs *flag.FlagSet, args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--":
			// Keep the terminator so the stdlib parser also stops scanning;
			// otherwise a leading positional starting with "-" is misread as a flag.
			pos = append(pos, args[i+1:]...)
			return append(append(flags, "--"), pos...)
		case len(a) > 1 && a[0] == '-':
			flags = append(flags, a)
			if strings.Contains(a, "=") {
				continue // --name=value: value is attached
			}
			name := strings.TrimLeft(a, "-")
			if f := fs.Lookup(name); f != nil && !isBoolFlag(f) && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
		default:
			pos = append(pos, a)
		}
	}
	return append(flags, pos...)
}

func isBoolFlag(f *flag.Flag) bool {
	bf, ok := f.Value.(interface{ IsBoolFlag() bool })
	return ok && bf.IsBoolFlag()
}

func findCommand(cmds []*Command, name string) *Command {
	for _, c := range cmds {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// flag accessors -------------------------------------------------------------

func fstr(fs *flag.FlagSet, name string) string {
	if f := fs.Lookup(name); f != nil {
		return f.Value.String()
	}
	return ""
}

func fbool(fs *flag.FlagSet, name string) bool {
	return fstr(fs, name) == "true"
}

// usageError signals a user mistake (bad flags/args) → exit code 2.
type usageError struct{ msg string }

func (e *usageError) Error() string { return e.msg }

func usageErr(format string, args ...any) error {
	return &usageError{fmt.Sprintf(format, args...)}
}

// ExitCode maps an error to a process exit code.
func ExitCode(err error) int {
	switch {
	case err == nil:
		return 0
	case isUsage(err):
		return 2
	case isAuth(err):
		return 3
	case isNotFound(err):
		return 4
	default:
		return 1
	}
}

func isNotFound(err error) bool {
	if toniecloud.IsNotFound(err) {
		return true
	}
	_, ok := err.(*notFoundError)
	return ok
}

// PrintError renders err to stderr: a structured JSON object when machine output
// is selected, otherwise a human-readable line. stdout is never touched, so it
// stays clean for agents parsing successful output.
func (a *App) PrintError(err error) {
	if err == nil {
		return
	}
	switch a.format() {
	case "json", "jsonl":
		obj := map[string]any{"error": err.Error(), "class": errorClass(err)}
		var apiErr *toniecloud.APIError
		if errors.As(err, &apiErr) {
			obj["status"] = apiErr.Status
		}
		b, _ := json.Marshal(obj)
		fmt.Fprintf(a.Stderr, "%s\n", b)
	default:
		fmt.Fprintf(a.Stderr, "error: %s\n", err.Error())
	}
}

func errorClass(err error) string {
	switch {
	case isUsage(err):
		return "usage"
	case isAuth(err):
		return "auth"
	case isNotFound(err):
		return "not_found"
	default:
		return "error"
	}
}

func isUsage(err error) bool {
	_, ok := err.(*usageError)
	return ok
}

func isAuth(err error) bool {
	return err == toniecloud.ErrNotAuthenticated || toniecloud.IsUnauthorized(err)
}

// config/path helpers --------------------------------------------------------

func configDir() string {
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "tonys")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "tonys")
}

func cacheDir() string {
	if x := os.Getenv("XDG_CACHE_HOME"); x != "" {
		return filepath.Join(x, "tonys")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".cache", "tonys")
}

func loadFileConfig(path string) (fileConfig, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return fileConfig{}, false
	}
	var fc fileConfig
	if json.Unmarshal(data, &fc) != nil {
		return fileConfig{}, false
	}
	return fc, true
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func applyIf(dst *string, v string) {
	if v != "" {
		*dst = v
	}
}
