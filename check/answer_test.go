package check_test

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/publisher"
	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/resolver"
	"github.com/sourcednet/testkit/testnet"
	"github.com/sourcednet/testkit/testsite"
)

const bridge = "https://daily-herald.test/news/2026/09/bridge-reopens.html"

func resolverNet(t *testing.T) (*testnet.Network, *resolver.Client, check.Fetcher) {
	t.Helper()
	n := testnet.Standard(t)
	r, err := resolver.New(resolver.Config{Name: "resolver.test", DataDir: t.TempDir(), HTTP: n.Client(), Logger: slog.New(slog.DiscardHandler)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	n.AddHandler("resolver.test", r.Handler())
	return n, &resolver.Client{Base: "https://resolver.test", HTTP: n.Client()}, check.HTTPFetcher{Client: n.Client()}
}

// edit decodes an answer, changes it, and re-encodes it without re-signing.
func edit(t *testing.T, raw []byte, f func(map[string]any)) []byte {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	f(m)
	out, _ := json.Marshal(m)
	return out
}

func TestAnswerResolve(t *testing.T) {
	n, c, f := resolverNet(t)
	a, _, err := c.FetchAnswer(context.Background(), resolver.FetchRequest{URL: bridge})
	if err != nil {
		t.Fatal(err)
	}
	var cite string
	for _, ch := range a.Chunks {
		if strings.Contains(ch.Text, "2.4 million") {
			cite = ch.Cite
		}
	}
	site := n.Site("daily-herald.test")
	page := site.Read("news/2026/09/bridge-reopens.html")
	site.WriteFile("news/2026/09/bridge-reopens.html", strings.Replace(page, "2.4 million", "3.1 million", 1))
	site.Build(testsite.T0.Add(time.Hour), map[string]publisher.Declaration{"news/2026/09/bridge-reopens.html": {Change: core.ChangeCorrection, Note: "Cost."}})

	ra, raw, err := c.ResolveAnswer(context.Background(), cite)
	if err != nil || ra.State != core.StateCorrected {
		t.Fatalf("resolve: %+v, %v", ra, err)
	}
	if res := check.Answer(context.Background(), f, raw); !res.OK() || res.Records != 2 {
		t.Fatalf("genuine resolve answer: %d records, %+v", res.Records, res.Findings)
	}
	// Altering the cited passage's text is caught.
	swapped := edit(t, raw, func(m map[string]any) {
		p := m["passage"].(map[string]any)
		p["text"] = strings.Replace(p["text"].(string), "2.4 million", "3.1 million", 1)
	})
	if res := check.Answer(context.Background(), f, swapped); res.OK() {
		t.Fatal("altered cited passage passed")
	}
}

func TestAnswerProblems(t *testing.T) {
	_, c, f := resolverNet(t)
	_, raw, err := c.FetchAnswer(context.Background(), resolver.FetchRequest{URL: bridge})
	if err != nil {
		t.Fatal(err)
	}
	for name, tt := range map[string]struct {
		raw    []byte
		substr string
	}{
		"not json":         {[]byte("{"), "unexpected end"},
		"not an answer":    {[]byte(`{"hello": "world"}`), "no verified_by"},
		"unsigned":         {edit(t, raw, func(m map[string]any) { delete(m, "resolver_sig") }), "not signed"},
		"unknown resolver": {edit(t, raw, func(m map[string]any) { m["verified_by"] = "elsewhere.test" }), "resolver.json"},
		"unknown record":   {edit(t, raw, func(m map[string]any) { m["record"] = "sr:sha256:" + strings.Repeat("0", 64) }), "record sr:sha256:000"},
		"wrong cite": {edit(t, raw, func(m map[string]any) {
			ch := m["chunks"].([]any)
			ch[0].(map[string]any)["cite"] = ch[1].(map[string]any)["cite"]
		}), "does not point to chunk"},
	} {
		t.Run(name, func(t *testing.T) {
			res := check.Answer(context.Background(), f, tt.raw)
			if res.OK() {
				t.Fatal("check passed")
			}
			found := false
			for _, fd := range res.Findings {
				found = found || strings.Contains(fd.Message, tt.substr)
			}
			if !found {
				t.Fatalf("no finding containing %q: %+v", tt.substr, res.Findings)
			}
		})
	}
}

func TestAnswerLocalAndLookup(t *testing.T) {
	_, c, f := resolverNet(t)
	_, raw, err := c.FetchAnswer(context.Background(), resolver.FetchRequest{URL: bridge})
	if err != nil {
		t.Fatal(err)
	}
	local := edit(t, raw, func(m map[string]any) { m["verified_by"] = "local"; delete(m, "resolver_sig") })
	res := check.Answer(context.Background(), f, local)
	if !res.OK() || len(res.Findings) != 1 || res.Findings[0].Severity != check.SeverityWarning {
		t.Fatalf("local answer: %+v", res.Findings)
	}

	l, err := c.Lookup(context.Background(), "The temporary ferry service that ran during the works stopped on Sunday evening.")
	if err != nil || !l.Found {
		t.Fatalf("lookup: %+v, %v", l, err)
	}
	lraw, _ := json.Marshal(l)
	lraw = edit(t, lraw, func(m map[string]any) { delete(m, "resolver_sig") }) // re-encoding changed the bytes; check hashes only
	lraw = edit(t, lraw, func(m map[string]any) { m["verified_by"] = "local" })
	if res := check.Answer(context.Background(), f, lraw); !res.OK() {
		t.Fatalf("lookup answer: %+v", res.Findings)
	}
	bad := edit(t, lraw, func(m map[string]any) {
		m["matches"].([]any)[0].(map[string]any)["text"] = "changed"
	})
	if res := check.Answer(context.Background(), f, bad); res.OK() {
		t.Fatal("altered match text passed")
	}
}
