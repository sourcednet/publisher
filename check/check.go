package check

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strings"
	"time"

	"github.com/sourcednet/core"
	"github.com/sourcednet/core/htmllink"
)

// Severity of a finding.
type Severity string

const (
	SeverityError   Severity = "error"
	SeverityWarning Severity = "warning"
)

// Finding is one problem found.
type Finding struct {
	Severity Severity    `json:"severity"`
	Subject  string      `json:"subject"`
	Reason   core.Reason `json:"reason,omitempty"`
	Message  string      `json:"message"`
}

// Result is the outcome of checking a publisher.
type Result struct {
	Publisher string    `json:"publisher"`
	Pages     int       `json:"pages"`
	Records   int       `json:"records"`
	Findings  []Finding `json:"findings"`
}

// OK reports whether there are no errors. Warnings don't count.
func (r *Result) OK() bool {
	for _, f := range r.Findings {
		if f.Severity == SeverityError {
			return false
		}
	}
	return true
}

// Options tune a check.
type Options struct {
	// PagesRequired makes a page that can't be fetched an error rather than
	// a warning. Set it for live domains; a local web root built from
	// Markdown has no HTML pages to fetch.
	PagesRequired bool
	// LinkHeader warns when pages don't send the recommended Link header.
	// Only meaningful over HTTP.
	LinkHeader bool
}

type checker struct {
	ctx       context.Context
	f         Fetcher
	opts      Options
	publisher string
	ks        *core.KeySet
	res       *Result
	verified  map[string]*core.Record
}

// Publisher checks everything a publisher serves.
func Publisher(ctx context.Context, f Fetcher, publisher string, opts Options) *Result {
	c := &checker{
		ctx:       ctx,
		f:         f,
		opts:      opts,
		publisher: strings.ToLower(publisher),
		res:       &Result{Publisher: strings.ToLower(publisher), Findings: []Finding{}},
		verified:  map[string]*core.Record{},
	}
	c.run()
	return c.res
}

func (c *checker) add(sev Severity, subject string, err error) {
	c.res.Findings = append(c.res.Findings, Finding{Severity: sev, Subject: subject, Reason: core.ReasonOf(err), Message: err.Error()})
}

func (c *checker) run() {
	raw, _, err := c.f.Fetch(c.ctx, core.KeysURL(c.publisher))
	if err != nil {
		c.add(SeverityError, "keys.json", err)
		return
	}
	if c.ks, err = core.ParseKeySet(raw, c.publisher); err != nil {
		c.add(SeverityError, "keys.json", err)
		return
	}
	if !slices.ContainsFunc(c.ks.Keys, func(k core.Key) bool { return k.Status == core.KeyActive }) {
		c.add(SeverityWarning, "keys.json", errors.New("no active key; nothing new can be signed"))
	}

	raw, _, err = c.f.Fetch(c.ctx, core.ManifestURL(c.publisher))
	if err != nil {
		c.add(SeverityError, "manifest.json", err)
		return
	}
	m, err := core.VerifyManifest(raw, c.ks, c.publisher, time.Time{})
	if err != nil {
		c.add(SeverityError, "manifest.json", err)
		return
	}
	if m.Shards != "" {
		c.add(SeverityError, "manifest.json", errors.New("sharded manifests are not supported by this checker yet"))
		return
	}

	for _, u := range slices.Sorted(maps.Keys(m.Entries)) {
		c.res.Pages++
		c.entry(u, m.Entries[u])
	}
	c.res.Records = len(c.verified)
}

