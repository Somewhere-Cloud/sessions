package api

import (
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Team cursors are kept under the daemon's state root, one file per cursor:
// <state-root>/team-cursors/<32 hex>.json. Each file is written to a temporary
// name and renamed into place, so a reader sees a whole cursor or none.
const (
	teamCursorDirName     = "team-cursors"
	teamCursorSchema      = 1
	teamCursorFingerprint = "team-member-json-sha256"
	teamCursorFileMax     = 256 * 1024
)

var (
	teamCursorKeyPattern = regexp.MustCompile(`^[0-9a-f]{32}$`)
	errTeamCursorMissing = errors.New("team cursor not saved")
)

type teamCursorFile struct {
	SchemaVersion int               `json:"schema_version"`
	Fingerprint   string            `json:"fingerprint"`
	Caller        string            `json:"caller"`
	AtMS          int64             `json:"at_ms"`
	Rows          map[string]string `json:"rows"`
}

func validTeamCursorKey(key string) bool { return teamCursorKeyPattern.MatchString(key) }

func teamCursorPath(root, key string) string {
	return filepath.Join(root, teamCursorDirName, key+".json")
}

func writeTeamCursor(root, key string, snapshot teamSnapshot) error {
	if !validTeamCursorKey(key) {
		return fmt.Errorf("invalid team cursor %q", key)
	}
	document := teamCursorFile{
		SchemaVersion: teamCursorSchema, Fingerprint: teamCursorFingerprint,
		Caller: snapshot.caller, AtMS: snapshot.at.UnixMilli(), Rows: make(map[string]string, len(snapshot.rows)),
	}
	for id, digest := range snapshot.rows {
		document.Rows[id] = hex.EncodeToString(digest[:])
	}
	encoded, err := json.Marshal(document)
	if err != nil {
		return err
	}
	directory := filepath.Join(root, teamCursorDirName)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary, err := os.CreateTemp(directory, ".tmp-*")
	if err != nil {
		return err
	}
	name := temporary.Name()
	_, writeErr := temporary.Write(encoded)
	if writeErr == nil {
		writeErr = temporary.Sync()
	}
	closeErr := temporary.Close()
	if writeErr == nil {
		writeErr = closeErr
	}
	if writeErr == nil {
		writeErr = os.Chmod(name, 0o600)
	}
	if writeErr == nil {
		writeErr = os.Rename(name, teamCursorPath(root, key))
	}
	if writeErr != nil {
		_ = os.Remove(name)
	}
	return writeErr
}

func readTeamCursor(root, key string) (teamSnapshot, error) {
	file, err := os.Open(teamCursorPath(root, key))
	if errors.Is(err, os.ErrNotExist) {
		return teamSnapshot{}, errTeamCursorMissing
	}
	if err != nil {
		return teamSnapshot{}, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, teamCursorFileMax+1))
	if err != nil {
		return teamSnapshot{}, err
	}
	if len(encoded) > teamCursorFileMax {
		return teamSnapshot{}, errors.New("team cursor file is too large")
	}
	return decodeTeamCursor(encoded)
}

func decodeTeamCursor(encoded []byte) (teamSnapshot, error) {
	var document teamCursorFile
	if err := json.Unmarshal(encoded, &document); err != nil {
		return teamSnapshot{}, err
	}
	if document.SchemaVersion != teamCursorSchema || document.Fingerprint != teamCursorFingerprint {
		return teamSnapshot{}, fmt.Errorf("unsupported team cursor schema %d/%q", document.SchemaVersion, document.Fingerprint)
	}
	if strings.TrimSpace(document.Caller) == "" || document.AtMS <= 0 || len(document.Rows) > teamMemberMax {
		return teamSnapshot{}, errors.New("team cursor is incomplete or oversized")
	}
	snapshot := teamSnapshot{caller: document.Caller, at: time.UnixMilli(document.AtMS), rows: make(map[string][32]byte, len(document.Rows))}
	for id, digest := range document.Rows {
		decoded, err := hex.DecodeString(digest)
		if err != nil || len(decoded) != 32 || id == "" {
			return teamSnapshot{}, errors.New("team cursor row is malformed")
		}
		snapshot.rows[id] = [32]byte(decoded)
	}
	return snapshot, nil
}

// loadTeamCursors reads every saved cursor. Files that are not cursors are
// ignored; cursor files that cannot be decoded are reported, not deleted, so
// a caller holding one hears that its baseline was damaged.
func loadTeamCursors(root string) (map[string]teamSnapshot, map[string]struct{}, error) {
	saved := make(map[string]teamSnapshot)
	unreadable := make(map[string]struct{})
	entries, err := os.ReadDir(filepath.Join(root, teamCursorDirName))
	if errors.Is(err, os.ErrNotExist) {
		return saved, unreadable, nil
	}
	if err != nil {
		return saved, unreadable, err
	}
	for _, entry := range entries {
		key, isCursor := strings.CutSuffix(entry.Name(), ".json")
		if !isCursor || !validTeamCursorKey(key) || !entry.Type().IsRegular() {
			continue
		}
		snapshot, err := readTeamCursor(root, key)
		if err != nil {
			unreadable[key] = struct{}{}
			continue
		}
		saved[key] = snapshot
	}
	return saved, unreadable, nil
}

func removeTeamCursor(root, key string) error {
	if !validTeamCursorKey(key) {
		return nil
	}
	if err := os.Remove(teamCursorPath(root, key)); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// removeStaleTeamCursorFiles deletes cursor and temporary files last written
// before cutoff. Only names this store creates are touched. It returns the
// cursor keys it removed.
func removeStaleTeamCursorFiles(root string, cutoff time.Time) []string {
	directory := filepath.Join(root, teamCursorDirName)
	entries, err := os.ReadDir(directory)
	if err != nil {
		return nil
	}
	var removed []string
	for _, entry := range entries {
		name := entry.Name()
		key, isCursor := strings.CutSuffix(name, ".json")
		isCursor = isCursor && validTeamCursorKey(key)
		if (!isCursor && !strings.HasPrefix(name, ".tmp-")) || !entry.Type().IsRegular() {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.ModTime().Before(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(directory, name)) == nil && isCursor {
			removed = append(removed, key)
		}
	}
	return removed
}
