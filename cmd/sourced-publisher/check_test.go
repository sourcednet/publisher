package main

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/resolver"
	"github.com/sourcednet/testkit/testnet"
)

func TestCheckResolverAnswer(t *testing.T) {
	n := testnet.Standard(t)
	r, err := resolver.New(resolver.Config{Name: "resolver.test", DataDir: t.TempDir(), HTTP: n.Client(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	n.AddHandler("resolver.test", r.Handler())
	c := &resolver.Client{Base: "https://resolver.test", HTTP: n.Client()}
	_, raw, err := c.FetchAnswer(context.Background(), resolver.FetchRequest{URL: "https://daily-herald.test/news/2026/09/bridge-reopens.html"})
	if err != nil {
		t.Fatal(err)
	}

	httpFetcher = check.HTTPFetcher{Client: n.Client()}
	t.Cleanup(func() { httpFetcher = check.HTTPFetcher{} })
	dir := t.TempDir()
	good := filepath.Join(dir, "answer.json")
	bad := filepath.Join(dir, "tampered.json")
	os.WriteFile(good, raw, 0o644)
	os.WriteFile(bad, []byte(strings.Replace(string(raw), "2.4 million", "9.9 million", 1)), 0o644)

	if out := mustSourced(t, "check", good); !strings.HasPrefix(out, "OK answer from resolver.test: 1 records") {
		t.Fatalf("unexpected output:\n%s", out)
	}
	if code, out, _ := sourced(t, "check", bad); code != 1 || !strings.Contains(out, "FAILED") {
		t.Fatalf("tampered answer: exit %d\n%s", code, out)
	}
}