// entry checks one manifest entry: the current record, its history, the
// bundles that should and shouldn't exist, and the page's link.
func (c *checker) entry(pageURL, currentID string) {
	cur, err := c.record(currentID)
	if err != nil {
		c.add(SeverityError, pageURL, err)
		return
	}
	if cur.URL != pageURL {
		c.add(SeverityWarning, pageURL, fmt.Errorf("manifest entry points to a record for %s (moved page?)", cur.URL))
	}

	withdrawn := cur.Change == core.ChangeWithdrawal
	seen := map[string]bool{}
	for r := cur; ; {
		seen[r.ID] = true
		c.bundle(pageURL, r, withdrawn, r == cur)
		if r.Supersedes == "" {
			break
		}
		if seen[r.Supersedes] {
			c.add(SeverityError, pageURL, fmt.Errorf("supersedes chain loops at %s", r.Supersedes))
			break
		}
		prev, err := c.record(r.Supersedes)
		if err != nil {
			c.add(SeverityError, pageURL, fmt.Errorf("history record %s: %w", r.Supersedes, err))
			break
		}
		r = prev
	}

	c.link(pageURL, cur, withdrawn)
}

func (c *checker) record(id string) (*core.Record, error) {
	if r, ok := c.verified[id]; ok {
		return r, nil
	}
	u, err := core.RecordURL(c.publisher, id)
	if err != nil {
		return nil, err
	}
	raw, _, err := c.f.Fetch(c.ctx, u)
	if err != nil {
		return nil, fmt.Errorf("record %s: %w", id, err)
	}
	r, err := core.VerifyRecord(raw, c.ks, c.publisher)
	if err != nil {
		return nil, err
	}
	if r.ID != id {
		return nil, fmt.Errorf("file for %s holds record %s", id, r.ID)
	}
	c.verified[id] = r
	return r, nil
}

// bundle checks a record's bundle. Bundles of withdrawn pages must be gone.
// Old versions should keep theirs; the current version must have one.
func (c *checker) bundle(pageURL string, r *core.Record, withdrawn, current bool) {
	if r.Change == core.ChangeWithdrawal {
		return
	}
	u, err := core.BundleURL(c.publisher, r.ID)
	if err != nil {
		c.add(SeverityError, pageURL, err)
		return
	}
	raw, _, err := c.f.Fetch(c.ctx, u)
	switch {
	case withdrawn && errors.Is(err, ErrNotFound):
		return
	case withdrawn && err == nil:
		c.add(SeverityError, pageURL, fmt.Errorf("bundle of withdrawn record %s is still served", r.ID))
		return
	case errors.Is(err, ErrNotFound) && !current:
		c.add(SeverityWarning, pageURL, fmt.Errorf("bundle of old version %s is missing; apps can't show what changed", r.ID))
		return
	case err != nil:
		c.add(SeverityError, pageURL, fmt.Errorf("bundle %s: %w", r.ID, err))
		return
	}
	var b core.Bundle
	if err := json.Unmarshal(raw, &b); err != nil {
		c.add(SeverityError, pageURL, fmt.Errorf("bundle %s: %w", r.ID, err))
		return
	}
	if err := core.VerifyBundle(r, &b); err != nil {
		c.add(SeverityError, pageURL, err)
	}
}

// link checks that the page links to its current record, or no longer links
// anywhere if it was withdrawn.
func (c *checker) link(pageURL string, cur *core.Record, withdrawn bool) {
	body, hdr, err := c.f.Fetch(c.ctx, pageURL)
	if errors.Is(err, ErrNotFound) {
		if !withdrawn {
			sev := SeverityWarning
			if c.opts.PagesRequired {
				sev = SeverityError
			}
			c.add(sev, pageURL, errors.New("page not found, so its sourced-record link can't be checked"))
		}
		return
	}
	if err != nil {
		c.add(SeverityError, pageURL, err)
		return
	}
	want, _ := core.RecordURL(c.publisher, cur.ID)
	got := htmllink.FromHTML(body, pageURL)
	switch {
	case withdrawn && got != "":
		c.add(SeverityError, pageURL, errors.New("withdrawn page still links to a record"))
	case withdrawn:
	case got == "":
		c.add(SeverityError, pageURL, errors.New(`page has no <link rel="sourced-record">`))
	case got != want:
		c.add(SeverityError, pageURL, fmt.Errorf("page links to %s, current record is %s", got, want))
	}
	if c.opts.LinkHeader && !withdrawn && htmllink.FromHeader(hdr, pageURL) == "" {
		c.add(SeverityWarning, pageURL, errors.New("no sourced-record Link header; clients need a GET instead of a HEAD"))
	}
}
