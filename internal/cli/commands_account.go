package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/bernardo-cs/tonys-cli/internal/toniecloud"
)

func meCommand() *Command {
	return &Command{
		Name:    "me",
		Summary: "Show the logged-in user (GET /me)",
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			u, err := a.Client().Me(ctx)
			if err != nil {
				return err
			}
			return a.emit(u, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintf(tw, "EMAIL\t%s\n", u.Email)
				fmt.Fprintf(tw, "UUID\t%s\n", u.UUID)
				for _, k := range []string{"firstName", "lastName", "locale", "country", "region"} {
					if v, ok := u.Extra[k]; ok && v != nil {
						fmt.Fprintf(tw, "%s\t%v\n", k, v)
					}
				}
				tw.Flush()
			})
		},
	}
}

func configCommand() *Command {
	return &Command{
		Name:    "config",
		Summary: "Show backend configuration and upload limits (GET /config)",
		Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
			cfg, err := a.Client().GetConfig(ctx)
			if err != nil {
				return err
			}
			return a.emit(cfg, func(w io.Writer) {
				tw := table(w)
				fmt.Fprintf(tw, "maxChapters\t%d\n", cfg.MaxChapters)
				fmt.Fprintf(tw, "maxSeconds\t%d\n", cfg.MaxSeconds)
				fmt.Fprintf(tw, "maxBytes\t%d\n", cfg.MaxBytes)
				fmt.Fprintf(tw, "accepts\t%v\n", cfg.Accepts)
				fmt.Fprintf(tw, "locales\t%v\n", cfg.Locales)
				fmt.Fprintf(tw, "ssoEnabled\t%v\n", cfg.SSOEnabled)
				tw.Flush()
			})
		},
	}
}

func authCommand() *Command {
	return &Command{
		Name:    "auth",
		Summary: "Manage authentication and the token cache",
		Sub: []*Command{
			{
				Name:    "login",
				Summary: "Acquire a fresh token and cache it",
				Long: `Perform a password-grant login with the resolved credentials (flags, then
$TONIE_USERNAME/$TONIE_PASSWORD, then config file) and store the token in the
cache so subsequent commands need no credentials.`,
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					entry, err := a.authenticator().Login(ctx)
					if err != nil {
						return err
					}
					a.info("Logged in as %s; token valid until %s", entry.Username, entry.ExpiresAt.Local().Format(time.RFC3339))
					out := authStatusEntry(entry, time.Now())
					return a.emit(out, func(w io.Writer) {
						tw := table(w)
						fmt.Fprintf(tw, "USERNAME\tEXPIRES\tVALID\n")
						fmt.Fprintf(tw, "%s\t%s\t%v\n", out.Username, out.ExpiresAt, out.Valid)
						tw.Flush()
					})
				},
			},
			{
				Name:    "status",
				Summary: "Show cached tokens and their validity",
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					if a.NoCache {
						return fmt.Errorf("token cache is disabled (--no-cache)")
					}
					cache := toniecloud.NewTokenCache(a.CachePath)
					now := time.Now()
					var rows []authStatus
					for _, e := range cache.Entries() {
						rows = append(rows, authStatusEntry(e, now))
					}
					return a.emit(rows, func(w io.Writer) {
						if len(rows) == 0 {
							a.info("No cached tokens at %s", a.CachePath)
							return
						}
						tw := table(w)
						fmt.Fprintf(tw, "USERNAME\tEXPIRES\tVALID\tHAS_REFRESH\n")
						for _, r := range rows {
							fmt.Fprintf(tw, "%s\t%s\t%v\t%v\n", r.Username, r.ExpiresAt, r.Valid, r.HasRefresh)
						}
						tw.Flush()
					})
				},
			},
			{
				Name:    "logout",
				Summary: "Delete cached token(s)",
				Flags: []FlagSpec{
					{Name: "all", Usage: "remove every cached account", Bool: true},
				},
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					cache := toniecloud.NewTokenCache(a.CachePath)
					if fbool(fs, "all") || a.Username == "" {
						cache.Delete("")
						a.info("Cleared all cached tokens")
					} else {
						cache.Delete(normalizeUser(a.Username))
						a.info("Removed cached token for %s", a.Username)
					}
					return a.emit(map[string]bool{"ok": true}, func(w io.Writer) {})
				},
			},
		},
	}
}

type authStatus struct {
	Username   string `json:"username"`
	ExpiresAt  string `json:"expiresAt"`
	Valid      bool   `json:"valid"`
	HasRefresh bool   `json:"hasRefresh"`
}

func authStatusEntry(e toniecloud.TokenEntry, now time.Time) authStatus {
	exp := ""
	if !e.ExpiresAt.IsZero() {
		exp = e.ExpiresAt.Local().Format(time.RFC3339)
	}
	return authStatus{
		Username:   e.Username,
		ExpiresAt:  exp,
		Valid:      e.Valid(now),
		HasRefresh: e.RefreshToken != "",
	}
}

// normalizeUser mirrors the auth package's normalization for cache keys.
func normalizeUser(u string) string {
	return toniecloud.NormalizeUser(u)
}
