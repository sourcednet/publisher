package check_test

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/publisher"
	"github.com/sourcednet/publisher/check"
	"github.com/sourcednet/testkit/testsite"
)

const domain = "example-library.test"

// builtSite returns a site with two pages, one of them revised once.
func builtSite(t *testing.T) *testsite.Site {
	t.Helper()
	s := testsite.New(t, domain)
	s.WritePage("guides/book-care.html", "Caring for old books", testsite.Para("book care", 400), "## Humidity", testsite.Para("humidity", 400))
	s.WritePage("index.html", "Example Library", testsite.Para("welcome", 350))
	s.Build(testsite.T0, nil)
	s.WritePage("guides/book-care.html", "Caring for old books", testsite.Para("book care", 400), "## Humidity", testsite.Para("revised humidity", 400))
	s.Build(testsite.T0.Add(time.Hour), nil)
	return s
}

func runDir(s *testsite.Site) *check.Result {
	return check.Publisher(context.Background(), check.DirFetcher{Root: s.Root(), Publisher: domain}, domain, check.Options{})
}

// wantFinding fails unless res has a finding of the given severity whose
// message contains substr.
func wantFinding(t *testing.T, res *check.Result, sev check.Severity, substr string) {
	t.Helper()
	for _, f := range res.Findings {
		if f.Severity == sev && strings.Contains(f.Message, substr) {
			return
		}
	}
	t.Fatalf("no %s finding containing %q in %+v", sev, substr, res.Findings)
}

func TestCheckCleanSite(t *testing.T) {
	res := runDir(builtSite(t))
	if !res.OK() || len(res.Findings) != 0 {
		t.Fatalf("want a clean result, got %+v", res.Findings)
	}
	if res.Pages != 2 || res.Records != 3 {
		t.Fatalf("got %d pages, %d records; want 2 and 3", res.Pages, res.Records)
	}
}

func TestCheckFindsProblems(t *testing.T) {
	tests := []struct {
		name   string
		breakf func(t *testing.T, s *testsite.Site)
		sev    check.Severity
		substr string
	}{
		{"tampered bundle", func(t *testing.T, s *testsite.Site) {
			cur := s.Current(s.URL("/guides/book-care.html"))
			p := s.BundlePath(cur.ID)
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), "humidity", "HUMIDITY", 1)), 0o644)
		}, check.SeverityError, "does not match its id"},

		{"missing history record", func(t *testing.T, s *testsite.Site) {
			cur := s.Current(s.URL("/guides/book-care.html"))
			os.Remove(s.RecordPath(cur.Supersedes))
		}, check.SeverityError, "history record"},

		{"missing old bundle", func(t *testing.T, s *testsite.Site) {
			cur := s.Current(s.URL("/guides/book-care.html"))
			os.Remove(s.BundlePath(cur.Supersedes))
		}, check.SeverityWarning, "bundle of old version"},

		{"missing current bundle", func(t *testing.T, s *testsite.Site) {
			cur := s.Current(s.URL("/guides/book-care.html"))
			os.Remove(s.BundlePath(cur.ID))
		}, check.SeverityError, "not found"},

		{"page links to old record", func(t *testing.T, s *testsite.Site) {
			cur := s.Current(s.URL("/guides/book-care.html"))
			oldHex, _ := core.IDHex(cur.Supersedes, core.RecordIDPrefix)
			curHex, _ := core.IDHex(cur.ID, core.RecordIDPrefix)
			page := strings.Replace(s.Read("guides/book-care.html"), curHex, oldHex, 1)
			os.WriteFile(filepath.Join(s.Root(), "guides", "book-care.html"), []byte(page), 0o644)
		}, check.SeverityError, "current record is"},

		{"page without link", func(t *testing.T, s *testsite.Site) {
			page := s.Read("index.html")
			i := strings.Index(page, "<link rel=\"sourced-record\"")
			j := strings.Index(page[i:], "\n")
			os.WriteFile(filepath.Join(s.Root(), "index.html"), []byte(page[:i]+page[i+j+1:]), 0o644)
		}, check.SeverityError, "has no <link"},

		{"tampered manifest", func(t *testing.T, s *testsite.Site) {
			p := s.Config.Tree().ManifestPath()
			b, _ := os.ReadFile(p)
			os.WriteFile(p, []byte(strings.Replace(string(b), "book-care", "book-kare", 1)), 0o644)
		}, check.SeverityError, "signature does not verify"},

		{"withdrawn bundle still served", func(t *testing.T, s *testsite.Site) {
			url := s.URL("/")
			cur := s.Current(url)
			bundle, _ := os.ReadFile(s.BundlePath(cur.ID))
			s.Build(testsite.T0.Add(2*time.Hour), map[string]publisher.Declaration{url: {Change: core.ChangeWithdrawal}})
			os.WriteFile(s.BundlePath(cur.ID), bundle, 0o644)
		}, check.SeverityError, "withdrawn record"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s := builtSite(t)
			tt.breakf(t, s)
			res := runDir(s)
			wantFinding(t, res, tt.sev, tt.substr)
			if tt.sev == check.SeverityWarning && !res.OK() {
				t.Fatalf("a warning alone must not fail the check: %+v", res.Findings)
			}
		})
	}
}

// httpsTo returns a fetcher whose client sends every host to srv, trusting
// its test certificate.
func httpsTo(srv *httptest.Server) check.HTTPFetcher {
	tr := srv.Client().Transport.(*http.Transport).Clone()
	tr.DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, srv.Listener.Addr().String())
	}
	tr.TLSClientConfig = tr.TLSClientConfig.Clone()
	tr.TLSClientConfig.ServerName = "example.com" // the name in httptest's certificate
	return check.HTTPFetcher{Client: &http.Client{Transport: tr}}
}

func TestCheckLiveDomain(t *testing.T) {
	s := builtSite(t)
	withHeader := false
	files := http.FileServer(http.Dir(s.Root()))
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if withHeader && strings.HasSuffix(r.URL.Path, ".html") || withHeader && r.URL.Path == "/" {
			w.Header().Set("Link", `</.well-known/sourced/records/x.json>; rel="sourced-record"`)
		}
		files.ServeHTTP(w, r)
	}))
	defer srv.Close()
	opts := check.Options{PagesRequired: true, LinkHeader: true}

	res := check.Publisher(context.Background(), httpsTo(srv), domain, opts)
	if !res.OK() {
		t.Fatalf("live check failed: %+v", res.Findings)
	}
	wantFinding(t, res, check.SeverityWarning, "Link header")

	withHeader = true
	res = check.Publisher(context.Background(), httpsTo(srv), domain, opts)
	if !res.OK() || len(res.Findings) != 0 {
		t.Fatalf("want a clean live result, got %+v", res.Findings)
	}
}

func TestHTTPFetcherRefusesPlainHTTP(t *testing.T) {
	if _, _, err := (check.HTTPFetcher{}).Fetch(context.Background(), "http://example.test/"); err == nil {
		t.Fatal("plain HTTP must be refused")
	}
}

func TestDirFetcherStaysInRoot(t *testing.T) {
	s := builtSite(t)
	f := check.DirFetcher{Root: s.Root(), Publisher: domain}
	if _, _, err := f.Fetch(context.Background(), "https://"+domain+"/../sourced.json"); err == nil {
		t.Fatal("path escaped the web root")
	}
	if _, _, err := f.Fetch(context.Background(), "https://other.test/index.html"); err == nil {
		t.Fatal("fetched a URL on another domain")
	}
	if _, _, err := f.Fetch(context.Background(), "https://"+domain+"/"); err != nil {
		t.Fatalf("index page: %v", err)
	}
}
