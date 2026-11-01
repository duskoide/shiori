package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"time"

	"github.com/duskoide/shiori/internal/app"
	"github.com/duskoide/shiori/internal/update"
)

// hereLayout is where this shiori is installed; tests replace it.
var hereLayout = update.Here

// runUpdate is "shiori update": it installs the latest release, or the
// one --version names, in place of this binary and its bundled core.
func runUpdate(argv []string, out io.Writer, home string) error {
	check, want := false, ""
	for i := 0; i < len(argv); i++ {
		switch a := argv[i]; {
		case a == "--check":
			check = true
		case a == "--version" && i+1 < len(argv):
			want = argv[i+1]
			i++
		case len(a) > 10 && a[:10] == "--version=":
			want = a[10:]
		default:
			return fmt.Errorf("update takes --check and --version <v>, not %q", a)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	lookCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	opts := update.FromEnv()
	r, err := update.Latest(lookCtx, opts, want)
	switch {
	case errors.Is(err, update.ErrNoRelease) && want != "":
		return fmt.Errorf("no release %s of %s", want, opts.RepoName())
	case errors.Is(err, update.ErrNoRelease) && opts.Token == "":
		return fmt.Errorf("no release of %s found; while the repository is private, set GITHUB_TOKEN to a token that can read it", opts.RepoName())
	case errors.Is(err, update.ErrNoRelease):
		return fmt.Errorf("no release of %s found, with the token in GITHUB_TOKEN (or GH_TOKEN)", opts.RepoName())
	case err != nil:
		return fmt.Errorf("cannot look for a new version: %w", err)
	}
	newer := update.Compare(r.Version(), version) > 0
	if check {
		if newer {
			fmt.Fprintf(out, "Shiori %s is out; this is %s. Run: shiori update\n%s\n", r.Version(), version, r.URL)
		} else {
			fmt.Fprintf(out, "Shiori %s is up to date (the latest release is %s).\n", version, r.Version())
		}
		app.NoteUpdateCheck(home, r)
		return nil
	}
	if want == "" && !newer {
		fmt.Fprintf(out, "Shiori %s is up to date (the latest release is %s).\n", version, r.Version())
		return nil
	}
	l, err := hereLayout()
	if err != nil {
		return err
	}
	fmt.Fprintf(out, "Updating Shiori %s to %s\n", version, r.Version())
	err = update.Install(ctx, opts, r, l, func(step string) { fmt.Fprintln(out, "  "+step) })
	if err != nil {
		return err
	}
	app.NoteUpdateCheck(home, r)
	fmt.Fprintf(out, "Shiori %s is installed. What is new: %s\n", r.Version(), r.URL)
	if _, err := app.Status(home); err == nil {
		fmt.Fprintln(out, "The open window still runs the version before: close it and open Shiori again.")
	}
	return nil
}
