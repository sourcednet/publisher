package publisher_test

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/publisher"
	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/testkit/testsite"
)

const domain = "example-library.test"

func at(hours int) time.Time { return testsite.T0.Add(time.Duration(hours) * time.Hour) }

// mustCheck runs the checker on the site's web root and fails on any error.
func mustCheck(t *testing.T, s *testsite.Site) *check.Result {
	t.Helper()
	res := check.Publisher(context.Background(), check.DirFetcher{Root: s.Root(), Publisher: domain}, domain, check.Options{})
	if !res.OK() {
		t.Fatalf("check failed: %+v", res.Findings)
	}
	return res
}

func action(t *testing.T, rep *publisher.Report, url string) publisher.Action {
	t.Helper()
	for _, p := range rep.Pages {
		if p.URL == url {
			return p.Action
		}
	}
	t.Fatalf("%s not in report", url)
	return ""
}

func linkTo(id string) string {
	h, _ := core.IDHex(id, core.RecordIDPrefix)
	return `<link rel="sourced-record" href="/.well-known/sourced/records/` + h + `.json">`
}

func newSite(t *testing.T) *testsite.Site {
	s := testsite.New(t, domain)
	s.WritePage("guides/book-care.html", "Caring for old books",
		testsite.Para("book care", 400),
		"## Humidity", testsite.Para("humidity", 400),
		"## Light", testsite.Para("light", 400))
	s.WritePage("guides/digitization.html", "Digitizing a collection", testsite.Para("digitization", 500))
	s.WriteMarkdown("notes/reading-room.md", "# Reading room\n\n"+testsite.Para("reading room", 350))
	return s
}

func TestBuildNewPages(t *testing.T) {
	s := newSite(t)
	rep := s.Build(at(0), nil)
	if rep.Count(publisher.ActionNew) != 3 || !rep.ManifestWritten {
		t.Fatalf("want 3 new pages and a manifest, got %+v", rep)
	}
	care := s.Current(s.URL("/guides/book-care.html"))
	if care.Title != "Caring for old books" || care.Language != "en" || len(care.Chunks) != 3 {
		t.Fatalf("unexpected record: %+v", care)
	}
	if want := []string{"Caring for old books", "Humidity"}; strings.Join(care.Chunks[1].Section, "/") != strings.Join(want, "/") {
		t.Fatalf("section %q, want %q", care.Chunks[1].Section, want)
	}
	if page := s.Read("guides/book-care.html"); !strings.Contains(page, linkTo(care.ID)) {
		t.Fatalf("page lacks its record link:\n%s", page)
	}
	md := s.Current(s.URL("/notes/reading-room"))
	if md.Title != "Reading room" {
		t.Fatalf("markdown title %q", md.Title)
	}

	res := mustCheck(t, s)
	if res.Pages != 3 || res.Records != 3 {
		t.Fatalf("check saw %d pages, %d records", res.Pages, res.Records)
	}
}

func TestBuildUnchangedWritesNothing(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	before := s.Read(".well-known/sourced/manifest.json")
	rep := s.Build(at(1), nil)
	if rep.Count(publisher.ActionUnchanged) != 3 || rep.ManifestWritten {
		t.Fatalf("want everything unchanged, got %+v", rep)
	}
	if s.Read(".well-known/sourced/manifest.json") != before {
		t.Fatal("manifest rewritten without changes")
	}
}

func TestBuildRevisionAndCorrection(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	url := s.URL("/guides/book-care.html")
	v1 := s.Current(url)

	// An undeclared change is a revision; untouched paragraphs keep their chunk IDs.
	s.WritePage("guides/book-care.html", "Caring for old books",
		testsite.Para("book care", 400),
		"## Humidity", testsite.Para("humidity", 400)+" A typo fixed.",
		"## Light", testsite.Para("light", 400))
	rep := s.Build(at(1), nil)
	if a := action(t, rep, url); a != publisher.Action(core.ChangeRevision) {
		t.Fatalf("action %s, want revision", a)
	}
	v2 := s.Current(url)
	if v2.Supersedes != v1.ID || v2.Chunks[0].ID != v1.Chunks[0].ID || v2.Chunks[1].ID == v1.Chunks[1].ID {
		t.Fatalf("unexpected v2: %+v", v2)
	}

	// A declared correction carries its note.
	s.WritePage("guides/book-care.html", "Caring for old books",
		testsite.Para("book care", 400),
		"## Humidity", testsite.Para("corrected humidity", 400),
		"## Light", testsite.Para("light", 400))
	note := "Corrected the recommended humidity."
	s.Build(at(2), map[string]publisher.Declaration{"guides/book-care.html": {Change: core.ChangeCorrection, Note: note}})
	v3 := s.Current(url)
	if v3.Change != core.ChangeCorrection || v3.Note != note || v3.Supersedes != v2.ID {
		t.Fatalf("unexpected v3: %+v", v3)
	}

	st, _, err := core.ResolveState(v1.ID, v3, func(id string) (*core.Record, error) { return s.Record(id), nil })
	if err != nil || st != core.StateCorrected {
		t.Fatalf("v1 state %s (%v), want corrected", st, err)
	}
	if page := s.Read("guides/book-care.html"); !strings.Contains(page, linkTo(v3.ID)) || strings.Count(page, "sourced-record") != 1 {
		t.Fatalf("page should link only to v3:\n%s", page)
	}
	mustCheck(t, s)
}

