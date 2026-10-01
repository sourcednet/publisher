package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/publisher"
	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/resolver/verifier"
)

// httpFetcher is what check uses for live domains. Tests replace it.
var httpFetcher check.Fetcher = check.HTTPFetcher{}

func cmdCheck(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := newFlags("check", "<project-dir | web-root | domain | answer.json>", stderr)
	asJSON := fs.Bool("json", false, "print the result as JSON")
	ca := fs.String("ca", "", "dev only: also trust this PEM certificate authority for live domains (e.g. a local test network)")
	if code, ok := parseFlags(fs, args); !ok {
		return code
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}
	target := fs.Arg(0)

	liveFetcher := func() (check.Fetcher, error) {
		if *ca == "" {
			return httpFetcher, nil
		}
		client, err := verifier.HTTPClient(*ca, 30*time.Second)
		if err != nil {
			return nil, err
		}
		return check.HTTPFetcher{Client: client}, nil
	}

	var (
		f    check.Fetcher
		pub  string
		opts check.Options
	)
	if fi, err := os.Stat(target); err == nil && !fi.IsDir() && strings.HasSuffix(target, ".json") {
		raw, err := os.ReadFile(target)
		if err != nil {
			return fail(stderr, err)
		}
		lf, err := liveFetcher()
		if err != nil {
			return fail(stderr, err)
		}
		return report(stdout, check.Answer(ctx, lf, raw), *asJSON, "answer from")
	}
	if fi, err := os.Stat(target); err == nil && fi.IsDir() {
		root := target
		if c, err := publisher.LoadConfig(target); err == nil {
			root, pub = c.RootDir(), c.Publisher
		} else if pub, err = publisherOfRoot(root); err != nil {
			return fail(stderr, err)
		}
		f = check.DirFetcher{Root: root, Publisher: pub}
	} else {
		pub = strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(target, "https://"), "/"))
		if strings.ContainsAny(pub, "/:") || !strings.Contains(pub, ".") {
			return fail(stderr, fmt.Errorf("%q is neither a directory nor a domain", target))
		}
		lf, err := liveFetcher()
		if err != nil {
			return fail(stderr, err)
		}
		f, opts = lf, check.Options{PagesRequired: true, LinkHeader: true}
	}

	return report(stdout, check.Publisher(ctx, f, pub, opts), *asJSON, "")
}

// report prints a check result and returns the exit code.
func report(stdout io.Writer, res *check.Result, asJSON bool, prefix string) int {
	if asJSON {
		b, _ := json.MarshalIndent(res, "", "  ")
		fmt.Fprintln(stdout, string(b))
	} else {
		for _, fd := range res.Findings {
			fmt.Fprintf(stdout, "%-7s %s: %s\n", fd.Severity, fd.Subject, fd.Message)
		}
		verdict := "OK"
		if !res.OK() {
			verdict = "FAILED"
		}
		if prefix != "" {
			fmt.Fprintf(stdout, "%s %s %s: %d records, %d findings.\n", verdict, prefix, res.Publisher, res.Records, len(res.Findings))
		} else {
			fmt.Fprintf(stdout, "%s %s: %d pages, %d records, %d findings.\n", verdict, res.Publisher, res.Pages, res.Records, len(res.Findings))
		}
	}
	if !res.OK() {
		return 1
	}
	return 0
}

// publisherOfRoot reads the publisher's domain from a web root's keys.json.
func publisherOfRoot(root string) (string, error) {
	b, err := os.ReadFile(filepath.Join(root, ".well-known", "sourced", "keys.json"))
	if err != nil {
		return "", fmt.Errorf("%s is not a project or a web root with /.well-known/sourced/keys.json", root)
	}
	var ks core.KeySet
	if err := json.Unmarshal(b, &ks); err != nil || ks.Publisher == "" {
		return "", errors.New("keys.json has no publisher")
	}
	return ks.Publisher, nil
}
