package publisher

import (
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/sourcednet/core"
)

// page is one page found in the web root.
type page struct {
	File     string // path relative to the root, slash-separated
	URL      string
	Title    string
	Language string
	Markdown string
	HTML     bool
}

// discoverPages lists the pages in the pages folder (the web root unless
// set apart): .html, .htm, and .md files outside /.well-known/ and not
// excluded by the config.
func discoverPages(c *Config) ([]string, error) {
	root := c.PagesDir()
	var files []string
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(root, p)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			if rel == ".well-known" || (rel != "." && strings.HasPrefix(d.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		switch strings.ToLower(path.Ext(rel)) {
		case ".html", ".htm", ".md":
		default:
			return nil
		}
		for _, pat := range c.Exclude {
			if ok, _ := path.Match(pat, rel); ok {
				return nil
			}
		}
		files = append(files, rel)
		return nil
	})
	return files, err
}

// pageURL maps a file in the web root to its public URL.
func pageURL(c *Config, rel string) string {
	p := "/" + rel
	ext := strings.ToLower(path.Ext(p))
	switch {
	case path.Base(p) == "index.html" || path.Base(p) == "index.htm" || path.Base(p) == "index.md":
		p = strings.TrimSuffix(p, path.Base(p))
	case ext == ".md" || (c.CleanURLs && (ext == ".html" || ext == ".htm")):
		p = strings.TrimSuffix(p, path.Ext(p))
	}
	return (&url.URL{Scheme: "https", Host: c.Publisher, Path: p}).String()
}

// resolvePageRef turns a --page argument, a URL or a path relative to the
// web root, into a page URL.
func resolvePageRef(c *Config, ref string) (string, error) {
	if strings.HasPrefix(ref, "https://") {
		u, err := url.Parse(ref)
		if err != nil || !strings.EqualFold(u.Hostname(), c.Publisher) {
			return "", fmt.Errorf("page %q is not a URL on %s", ref, c.Publisher)
		}
		return ref, nil
	}
	return pageURL(c, strings.TrimPrefix(filepath.ToSlash(ref), "/")), nil
}

func loadPage(c *Config, rel string) (*page, error) {
	src, err := os.ReadFile(filepath.Join(c.PagesDir(), filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}
	p := &page{File: rel, URL: pageURL(c, rel), Language: c.Language}
	if strings.EqualFold(path.Ext(rel), ".md") {
		p.Markdown = string(src)
		p.Title = markdownTitle(p.Markdown, rel)
		return p, nil
	}
	ex, err := extractHTML(src, Extraction{Selector: c.ContentSelector, Drop: c.DropSelectors, DropSections: c.DropSections}, p.URL)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", rel, err)
	}
	p.HTML, p.Markdown, p.Title = true, ex.Markdown, ex.Title
	if ex.Language != "" {
		p.Language = ex.Language
	}
	if p.Title == "" {
		p.Title = rel
	}
	return p, nil
}

// markdownTitle returns the first level-one heading, or the file name.
func markdownTitle(md, rel string) string {
	for _, line := range strings.Split(core.NormalizeText(md), "\n") {
		if t, ok := strings.CutPrefix(line, "# "); ok {
			return strings.TrimSpace(t)
		}
	}
	return strings.TrimSuffix(path.Base(rel), path.Ext(rel))
}
