package storage

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
)

func candidates(ctx context.Context, root string, tracked map[string]bool) []Candidate {
	result := []Candidate{}
	for path := range tracked {
		if ctx.Err() != nil || len(result) >= 64 {
			break
		}
		if filepath.Base(path) != "package-lock.json" {
			continue
		}
		base := filepath.Dir(path)
		if !tracked[filepath.ToSlash(filepath.Join(base, "package.json"))] {
			continue
		}
		result = appendCandidate(ctx, result, root, base, "node_modules", "dependencies", "Run npm ci in "+filepath.Join(root, base)+" using the recorded lockfile; install scripts and network access may be required. Restoration has not been tested.", tracked)
		if viteBuild(root, base, tracked) {
			result = appendCandidate(ctx, result, root, base, "dist", "build-output", "Run npm ci, then npm run build in "+filepath.Join(root, base)+". Build environment and restoration have not been tested.", tracked)
		}
	}
	return result
}
func appendCandidate(ctx context.Context, result []Candidate, root, base, name, category, restore string, tracked map[string]bool) []Candidate {
	if len(result) >= 64 {
		return result
	}
	relative := filepath.ToSlash(filepath.Join(base, name))
	for path := range tracked {
		if path == relative || strings.HasPrefix(path, relative+"/") {
			return result
		}
	}
	info, err := os.Lstat(filepath.Join(root, relative))
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return result
	}
	if _, err := git(ctx, root, "check-ignore", "--quiet", "--", relative); err != nil {
		return result
	}
	return append(result, Candidate{Path: filepath.Join(root, relative), Category: category, Action: "kept", Reasons: []string{}, Restore: restore, Bytes: emptyBytes()})
}
func viteBuild(root, base string, tracked map[string]bool) bool {
	config := false
	for _, name := range []string{"vite.config.ts", "vite.config.js", "vite.config.mts"} {
		if tracked[filepath.ToSlash(filepath.Join(base, name))] {
			config = true
		}
	}
	if !config {
		return false
	}
	path := filepath.Join(root, base, "package.json")
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() {
		return false
	}
	file, err := os.Open(path)
	if err != nil {
		return false
	}
	defer file.Close()
	var value struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.NewDecoder(io.LimitReader(file, 65536)).Decode(&value) != nil {
		return false
	}
	return strings.Contains(value.Scripts["build"], "vite build")
}
func categoryFor(path, relative string, tracked map[string]bool, candidates []Candidate) string {
	for _, candidate := range candidates {
		if contains(candidate.Path, path) {
			return candidate.Category
		}
	}
	if tracked[filepath.ToSlash(relative)] || relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
		return "source"
	}
	return "unknown"
}
func contains(root, path string) bool {
	rel, err := filepath.Rel(root, path)
	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}
func preview(root *Root) {
	for index := range root.Candidates {
		candidate := &root.Candidates[index]
		if root.Ownership != "known" {
			candidate.Reasons = append(candidate.Reasons, "external or unrecorded worktree; ownership unknown")
		}
		if root.Git.Dirty != "no" {
			candidate.Reasons = append(candidate.Reasons, "Git changes: "+root.Git.Dirty)
		}
		if root.Git.Untracked != "no" {
			candidate.Reasons = append(candidate.Reasons, "untracked files: "+root.Git.Untracked)
		}
		if root.Git.Unpushed != "no" {
			candidate.Reasons = append(candidate.Reasons, "unpushed commits: "+root.Git.Unpushed+" (local refs only; no fetch)")
		}
		if len(root.References) > 0 {
			candidate.Reasons = append(candidate.Reasons, "live or unresolved runner references this checkout")
		}
		if root.Bytes.Confidence != "observed" {
			candidate.Bytes.Confidence = "partial"
			candidate.Reasons = append(candidate.Reasons, "scan incomplete or allocation unavailable")
		}
		if candidate.Bytes.SharedLogical > 0 {
			candidate.Reasons = append(candidate.Reasons, "shared hardlinks; freed bytes cannot be inferred")
		}
		if len(candidate.Reasons) == 0 {
			candidate.Action = "potentially-reclaimable"
			candidate.Reasons = append(candidate.Reasons, "ignored, untracked generated directory with a tracked lockfile; manual review required")
		}
	}
}
