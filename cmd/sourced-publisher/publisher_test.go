package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/testkit/testsite"
)

// sourced runs the command and returns its exit code and output.
func sourced(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := run(context.Background(), args, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func mustSourced(t *testing.T, args ...string) string {
	t.Helper()
	code, out, errOut := sourced(t, args...)
	if code != 0 {
		t.Fatalf("sourced %s: exit %d\nstdout: %s\nstderr: %s", strings.Join(args, " "), code, out, errOut)
	}
	return out
}

func writePage(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, "public", filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	page := "<!doctype html>\n<html lang=\"en\">\n<head>\n<title>Guide</title>\n</head>\n<body>\n<main>\n<h1>Guide</h1>\n<p>" + body + "</p>\n</main>\n</body>\n</html>\n"
	if err := os.WriteFile(p, []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestPublisherWorkflow(t *testing.T) {
	dir := t.TempDir()
	mustSourced(t, "init", "-publisher", "example-library.test", "-now", "2026-09-28T10:00:00Z", dir)
	writePage(t, dir, "guides/a.html", testsite.Para("first", 400))

	out := mustSourced(t, "build", "-now", "2026-09-28T11:00:00Z", dir)
	if !strings.Contains(out, "1 new") || !strings.Contains(out, "Manifest updated") {
		t.Fatalf("unexpected build output:\n%s", out)
	}
	if out := mustSourced(t, "check", dir); !strings.HasPrefix(out, "OK example-library.test: 1 pages, 1 records") {
		t.Fatalf("unexpected check output:\n%s", out)
	}
	mustSourced(t, "check", filepath.Join(dir, "public")) // a bare web root works too

	// Declared changes: a correction with its note, reported as JSON.
	writePage(t, dir, "guides/a.html", testsite.Para("first, corrected", 400))
	out = mustSourced(t, "build", "-now", "2026-09-28T12:00:00Z", "-json", "-page", "guides/a.html", "-correction", "Fixed a claim.", dir)
	var rep struct {
		Pages []struct{ Action string } `json:"pages"`
	}
	if err := json.Unmarshal([]byte(out), &rep); err != nil || len(rep.Pages) != 1 || rep.Pages[0].Action != "correction" {
		t.Fatalf("unexpected JSON report (%v):\n%s", err, out)
	}

	// Misuse is rejected.
	for _, args := range [][]string{
		{"build", "-page", "guides/a.html", dir},
		{"build", "-correction", "x", dir},
		{"build", "-page", "guides/a.html", "-revision", "-withdraw", dir},
		{"build", "-page", "guides/a.html", "-correction", "nothing changed", dir},
		{"build", "-now", "yesterday", dir},
	} {
		if code, _, _ := sourced(t, args...); code == 0 {
			t.Errorf("sourced %s succeeded, want failure", strings.Join(args, " "))
		}
	}

	// Rotate and revoke; everything still checks.
	out = mustSourced(t, "keygen", "-now", "2026-10-01T00:00:00Z", dir)
	if !strings.Contains(out, "New signing key 2026b") {
		t.Fatalf("unexpected keygen output:\n%s", out)
	}
	if out := mustSourced(t, "revoke", "2026a", dir); !strings.Contains(out, "re-signed 2 records") {
		t.Fatalf("unexpected revoke output:\n%s", out)
	}
	mustSourced(t, "check", dir)

	// A tampered bundle fails the check with exit code 1.
	bundles, _ := filepath.Glob(filepath.Join(dir, "public", ".well-known", "sourced", "bundles", "*.json"))
	for _, b := range bundles {
		src, _ := os.ReadFile(b)
		os.WriteFile(b, bytes.Replace(src, []byte("first"), []byte("FIRST"), 1), 0o644)
	}
	if code, out, _ := sourced(t, "check", dir); code != 1 || !strings.Contains(out, "FAILED") {
		t.Fatalf("tampered site: exit %d\n%s", code, out)
	}
}

func TestCheckDomainUsesHTTPS(t *testing.T) {
	var got []string
	httpFetcher = fetcherFunc(func(u string) { got = append(got, u) })
	t.Cleanup(func() { httpFetcher = check.HTTPFetcher{} })

	code, _, _ := sourced(t, "check", "example-library.test")
	if code != 1 || len(got) == 0 || got[0] != "https://example-library.test/.well-known/sourced/keys.json" {
		t.Fatalf("exit %d, fetched %v", code, got)
	}
	if code, _, _ := sourced(t, "check", "not a domain"); code == 0 {
		t.Fatal("garbage target accepted")
	}
}

// fetcherFunc records URLs and fails every fetch.
type fetcherFunc func(string)

func (f fetcherFunc) Fetch(_ context.Context, u string) ([]byte, http.Header, error) {
	f(u)
	return nil, nil, check.ErrNotFound
}
