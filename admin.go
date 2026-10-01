package publisher

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/sourcednet/core"
)

// Init creates a project in dir: sourced.json, a first signing key, and
// keys.json in the web root. It returns the new config.
func Init(dir, publisher, root, pages string, now time.Time) (*Config, error) {
	if _, err := os.Stat(filepath.Join(dir, ConfigFile)); err == nil {
		return nil, fmt.Errorf("%s already exists", filepath.Join(dir, ConfigFile))
	}
	c := DefaultConfig(publisher, root)
	c.Pages = pages
	c.dir = dir
	if err := c.validate(); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(c.RootDir(), 0o755); err != nil {
		return nil, err
	}
	ks := &core.KeySet{Spec: core.SpecVersion, Publisher: c.Publisher}
	id, err := addKey(c, ks, now)
	if err != nil {
		return nil, err
	}
	c.SigningKey = id
	if err := c.Tree().writeJSON(c.Tree().KeysPath(), ks); err != nil {
		return nil, err
	}
	if err := c.Save(dir); err != nil {
		return nil, err
	}
	return c, nil
}

// AddKey generates a new signing key, retires the active ones, and makes the
// new key the project's signing key. Content signed with retired keys stays
// valid. It returns the new key's ID.
func AddKey(c *Config, now time.Time) (string, error) {
	tree := c.Tree()
	ks, err := tree.ReadKeySet()
	if err != nil {
		return "", err
	}
	for i := range ks.Keys {
		if ks.Keys[i].Status == core.KeyActive {
			ks.Keys[i].Status = core.KeyRetired
		}
	}
	id, err := addKey(c, ks, now)
	if err != nil {
		return "", err
	}
	if err := tree.writeJSON(tree.KeysPath(), ks); err != nil {
		return "", err
	}
	c.SigningKey = id
	return id, c.Save(c.dir)
}

// addKey generates a key with the next free ID for now's year (2026a,
// 2026b, …), stores the private half, and appends the public half to ks.
func addKey(c *Config, ks *core.KeySet, now time.Time) (string, error) {
	if now.IsZero() {
		now = time.Now()
	}
	var id string
	for l := 'a'; l <= 'z'; l++ {
		cand := fmt.Sprintf("%d%c", now.UTC().Year(), l)
		if !hasKey(ks, cand) {
			id = cand
			break
		}
	}
	if id == "" {
		return "", fmt.Errorf("no free key id left for %d", now.UTC().Year())
	}
	pub, priv, err := core.GenerateKey(nil)
	if err != nil {
		return "", err
	}
	if err := writePrivateKey(c.KeysPath(), id, priv); err != nil {
		return "", err
	}
	ks.Keys = append(ks.Keys, core.NewKey(id, pub))
	return id, nil
}

func hasKey(ks *core.KeySet, id string) bool {
	for _, k := range ks.Keys {
		if k.ID == id {
			return true
		}
	}
	return false
}

// RevokeKey revokes a key and re-signs everything it signed with the current
// signing key: every record in the tree, current or historical, and the
// manifest. Record IDs don't depend on the key, so every citation stays
// valid. It returns how many records were re-signed.
func RevokeKey(c *Config, id string) (int, error) {
	if id == c.SigningKey {
		return 0, errors.New("cannot revoke the signing key; run keygen first to add a new one")
	}
	tree := c.Tree()
	ks, err := tree.ReadKeySet()
	if err != nil {
		return 0, err
	}
	if !hasKey(ks, id) {
		return 0, fmt.Errorf("key %q is not in keys.json", id)
	}
	priv, err := readPrivateKey(c.KeysPath(), c.SigningKey)
	if err != nil {
		return 0, err
	}

	// Read everything while the key still verifies, then write it all back.
	ids, err := tree.recordIDs()
	if err != nil {
		return 0, err
	}
	var resign []*core.Record
	for _, rid := range ids {
		r, err := tree.ReadRecord(ks, rid)
		if err != nil {
			return 0, fmt.Errorf("record %s: %w", rid, err)
		}
		if r.Sig.KeyID == id {
			resign = append(resign, r)
		}
	}
	man, err := tree.ReadManifest(ks)
	if err != nil {
		return 0, err
	}

	for _, r := range resign {
		oldID := r.ID
		if err := core.SignRecord(r, c.SigningKey, priv); err != nil {
			return 0, err
		}
		if r.ID != oldID {
			return 0, fmt.Errorf("re-signing changed record id %s to %s", oldID, r.ID)
		}
		if err := tree.writeRecord(r); err != nil {
			return 0, err
		}
	}
	if man != nil && man.Sig.KeyID == id {
		if err := core.SignManifest(man, c.SigningKey, priv); err != nil {
			return 0, err
		}
		if err := tree.writeJSON(tree.ManifestPath(), man); err != nil {
			return 0, err
		}
	}
	for i := range ks.Keys {
		if ks.Keys[i].ID == id {
			ks.Keys[i].Status = core.KeyRevoked
		}
	}
	return len(resign), tree.writeJSON(tree.KeysPath(), ks)
}
