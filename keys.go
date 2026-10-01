package publisher

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/sourcednet/core"
)

// privateKeyFile is how a private key is stored: the 32-byte Ed25519 seed.
type privateKeyFile struct {
	Spec string `json:"spec"`
	ID   string `json:"id"`
	Alg  string `json:"alg"`
	Seed string `json:"seed"`
}

func privateKeyPath(dir, id string) string { return filepath.Join(dir, id+".key.json") }

func writePrivateKey(dir, id string, priv ed25519.PrivateKey) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	path := privateKeyPath(dir, id)
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("private key %s already exists", path)
	}
	b, err := core.EncodeJSON(privateKeyFile{
		Spec: core.SpecVersion,
		ID:   id,
		Alg:  core.AlgEd25519,
		Seed: base64.StdEncoding.EncodeToString(priv.Seed()),
	})
	if err != nil {
		return err
	}
	return writeFileAtomic(path, b, 0o600)
}

func readPrivateKey(dir, id string) (ed25519.PrivateKey, error) {
	b, err := os.ReadFile(privateKeyPath(dir, id))
	if err != nil {
		return nil, err
	}
	var f privateKeyFile
	if err := json.Unmarshal(b, &f); err != nil {
		return nil, fmt.Errorf("private key %s: %w", id, err)
	}
	seed, err := base64.StdEncoding.DecodeString(f.Seed)
	if err != nil || len(seed) != ed25519.SeedSize || f.Alg != core.AlgEd25519 || f.ID != id {
		return nil, errors.New("private key " + id + ": invalid file")
	}
	return ed25519.NewKeyFromSeed(seed), nil
}
