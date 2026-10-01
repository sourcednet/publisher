package publisher

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/sourcednet/core"
)

// Declaration is a change the publisher declares for a page on this build.
type Declaration struct {
	Change core.Change
	Note   string
}

// BuildOptions control one build.
type BuildOptions struct {
	// Now is the publication time of new records and the manifest.
	// Zero means the current time.
	Now time.Time
	// Declare maps pages, as URLs or paths relative to the web root, to the
	// change they carry. Changed pages without a declaration are revisions.
	Declare map[string]Declaration
}

// Action is what a build did with a page.
type Action string

const (
	ActionNew       Action = "new"
	ActionUnchanged Action = "unchanged"
	ActionMissing   Action = "missing"
	ActionSkipped   Action = "skipped"
)

// PageResult reports one page.
type PageResult struct {
	URL    string `json:"url"`
	File   string `json:"file,omitempty"`
	Action Action `json:"action"` // new, unchanged, missing, skipped, or a change type
	Record string `json:"record,omitempty"`
}

// Report summarizes a build.
type Report struct {
	Pages           []PageResult `json:"pages"`
	Warnings        []string     `json:"warnings,omitempty"`
	ManifestWritten bool         `json:"manifest_written"`
}

// Count returns how many pages got the given action.
func (r *Report) Count(a Action) int {
	n := 0
	for _, p := range r.Pages {
		if p.Action == a {
			n++
		}
	}
	return n
}

type builder struct {
	c       *Config
	tree    Tree
	ks      *core.KeySet
	keyID   string
	priv    ed25519.PrivateKey
	now     time.Time
	entries map[string]string
	report  *Report
	changed bool
}

// Build brings the publisher tree in line with the pages in the web root:
// new pages get a first record, changed pages a successor, and declared
// withdrawals a withdrawal record. The manifest is rewritten only if
// something changed, and every HTML page gets its sourced-record link.
func Build(c *Config, opts BuildOptions) (*Report, error) {
	b := &builder{c: c, tree: c.Tree(), now: opts.Now, report: &Report{}, keyID: c.SigningKey}
	if b.now.IsZero() {
		b.now = time.Now()
	}
	b.now = b.now.UTC().Truncate(time.Second)

	var err error
	if b.ks, err = b.tree.ReadKeySet(); err != nil {
		return nil, fmt.Errorf("keys.json: %w", err)
	}
	if !keyActive(b.ks, c.SigningKey) {
		return nil, fmt.Errorf("signing key %q is not an active key in keys.json", c.SigningKey)
	}
	priv, err := readPrivateKey(c.KeysPath(), c.SigningKey)
	if err != nil {
		return nil, err
	}
	b.priv = priv
	man, err := b.tree.ReadManifest(b.ks)
	if err != nil {
		return nil, fmt.Errorf("manifest.json: %w", err)
	}
	b.entries = map[string]string{}
	if man != nil {
		for u, id := range man.Entries {
			b.entries[u] = id
		}
	}

	declared := map[string]Declaration{}
	for ref, d := range opts.Declare {
		u, err := resolvePageRef(c, ref)
		if err != nil {
			return nil, err
		}
		declared[u] = d
	}

	files, err := discoverPages(c)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, rel := range files {
		p, err := loadPage(c, rel)
		if err != nil {
			return nil, err
		}
		seen[p.URL] = true
		d, isDeclared := declared[p.URL]
		delete(declared, p.URL)
		if err := b.page(p, d, isDeclared); err != nil {
			return nil, fmt.Errorf("%s: %w", rel, err)
		}
	}

	// Declarations for pages no longer in the web root: only withdrawals make sense.
	for _, u := range slices.Sorted(maps.Keys(declared)) {
		d := declared[u]
		if d.Change != core.ChangeWithdrawal {
			return nil, fmt.Errorf("%s: declared a %s, but the page is not in the web root", u, d.Change)
		}
		if err := b.withdraw(u, "", d.Note); err != nil {
			return nil, fmt.Errorf("%s: %w", u, err)
		}
		seen[u] = true
	}

	for _, u := range slices.Sorted(maps.Keys(b.entries)) {
		if seen[u] {
			continue
		}
		if cur, err := b.tree.ReadRecord(b.ks, b.entries[u]); err == nil && cur.Change == core.ChangeWithdrawal {
			continue
		}
		b.report.Pages = append(b.report.Pages, PageResult{URL: u, Action: ActionMissing, Record: b.entries[u]})
		b.report.Warnings = append(b.report.Warnings,
			fmt.Sprintf("%s is in the manifest but not in the web root; it stays published until you withdraw it", u))
	}

	if b.changed {
		if err := b.writeManifest(man); err != nil {
			return nil, err
		}
		b.report.ManifestWritten = true
	}
	return b.report, nil
}

