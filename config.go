package publisher

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/sourcednet/core"
)

// ConfigFile is the name of a project's config file.
const ConfigFile = "sourced.json"

// Config describes a publisher project. Relative paths resolve against the
// directory holding the config file.
type Config struct {
	Publisher string `json:"publisher"`
	// Root is the web root: the signed files go to its
	// /.well-known/sourced/.
	Root string `json:"root"`
	// Pages, if set, is the folder holding the built pages to sign, such as
	// a site generator's output (dist); otherwise the pages are in Root.
	// This lets a site sign its build output on the author's machine and
	// commit the signed files to the folder its generator publishes as is.
	Pages      string           `json:"pages,omitempty"`
	KeysDir    string           `json:"keys_dir"`
	SigningKey string           `json:"signing_key"`
	Language   string           `json:"language,omitempty"`
	License    string           `json:"license,omitempty"`
	Chunking   core.ChunkParams `json:"chunking"`

	// ContentSelector picks the element holding a page's main content: a tag
	// name such as "main", or "#id". Empty tries main, article, then body.
	ContentSelector string `json:"content_selector,omitempty"`
	// DropSelectors lists elements inside the content to leave out of what
	// is signed, such as reference lists or navigation boxes: tag, .class,
	// #id, tag.class, or tag#id.
	DropSelectors []string `json:"drop_selectors,omitempty"`
	// DropSections lists section headings, such as "See also" or "Related
	// articles", whose sections (the heading and everything up to the next
	// heading of the same or a higher level) are left out of what is signed.
	// Matched ignoring case.
	DropSections []string `json:"drop_sections,omitempty"`
	// CleanURLs serves about.html at /about instead of /about.html.
	CleanURLs bool `json:"clean_urls,omitempty"`
	// Exclude lists path patterns, relative to the root, that are not pages.
	Exclude []string `json:"exclude,omitempty"`

	dir string
}

// DefaultConfig returns a config for publisher with the web root at root.
func DefaultConfig(publisher, root string) *Config {
	return &Config{
		Publisher: strings.ToLower(publisher),
		Root:      root,
		KeysDir:   ".sourced/keys",
		Chunking:  core.DefaultChunkParams,
		Exclude:   []string{"404.html"},
	}
}

// LoadConfig reads a config file. path may be the file or its directory.
func LoadConfig(path string) (*Config, error) {
	if fi, err := os.Stat(path); err == nil && fi.IsDir() {
		path = filepath.Join(path, ConfigFile)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(b, &c); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.dir = filepath.Dir(path)
	if err := c.validate(); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return &c, nil
}

// Save writes the config into dir, which becomes its base directory.
func (c *Config) Save(dir string) error {
	if err := c.validate(); err != nil {
		return err
	}
	b, err := core.EncodeJSON(c)
	if err != nil {
		return err
	}
	c.dir = dir
	return writeFileAtomic(filepath.Join(dir, ConfigFile), b, 0o644)
}

func (c *Config) validate() error {
	switch {
	case c.Publisher == "" || strings.ContainsAny(c.Publisher, "/:"):
		return fmt.Errorf("publisher %q: want a bare domain such as example.org", c.Publisher)
	case c.Root == "":
		return errors.New("missing root")
	case c.KeysDir == "":
		return errors.New("missing keys_dir")
	}
	if err := c.Chunking.Validate(); err != nil {
		return err
	}
	if _, err := parseSelectors(c.DropSelectors); err != nil {
		return err
	}
	keys := c.resolve(c.KeysDir)
	for name, dir := range map[string]string{"the web root": c.Root, "the pages folder": c.Pages} {
		if dir == "" {
			continue
		}
		if rel, err := filepath.Rel(c.resolve(dir), keys); err == nil && !strings.HasPrefix(rel, "..") {
			return fmt.Errorf("keys_dir %q is inside %s %q: private keys would be published", c.KeysDir, name, dir)
		}
	}
	return nil
}

func (c *Config) resolve(p string) string {
	if filepath.IsAbs(p) {
		return p
	}
	return filepath.Join(c.dir, p)
}

// RootDir returns the web root's path.
func (c *Config) RootDir() string { return c.resolve(c.Root) }

// PagesDir returns the absolute path of the folder holding the pages to
// sign: Pages if set, otherwise the web root.
func (c *Config) PagesDir() string {
	if c.Pages == "" {
		return c.RootDir()
	}
	return c.resolve(c.Pages)
}

// KeysPath returns the private key directory's path.
func (c *Config) KeysPath() string { return c.resolve(c.KeysDir) }

// Tree returns the publisher tree inside the web root.
func (c *Config) Tree() Tree { return Tree{Root: c.RootDir(), Publisher: c.Publisher} }
