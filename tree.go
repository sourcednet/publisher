package publisher

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/sourcednet/core"
)

// Tree is the /.well-known/sourced/ tree inside a web root.
type Tree struct {
	Root      string
	Publisher string
}

func (t Tree) path(parts ...string) string {
	base := filepath.Join(t.Root, filepath.FromSlash(strings.Trim(core.WellKnownPath, "/")))
	return filepath.Join(append([]string{base}, parts...)...)
}

// KeysPath is the path of keys.json.
func (t Tree) KeysPath() string { return t.path("keys.json") }

// ManifestPath is the path of manifest.json.
func (t Tree) ManifestPath() string { return t.path("manifest.json") }

// RecordPath is the path of a record file.
func (t Tree) RecordPath(id string) (string, error) {
	h, err := core.IDHex(id, core.RecordIDPrefix)
	if err != nil {
		return "", err
	}
	return t.path("records", h+".json"), nil
}

// BundlePath is the path of a record's bundle.
func (t Tree) BundlePath(id string) (string, error) {
	h, err := core.IDHex(id, core.RecordIDPrefix)
	if err != nil {
		return "", err
	}
	return t.path("bundles", h+".json"), nil
}

// ReadKeySet reads and checks keys.json.
func (t Tree) ReadKeySet() (*core.KeySet, error) {
	b, err := os.ReadFile(t.KeysPath())
	if err != nil {
		return nil, err
	}
	return core.ParseKeySet(b, t.Publisher)
}

// ReadManifest reads and verifies manifest.json. It returns nil and no error
// if the publisher has no manifest yet.
func (t Tree) ReadManifest(ks *core.KeySet) (*core.Manifest, error) {
	b, err := os.ReadFile(t.ManifestPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return core.VerifyManifest(b, ks, t.Publisher, time.Time{})
}

// ReadRecord reads and verifies a record.
func (t Tree) ReadRecord(ks *core.KeySet, id string) (*core.Record, error) {
	p, err := t.RecordPath(id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	return core.VerifyRecord(b, ks, t.Publisher)
}

func (t Tree) writeJSON(path string, v any) error {
	b, err := core.EncodeJSON(v)
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o644)
}

func (t Tree) writeRecord(r *core.Record) error {
	p, err := t.RecordPath(r.ID)
	if err != nil {
		return err
	}
	return t.writeJSON(p, r)
}

func (t Tree) writeBundle(b *core.Bundle) error {
	p, err := t.BundlePath(b.Record)
	if err != nil {
		return err
	}
	return t.writeJSON(p, b)
}

// recordIDs lists the IDs of every record file in the tree.
func (t Tree) recordIDs() ([]string, error) {
	entries, err := os.ReadDir(t.path("records"))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		if h, ok := strings.CutSuffix(e.Name(), ".json"); ok && !e.IsDir() {
			ids = append(ids, core.RecordIDPrefix+h)
		}
	}
	return ids, nil
}
