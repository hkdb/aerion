// Package contacts is the top-level Go package for the Contacts extension.
// It carries only the embedded manifest data so the host can read extension
// metadata without depending on the backend implementation package.
//
// The real backend implementation lives in extensions/contacts/backend; the
// frontend assets in extensions/contacts/frontend.
package contacts

import (
	_ "embed"
	"encoding/json"

	coreapi "github.com/hkdb/aerion/internal/core/api/v1"
)

//go:embed manifest.json
var manifestJSON []byte

// ManifestJSON returns the raw manifest.json bytes embedded in the binary.
// Useful for shipping the manifest over IPC to inspector UIs without re-marshalling.
func ManifestJSON() []byte {
	out := make([]byte, len(manifestJSON))
	copy(out, manifestJSON)
	return out
}

// Manifest returns the parsed manifest. Panics on malformed JSON since the
// file is compiled into the binary — a parse error is a build-time bug.
func Manifest() coreapi.Manifest {
	var m coreapi.Manifest
	if err := json.Unmarshal(manifestJSON, &m); err != nil {
		panic("contacts: manifest.json is malformed (build-time bug): " + err.Error())
	}
	return m
}
