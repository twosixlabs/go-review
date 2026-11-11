// Package changes owns revision comparisons independently of ordinary reviews.
package changes

import (
	"crypto/sha256"
	"encoding/hex"
)

const Policy = "change-review/1"

type Request struct {
	Directory string `json:"directory"`
	Before    string `json:"before"`
	After     string `json:"after"`
	MergeBase bool   `json:"mergeBase,omitempty"`
	Provider  string `json:"provider,omitempty"`
}

type Revision struct {
	Label  string `json:"label"`
	Commit string `json:"commit"`
}

type Resolved struct {
	Repository      string    `json:"repository"`
	Before          Revision  `json:"before"`
	After           Revision  `json:"after"`
	Target          *Revision `json:"target,omitempty"`
	Policy          string    `json:"policy"`
	Provider        string    `json:"provider,omitempty"`
	ObjectDirectory string    `json:"-"`
	inputStorage    string
}

type Object struct {
	Path string `json:"path"`
	OID  string `json:"oid"`
	Mode string `json:"mode"`
}

type Range struct {
	Start int `json:"start"`
	Lines int `json:"lines"`
}

// An item is unique changed material. Associations and contextual occurrences
// reference its ID and never introduce additional progress units.
type Item struct {
	ParentID string `json:"parentID,omitempty"`
	ID       string `json:"id"`
	FileID   string `json:"fileID"`
	Before   Range  `json:"before"`
	After    Range  `json:"after"`
	Kind     string `json:"kind"`
}

type FileChange struct {
	ID                string `json:"id"`
	Status            string `json:"status"`
	Before            Object `json:"before"`
	After             Object `json:"after"`
	Content           string `json:"content"`
	BeforeEligibility string `json:"beforeEligibility,omitempty"`
	AfterEligibility  string `json:"afterEligibility,omitempty"`
	Items             []Item `json:"items"`
}

func identity(parts ...string) string {
	h := sha256.New()
	for _, part := range parts {
		h.Write([]byte(part))
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))
}