func TestBuildRejectsMisplacedDeclarations(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	for name, decl := range map[string]map[string]publisher.Declaration{
		"unchanged page": {"guides/digitization.html": {Change: core.ChangeCorrection, Note: "n"}},
		"unknown page":   {"guides/nope.html": {Change: core.ChangeCorrection, Note: "n"}},
		"other domain":   {"https://elsewhere.test/x": {Change: core.ChangeRevision}},
	} {
		if _, err := publisher.Build(s.Config, publisher.BuildOptions{Now: at(1), Declare: decl}); err == nil {
			t.Errorf("%s: build succeeded, want an error", name)
		}
	}

	s.WritePage("guides/new.html", "New", testsite.Para("new page", 400))
	decl := map[string]publisher.Declaration{"guides/new.html": {Change: core.ChangeRevision}}
	if _, err := publisher.Build(s.Config, publisher.BuildOptions{Now: at(1), Declare: decl}); err == nil {
		t.Error("declaring a change on a new page should fail")
	}
}

func TestBuildWithdrawal(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	url := s.URL("/guides/digitization.html")
	v1 := s.Current(url)
	s.WritePage("guides/digitization.html", "Digitizing a collection", testsite.Para("digitization", 500)+" Updated.")
	s.Build(at(1), nil)
	v2 := s.Current(url)

	rep := s.Build(at(2), map[string]publisher.Declaration{url: {Change: core.ChangeWithdrawal, Note: "Legal request."}})
	if a := action(t, rep, url); a != publisher.Action(core.ChangeWithdrawal) {
		t.Fatalf("action %s, want withdrawal", a)
	}
	w := s.Current(url)
	if w.Change != core.ChangeWithdrawal || len(w.Chunks) != 0 || w.Supersedes != v2.ID {
		t.Fatalf("unexpected withdrawal record: %+v", w)
	}
	for _, id := range []string{v1.ID, v2.ID} {
		p, _ := s.Config.Tree().BundlePath(id)
		if _, err := os.Stat(p); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("bundle %s still exists after withdrawal", id)
		}
	}
	if strings.Contains(s.Read("guides/digitization.html"), "sourced-record") {
		t.Fatal("withdrawn page still links to a record")
	}
	mustCheck(t, s)

	// The page file is still there, but a withdrawn page is never silently republished.
	rep = s.Build(at(3), nil)
	if a := action(t, rep, url); a != publisher.ActionSkipped || len(rep.Warnings) == 0 {
		t.Fatalf("action %s, warnings %v; want skipped with a warning", a, rep.Warnings)
	}
	if s.Current(url).ID != w.ID {
		t.Fatal("withdrawn page was republished")
	}
}

func TestBuildMissingPage(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	url := s.URL("/notes/reading-room")
	id := s.Current(url).ID

	s.Remove("notes/reading-room.md")
	rep := s.Build(at(1), nil)
	if a := action(t, rep, url); a != publisher.ActionMissing || len(rep.Warnings) != 1 {
		t.Fatalf("action %s, warnings %v; want missing with one warning", a, rep.Warnings)
	}
	if s.Current(url).ID != id {
		t.Fatal("a missing page must stay published until withdrawn")
	}

	s.Build(at(2), map[string]publisher.Declaration{url: {Change: core.ChangeWithdrawal}})
	if s.Current(url).Change != core.ChangeWithdrawal {
		t.Fatal("missing page not withdrawn")
	}
	if rep := s.Build(at(3), nil); len(rep.Warnings) != 0 {
		t.Fatalf("withdrawn pages should not warn as missing: %v", rep.Warnings)
	}
	mustCheck(t, s)
}

