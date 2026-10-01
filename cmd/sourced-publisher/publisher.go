package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/publisher"
)

// newFlags returns a flag set that reports errors instead of exiting.
func newFlags(name, args string, stderr io.Writer) *flag.FlagSet {
	fs := flag.NewFlagSet(name, flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: sourced-publisher %s [flags] %s\n\nFlags:\n", name, args)
		fs.PrintDefaults()
	}
	return fs
}

// parseFlags parses args and returns the exit code to use if it failed.
func parseFlags(fs *flag.FlagSet, args []string) (int, bool) {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0, false
		}
		return 2, false
	}
	return 0, true
}

// timeFlag parses an RFC 3339 time; empty means now.
type timeFlag struct{ t time.Time }

func (f *timeFlag) String() string {
	if f.t.IsZero() {
		return ""
	}
	return f.t.Format(time.RFC3339)
}

func (f *timeFlag) Set(s string) error {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return errors.New("want an RFC 3339 time such as 2026-09-30T09:00:00Z")
	}
	f.t = t
	return nil
}

// listFlag collects a repeatable string flag.
type listFlag []string

func (f *listFlag) String() string     { return strings.Join(*f, ",") }
func (f *listFlag) Set(s string) error { *f = append(*f, s); return nil }

func projectDir(fs *flag.FlagSet) (string, error) {
	switch fs.NArg() {
	case 0:
		return ".", nil
	case 1:
		return fs.Arg(0), nil
	}
	return "", errors.New("want at most one project directory")
}

func fail(stderr io.Writer, err error) int {
	fmt.Fprintln(stderr, "sourced-publisher:", err)
	return 1
}

func cmdInit(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("init", "[project-dir]", stderr)
	pub := fs.String("publisher", "", "the publisher's domain, e.g. example.org (required)")
	root := fs.String("root", "public", "the web root, relative to the project directory")
	var now timeFlag
	fs.Var(&now, "now", "time used to name the first key (RFC 3339; default now)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	dir, err := projectDir(fs)
	if err != nil || *pub == "" {
		fs.Usage()
		return 2
	}
	c, err := publisher.Init(dir, *pub, *root, now.t)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "Created %s/%s for %s with signing key %s.\n", dir, publisher.ConfigFile, c.Publisher, c.SigningKey)
	fmt.Fprintf(stdout, "Private keys are in %s. Keep them out of the web root and back them up.\n", c.KeysPath())
	fmt.Fprintln(stdout, "Next: put your site in the web root and run `sourced-publisher build`.")
	return 0
}

func cmdBuild(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("build", "[project-dir]", stderr)
	var now timeFlag
	var pages listFlag
	fs.Var(&now, "now", "publication time for new records (RFC 3339; default now)")
	fs.Var(&pages, "page", "page the declared change applies to: a URL or a path in the web root (repeatable)")
	revision := fs.Bool("revision", false, "declare a revision for -page (the default for changed pages)")
	correction := fs.String("correction", "", "declare a correction for -page, with this note")
	retraction := fs.String("retraction", "", "declare a retraction for -page, with this note")
	withdraw := fs.Bool("withdraw", false, "withdraw -page: record the withdrawal and delete its text")
	note := fs.String("note", "", "optional note for -withdraw or -revision")
	asJSON := fs.Bool("json", false, "print the report as JSON")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	dir, err := projectDir(fs)
	if err != nil {
		fs.Usage()
		return 2
	}

	var decl *publisher.Declaration
	n := 0
	for _, d := range []struct {
		set bool
		d   publisher.Declaration
	}{
		{*revision, publisher.Declaration{Change: core.ChangeRevision, Note: *note}},
		{*correction != "", publisher.Declaration{Change: core.ChangeCorrection, Note: *correction}},
		{*retraction != "", publisher.Declaration{Change: core.ChangeRetraction, Note: *retraction}},
		{*withdraw, publisher.Declaration{Change: core.ChangeWithdrawal, Note: *note}},
	} {
		if d.set {
			n++
			decl = &d.d
		}
	}
	switch {
	case n > 1:
		return fail(stderr, errors.New("declare one change at a time: -revision, -correction, -retraction, or -withdraw"))
	case n == 1 && len(pages) == 0:
		return fail(stderr, errors.New("a declared change needs at least one -page"))
	case n == 0 && len(pages) > 0:
		return fail(stderr, errors.New("-page needs a declared change: -revision, -correction, -retraction, or -withdraw"))
	}
	opts := publisher.BuildOptions{Now: now.t, Declare: map[string]publisher.Declaration{}}
	for _, p := range pages {
		opts.Declare[p] = *decl
	}

	c, err := publisher.LoadConfig(dir)
	if err != nil {
		return fail(stderr, err)
	}
	rep, err := publisher.Build(c, opts)
	if err != nil {
		return fail(stderr, err)
	}
	if *asJSON {
		b, _ := json.MarshalIndent(rep, "", "  ")
		fmt.Fprintln(stdout, string(b))
		return 0
	}
	for _, p := range rep.Pages {
		if p.Action != publisher.ActionUnchanged {
			fmt.Fprintf(stdout, "%-10s %s\n", p.Action, p.URL)
		}
	}
	for _, w := range rep.Warnings {
		fmt.Fprintln(stdout, "warning:", w)
	}
	changed := 0
	for _, p := range rep.Pages {
		switch p.Action {
		case publisher.ActionNew, publisher.ActionUnchanged, publisher.ActionMissing, publisher.ActionSkipped:
		default:
			changed++
		}
	}
	manifest := "unchanged"
	if rep.ManifestWritten {
		manifest = "updated"
	}
	fmt.Fprintf(stdout, "%d pages: %d new, %d changed, %d unchanged. Manifest %s.\n",
		len(rep.Pages), rep.Count(publisher.ActionNew), changed, rep.Count(publisher.ActionUnchanged), manifest)
	return 0
}

func cmdKeygen(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("keygen", "[project-dir]", stderr)
	var now timeFlag
	fs.Var(&now, "now", "time used to name the key (RFC 3339; default now)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	dir, err := projectDir(fs)
	if err != nil {
		fs.Usage()
		return 2
	}
	c, err := publisher.LoadConfig(dir)
	if err != nil {
		return fail(stderr, err)
	}
	prev := c.SigningKey
	id, err := publisher.AddKey(c, now.t)
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "New signing key %s. Key %s is retired: what it signed stays valid.\n", id, prev)
	fmt.Fprintln(stdout, "Publish the web root so keys.json goes live before the next build.")
	return 0
}

func cmdRevoke(_ context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("revoke", "<key-id> [project-dir]", stderr)
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() < 1 || fs.NArg() > 2 {
		fs.Usage()
		return 2
	}
	dir := "."
	if fs.NArg() == 2 {
		dir = fs.Arg(1)
	}
	c, err := publisher.LoadConfig(dir)
	if err != nil {
		return fail(stderr, err)
	}
	n, err := publisher.RevokeKey(c, fs.Arg(0))
	if err != nil {
		return fail(stderr, err)
	}
	fmt.Fprintf(stdout, "Revoked %s and re-signed %d records with %s. Record IDs and citations are unchanged.\n", fs.Arg(0), n, c.SigningKey)
	return 0
}
