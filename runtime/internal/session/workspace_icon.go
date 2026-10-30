package session

import (
	"encoding/base64"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Known favicon locations only: no HTML discovery, URL fetch, or recursive
// scan. A generic icon.* is an app logo rather than the project's favicon,
// except where Next.js serves app/icon.* as one. A project without a favicon
// gets the client's lettered mark. This is called by the background workspace
// observer, never inventory.
func readWorkspaceIcon(root string) string {
	resolved, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	root = resolved
	for _, dir := range []string{"", "public", "app", "src/app", "frontend/public", "assets"} {
		names := []string{"favicon.svg", "favicon.png", "favicon.ico"}
		if dir == "app" || dir == "src/app" {
			names = append(names, "icon.svg", "icon.png")
		}
		for _, name := range names {
			path, err := filepath.EvalSymlinks(filepath.Join(root, dir, name))
			if err != nil {
				continue
			}
			relative, err := filepath.Rel(root, path)
			if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				continue
			}
			if icon := readLocalIcon(path); icon != "" {
				return icon
			}
		}
	}
	return ""
}

func readLocalIcon(path string) string {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > 16384 {
		return ""
	}
	file, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 16385))
	if err != nil || len(data) > 16384 {
		return ""
	}
	mime := ""
	switch {
	case strings.HasPrefix(string(data), "\x89PNG\r\n\x1a\n"):
		mime = "image/png"
	case strings.HasPrefix(string(data), "\x00\x00\x01\x00"):
		mime = "image/x-icon"
	case filepath.Ext(path) == ".svg" && strings.Contains(string(data), "<svg"):
		// Delivered only as an image data URL, never inserted as document markup.
		mime = "image/svg+xml"
	}
	if mime == "" {
		return ""
	}
	return "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(data)
}
