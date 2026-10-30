package session

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceIconLocalBoundaries(t *testing.T) {
	root := t.TempDir()
	if got := readWorkspaceIcon(root); got != "" {
		t.Fatal("missing icon invented")
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><circle cx="8" cy="8" r="6"/></svg>`
	outside := filepath.Join(t.TempDir(), "outside.svg")
	if err := os.WriteFile(outside, []byte(svg), 0600); err != nil {
		t.Fatal(err)
	}
	candidate := filepath.Join(root, "favicon.svg")
	if err := os.Symlink(outside, candidate); err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceIcon(root); got != "" {
		t.Fatal("exported icon outside workspace")
	}
	if err := os.Remove(candidate); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(candidate, []byte(strings.Repeat("x", 16385)), 0600); err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceIcon(root); got != "" {
		t.Fatal("oversized icon accepted")
	}
	if err := os.WriteFile(candidate, []byte(svg), 0600); err != nil {
		t.Fatal(err)
	}
	want := "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString([]byte(svg))
	if got := readWorkspaceIcon(root); got != want {
		t.Fatal("local SVG icon not delivered as image")
	}
}
