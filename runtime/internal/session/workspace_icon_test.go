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

// An app logo in public/icon.svg is not the project's favicon; Next.js's
// app/icon.png is.
func TestWorkspaceIconIgnoresGenericAppLogos(t *testing.T) {
	root := t.TempDir()
	svg := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 16 16"><rect width="16" height="16"/></svg>`
	for _, dir := range []string{"public", "frontend/public"} {
		if err := os.MkdirAll(filepath.Join(root, dir), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, dir, "icon.svg"), []byte(svg), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if got := readWorkspaceIcon(root); got != "" {
		t.Fatal("a generic app logo was used as the project favicon")
	}
	if err := os.MkdirAll(filepath.Join(root, "app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "app", "icon.svg"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := readWorkspaceIcon(root); !strings.HasPrefix(got, "data:image/svg+xml;base64,") {
		t.Fatal("Next.js app/icon.svg was not used")
	}
}