func (b *builder) page(p *page, d Declaration, isDeclared bool) error {
	if d.Change == core.ChangeWithdrawal {
		return b.withdraw(p.URL, p.File, d.Note)
	}
	result := PageResult{URL: p.URL, File: p.File}

	chunks, err := core.ChunkMarkdown(p.Markdown, b.c.Chunking)
	if err != nil {
		return err
	}
	if len(chunks) == 0 {
		b.report.Warnings = append(b.report.Warnings, fmt.Sprintf("%s has no content; skipped", p.File))
		result.Action = ActionSkipped
		b.report.Pages = append(b.report.Pages, result)
		return nil
	}
	meta := core.PageMeta{
		Publisher:   b.c.Publisher,
		URL:         p.URL,
		Title:       p.Title,
		PublishedAt: b.now,
		Language:    p.Language,
		License:     b.c.License,
	}
	cand := core.NewRecord(meta, chunks)

	var rec *core.Record
	curID, exists := b.entries[p.URL]
	switch {
	case !exists:
		if isDeclared {
			return fmt.Errorf("declared a %s, but the page is new", d.Change)
		}
		rec, result.Action = cand, ActionNew
	default:
		cur, err := b.tree.ReadRecord(b.ks, curID)
		if err != nil {
			return fmt.Errorf("current record: %w", err)
		}
		if cur.Change == core.ChangeWithdrawal {
			b.report.Warnings = append(b.report.Warnings,
				fmt.Sprintf("%s was withdrawn but is still in the web root; not republished", p.File))
			result.Action, result.Record = ActionSkipped, curID
			b.report.Pages = append(b.report.Pages, result)
			return b.link(p, "")
		}
		if sameContent(cur, cand) {
			if isDeclared {
				return fmt.Errorf("declared a %s, but the page has not changed", d.Change)
			}
			result.Action, result.Record = ActionUnchanged, curID
			b.report.Pages = append(b.report.Pages, result)
			return b.link(p, curID)
		}
		change := core.ChangeRevision
		if isDeclared {
			change = d.Change
		}
		if rec, err = core.NewSuccessor(cur, change, d.Note, meta, chunks); err != nil {
			return err
		}
		result.Action = Action(change)
	}

	if err := core.SignRecord(rec, b.keyID, b.priv); err != nil {
		return err
	}
	bundle, err := core.NewBundle(rec, chunks)
	if err != nil {
		return err
	}
	if err := b.tree.writeBundle(bundle); err != nil {
		return err
	}
	if err := b.tree.writeRecord(rec); err != nil {
		return err
	}
	b.entries[p.URL], b.changed = rec.ID, true
	result.Record = rec.ID
	b.report.Pages = append(b.report.Pages, result)
	return b.link(p, rec.ID)
}

// withdraw records a withdrawal for url and deletes the bundles of every version.
func (b *builder) withdraw(url, file, note string) error {
	curID, ok := b.entries[url]
	if !ok {
		return errors.New("cannot withdraw a page that was never published")
	}
	cur, err := b.tree.ReadRecord(b.ks, curID)
	if err != nil {
		return fmt.Errorf("current record: %w", err)
	}
	if cur.Change == core.ChangeWithdrawal {
		return errors.New("already withdrawn")
	}
	meta := core.PageMeta{
		Publisher:   b.c.Publisher,
		URL:         url,
		Title:       cur.Title,
		PublishedAt: b.now,
		Language:    cur.Language,
		License:     cur.License,
	}
	rec, err := core.NewSuccessor(cur, core.ChangeWithdrawal, note, meta, nil)
	if err != nil {
		return err
	}
	if err := core.SignRecord(rec, b.keyID, b.priv); err != nil {
		return err
	}
	if err := b.tree.writeRecord(rec); err != nil {
		return err
	}
	for id := cur.ID; id != ""; {
		p, err := b.tree.BundlePath(id)
		if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		r, err := b.tree.ReadRecord(b.ks, id)
		if err != nil {
			return fmt.Errorf("history record %s: %w", id, err)
		}
		id = r.Supersedes
	}
	b.entries[url], b.changed = rec.ID, true
	b.report.Pages = append(b.report.Pages, PageResult{URL: url, File: file, Action: Action(core.ChangeWithdrawal), Record: rec.ID})
	if file != "" {
		return b.link(&page{File: file, HTML: isHTML(file)}, "")
	}
	return nil
}

// link sets the page's sourced-record link to the record, or removes it if
// recordID is empty. Markdown pages have no HTML to link from.
func (b *builder) link(p *page, recordID string) error {
	if !p.HTML {
		return nil
	}
	href := ""
	if recordID != "" {
		h, err := core.IDHex(recordID, core.RecordIDPrefix)
		if err != nil {
			return err
		}
		href = core.WellKnownPath + "records/" + h + ".json"
	}
	path := filepath.Join(b.c.RootDir(), filepath.FromSlash(p.File))
	src, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	out, changed, err := setRecordLink(src, href)
	if err != nil {
		return err
	}
	if !changed {
		return nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, out, fi.Mode().Perm())
}

func (b *builder) writeManifest(prev *core.Manifest) error {
	generated := b.now
	if prev != nil && !generated.After(prev.GeneratedAt) {
		// Never go backwards: verifiers reject a manifest older than one they've seen.
		generated = prev.GeneratedAt.Add(time.Second)
	}
	m := &core.Manifest{
		Spec:        core.SpecVersion,
		Publisher:   b.c.Publisher,
		GeneratedAt: generated,
		Entries:     b.entries,
	}
	if err := core.SignManifest(m, b.keyID, b.priv); err != nil {
		return err
	}
	return b.tree.writeJSON(b.tree.ManifestPath(), m)
}

// sameContent reports whether a candidate record says the same as the
// current one, ignoring time and history.
func sameContent(cur, cand *core.Record) bool {
	return cur.URL == cand.URL &&
		cur.Title == cand.Title &&
		cur.Language == cand.Language &&
		cur.MediaType == cand.MediaType &&
		cur.License == cand.License &&
		slices.EqualFunc(cur.Chunks, cand.Chunks, func(a, b core.ChunkRef) bool {
			return a.ID == b.ID && slices.Equal(a.Section, b.Section)
		}) &&
		slices.Equal(cur.References, cand.References)
}

func keyActive(ks *core.KeySet, id string) bool {
	for _, k := range ks.Keys {
		if k.ID == id {
			return k.Status == core.KeyActive
		}
	}
	return false
}

func isHTML(rel string) bool {
	ext := filepath.Ext(rel)
	return ext == ".html" || ext == ".htm"
}
