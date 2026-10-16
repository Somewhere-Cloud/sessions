// Package storage implements a bounded, read-only repository storage snapshot.
package storage

import "time"

const MaxRoots = 128
const MaxEntries = 300000
const Budget = 15 * time.Second

type Seed struct {
	Path    string
	Owned   bool
	Session string
}
type Reference struct {
	Path    string `json:"path"`
	Session string `json:"session"`
	PID     int    `json:"pid"`
}
type Bytes struct {
	Logical       int64  `json:"logical"`
	Allocated     *int64 `json:"allocated"`
	SharedLogical int64  `json:"shared_logical"`
	Files         int    `json:"files"`
	Symlinks      int    `json:"symlinks"`
	Confidence    string `json:"confidence"`
}
type Candidate struct {
	Path     string   `json:"path"`
	Category string   `json:"category"`
	Action   string   `json:"action"`
	Reasons  []string `json:"reasons"`
	Restore  string   `json:"restore,omitempty"`
	Bytes    Bytes    `json:"bytes"`
}
type GitState struct {
	Dirty     string `json:"dirty"`
	Untracked string `json:"untracked"`
	Unpushed  string `json:"unpushed"`
}
type Root struct {
	Path       string           `json:"path"`
	Ownership  string           `json:"ownership"`
	Sessions   []string         `json:"sessions"`
	Git        GitState         `json:"git"`
	References []Reference      `json:"references"`
	Bytes      Bytes            `json:"bytes"`
	Categories map[string]Bytes `json:"categories"`
	Candidates []Candidate      `json:"candidates"`
	Issues     []string         `json:"issues"`
}
type Report struct {
	MachineID   string   `json:"machine_id"`
	MachineName string   `json:"machine_name"`
	Preview     bool     `json:"preview"`
	Complete    bool     `json:"complete"`
	Roots       []Root   `json:"roots"`
	Totals      Bytes    `json:"totals"`
	Issues      []string `json:"issues"`
	Limitations []string `json:"limitations"`
	ScannedAt   int64    `json:"scanned_at"`
}

func emptyBytes() Bytes {
	value := int64(0)
	return Bytes{Allocated: &value, Confidence: "observed"}
}
func addBytes(target *Bytes, value Bytes) {
	target.Logical += value.Logical
	target.SharedLogical += value.SharedLogical
	target.Files += value.Files
	target.Symlinks += value.Symlinks
	if target.Allocated != nil && value.Allocated != nil {
		*target.Allocated += *value.Allocated
	} else {
		target.Allocated = nil
	}
	if value.Confidence != "observed" {
		target.Confidence = "partial"
	}
}
