package cli

import (
	"context"
	"flag"
	"fmt"
	"io"
)

func householdCommand() *Command {
	return &Command{
		Name:    "household",
		Summary: "List households",
		Sub: []*Command{
			{
				Name:    "list",
				Summary: "List all households of the logged-in user",
				Run: func(ctx context.Context, a *App, fs *flag.FlagSet, args []string) error {
					hs, err := a.Client().Households(ctx)
					if err != nil {
						return err
					}
					return a.emit(hs, func(w io.Writer) {
						tw := table(w)
						fmt.Fprintf(tw, "ID\tNAME\tOWNER\tACCESS\n")
						for _, h := range hs {
							fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", h.ID, h.Name, h.OwnerName, h.Access)
						}
						tw.Flush()
					})
				},
			},
		},
	}
}