func TestManifestTimeNeverGoesBackwards(t *testing.T) {
	s := newSite(t)
	s.Build(at(5), nil)
	s.WritePage("guides/digitization.html", "Digitizing a collection", testsite.Para("digitization", 500)+" Later edit.")
	s.Build(at(1), nil) // clock went backwards

	m, err := s.Config.Tree().ReadManifest(s.KeySet())
	if err != nil {
		t.Fatal(err)
	}
	if !m.GeneratedAt.After(at(5)) {
		t.Fatalf("manifest generated_at %s went backwards", m.GeneratedAt)
	}
}

func TestKeyRotationAndRevocation(t *testing.T) {
	s := newSite(t)
	s.Build(at(0), nil)
	care := s.URL("/guides/book-care.html")
	oldKey := s.Config.SigningKey
	v1 := s.Current(care)

	newKey, err := publisher.AddKey(s.Config, at(1))
	if err != nil {
		t.Fatal(err)
	}
	if newKey == oldKey || s.Config.SigningKey != newKey {
		t.Fatalf("new key %s, old %s, config %s", newKey, oldKey, s.Config.SigningKey)
	}
	reloaded, err := publisher.LoadConfig(s.Dir)
	if err != nil || reloaded.SigningKey != newKey {
		t.Fatalf("config not saved: %v", err)
	}

	s.WritePage("guides/book-care.html", "Caring for old books", testsite.Para("book care, second edition", 400))
	s.Build(at(2), nil)
	if v2 := s.Current(care); v2.Sig.KeyID != newKey {
		t.Fatalf("new record signed with %s, want %s", v2.Sig.KeyID, newKey)
	}
	mustCheck(t, s) // old records still verify with the retired key

	if _, err := publisher.RevokeKey(s.Config, newKey); err == nil {
		t.Fatal("revoking the signing key should fail")
	}
	n, err := publisher.RevokeKey(s.Config, oldKey)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("re-signed %d records, want the 3 signed by the old key", n)
	}
	if r := s.Record(v1.ID); r.Sig.KeyID != newKey {
		t.Fatalf("history record still signed with %s", r.Sig.KeyID)
	}
	for _, k := range s.KeySet().Keys {
		if k.ID == oldKey && k.Status != core.KeyRevoked {
			t.Fatalf("old key status %s", k.Status)
		}
	}
	mustCheck(t, s) // same IDs, new signatures, nothing broken
}

func TestConfigKeepsKeysOutOfWebRoot(t *testing.T) {
	c := publisher.DefaultConfig("example.test", "public")
	c.KeysDir = "public/.keys"
	if err := c.Save(t.TempDir()); err == nil {
		t.Fatal("a keys_dir inside the web root must be rejected")
	}
}

func TestInitTwiceFails(t *testing.T) {
	s := testsite.New(t, domain)
	if _, err := publisher.Init(s.Dir, domain, "public", "", testsite.T0); err == nil {
		t.Fatal("second init should fail")
	}
}

// TestSignPagesFromAnotherFolder signs a site generator's output (dist)
// into the folder it publishes as is (public), as a site signing on its
// author's machine would.
func TestSignPagesFromAnotherFolder(t *testing.T) {
	dir := t.TempDir()
	c, err := publisher.Init(dir, domain, "public", "dist", testsite.T0)
	if err != nil {
		t.Fatal(err)
	}
	page := `<html><head><title>Guide</title></head><body><main><h1>Guide</h1><p>Old books last for centuries when stored with care.</p></main></body></html>`
	if err := os.MkdirAll(dir+"/dist/guides", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dir+"/dist/guides/care.html", []byte(page), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := publisher.Build(c, publisher.BuildOptions{Now: testsite.T0}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(dir + "/public/.well-known/sourced/manifest.json"); err != nil {
		t.Fatalf("signed files not in the web root: %v", err)
	}
	if _, err := os.Stat(dir + "/dist/.well-known"); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("signed files written to the pages folder: %v", err)
	}
	f := check.DirFetcher{Root: c.RootDir(), Publisher: domain, Pages: c.PagesDir()}
	if res := check.Publisher(context.Background(), f, domain, check.Options{}); !res.OK() || res.Pages != 1 {
		t.Fatalf("check: %d pages, %+v", res.Pages, res.Findings)
	}

	if _, err := publisher.Init(t.TempDir(), domain, "public", ".sourced", testsite.T0); err == nil || !strings.Contains(err.Error(), "inside the pages folder") {
		t.Fatalf("keys inside the pages folder: %v", err)
	}
}
