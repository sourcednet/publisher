// Package check validates what a publisher serves: keys, manifest, every
// record and its history, bundles, and each page's sourced-record link. It
// works the same against a local web root or a live domain.
package check

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

// ErrNotFound means the URL does not exist.
var ErrNotFound = errors.New("not found")

// maxBody caps how much of any response is read.
const maxBody = 16 << 20

// Fetcher gets the body and headers at an https URL.
type Fetcher interface {
	Fetch(ctx context.Context, rawURL string) ([]byte, http.Header, error)
}

// HTTPFetcher fetches over HTTPS. Plain HTTP is always refused.
type HTTPFetcher struct {
	Client *http.Client
}

// Fetch implements Fetcher.
func (f HTTPFetcher) Fetch(ctx context.Context, rawURL string) ([]byte, http.Header, error) {
	if !strings.HasPrefix(rawURL, "https://") {
		return nil, nil, fmt.Errorf("refusing non-https URL %s", rawURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, nil, err
	}
	client := f.Client
	if client == nil {
		client = http.DefaultClient
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone {
		return nil, resp.Header, ErrNotFound
	}
	if resp.StatusCode != http.StatusOK {
		return nil, resp.Header, fmt.Errorf("GET %s: %s", rawURL, resp.Status)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	return b, resp.Header, err
}

// DirFetcher serves a publisher's URLs from a local web root, the way a
// static host would: a trailing slash maps to index.html, and a path without
// an extension also tries .html and /index.html.
type DirFetcher struct {
	Root      string
	Publisher string
	// Pages, if set, is where pages are; Root still holds
	// /.well-known/sourced/ (see the publisher's pages setting).
	Pages string
}

// Fetch implements Fetcher.
func (f DirFetcher) Fetch(_ context.Context, rawURL string) ([]byte, http.Header, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, nil, err
	}
	if u.Scheme != "https" || !strings.EqualFold(u.Hostname(), f.Publisher) {
		return nil, nil, fmt.Errorf("%s is not on %s", rawURL, f.Publisher)
	}
	p := path.Clean("/" + u.Path)
	var candidates []string
	switch {
	case strings.HasSuffix(u.Path, "/"):
		candidates = []string{path.Join(p, "index.html")}
	case path.Ext(p) == "":
		candidates = []string{p, p + ".html", path.Join(p, "index.html")}
	default:
		candidates = []string{p}
	}
	root := f.Root
	if f.Pages != "" && !strings.HasPrefix(p, "/.well-known/") {
		root = f.Pages
	}
	for _, c := range candidates {
		full := filepath.Join(root, filepath.FromSlash(c))
		fi, err := os.Stat(full)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && fi.IsDir()) {
			continue
		}
		if err != nil {
			return nil, nil, err
		}
		b, err := os.ReadFile(full)
		if err != nil {
			return nil, nil, err
		}
		return b, http.Header{}, nil
	}
	return nil, nil, ErrNotFound
}

// HTTPClient returns a client with a request timeout. If caFile is set, its
// PEM certificates are trusted in addition to the system roots: a dev-only
// setting for local test networks with their own certificate authority.
func HTTPClient(caFile string, timeout time.Duration) (*http.Client, error) {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, err
		}
		pool, err := x509.SystemCertPool()
		if err != nil {
			pool = x509.NewCertPool()
		}
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("%s: no PEM certificates found", caFile)
		}
		tr.TLSClientConfig = &tls.Config{RootCAs: pool}
	}
	return &http.Client{Transport: tr, Timeout: timeout}, nil
}
