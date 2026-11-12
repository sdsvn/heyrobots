// Package ids makes random identifiers with a readable prefix.
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

// New returns prefix + "_" + 16 random hex characters.
func New(prefix string) string {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		panic("ids: no randomness: " + err.Error())
	}
	return prefix + "_" + hex.EncodeToString(b)
}
