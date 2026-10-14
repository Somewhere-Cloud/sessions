package storage

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"time"
)

type scanner struct {
	ctx           context.Context
	entries       int
	seen          map[string]bool
	candidateSeen map[string]map[string]bool
	total         Bytes
}

func Scan(ctx context.Context, seeds []Seed, references []Reference, excluded []string) Report {
	ctx, cancel := context.WithTimeout(ctx, Budget)
	defer cancel()
	report := Report{Preview: true, Complete: true, Roots: []Root{}, Issues: []string{}, ScannedAt: time.Now().UnixMilli(), Limitations: []string{
		"Read-only observation, not a teardown plan or filesystem snapshot. Source and unknown data are retained.",
		"Only known repository seeds and their Git-registered worktrees are scanned; no home traversal or provider transcript reads.",
		"Process references cover Sessions runners only; external processes and open files are unknown.",
		"Allocated bytes are stat blocks, not exclusive reclaimable space; APFS clones, compression and links outside the scan are not resolved. Windows allocation and hardlink identity are unavailable.",
		"Git upstream checks use local refs without fetching. Restoration commands are suggestions, not verified restore guarantees.",
		"Filesystem syscalls cannot be interrupted; a stalled scan occupies the single scan slot until it returns. Paths can change during a scan.",
	}}
	if len(seeds) > MaxRoots {
		seeds = seeds[:MaxRoots]
		report.Issues = append(report.Issues, "seed limit reached")
	}
	discovered, issues := discover(ctx, seeds)
	report.Issues = append(report.Issues, issues...)
	scan := scanner{ctx: ctx, seen: map[string]bool{}, total: emptyBytes()}
	for _, seed := range discovered {
		root := scan.root(seed, references, excluded)
		report.Roots = append(report.Roots, root)
		if root.Bytes.Confidence != "observed" {
			report.Complete = false
		}
	}
	report.Totals = scan.total
	if ctx.Err() != nil {
		report.Issues = append(report.Issues, "scan canceled or timed out")
	}
	if len(report.Issues) > 0 {
		report.Complete = false
	}
	if !report.Complete {
		report.Totals.Confidence = "partial"
	}
	return report
}
func (s *scanner) root(seed Seed, references []Reference, excluded []string) Root {
	root := Root{Path: seed.Path, Ownership: "unknown", Git: GitState{Dirty: "unknown", Untracked: "unknown", Unpushed: "unknown"}, Sessions: []string{}, References: []Reference{}, Bytes: emptyBytes(), Categories: map[string]Bytes{}, Candidates: []Candidate{}, Issues: []string{}}
	if seed.Owned {
		root.Ownership = "known"
		root.Sessions = append(root.Sessions, seed.Session)
	}
	for _, reference := range references {
		if contains(seed.Path, normalizePath(reference.Path)) || contains(normalizePath(reference.Path), seed.Path) {
			root.References = append(root.References, reference)
		}
	}
	for _, path := range excluded {
		resolved, _ := filepath.EvalSymlinks(path)
		if contains(seed.Path, normalizePath(path)) || (resolved != "" && contains(seed.Path, resolved)) {
			root.Issues = append(root.Issues, "home or filesystem root retained without traversal")
			root.Bytes.Confidence = "partial"
			return root
		}
	}
	info, err := os.Lstat(seed.Path)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		root.Issues = append(root.Issues, "root missing, inaccessible, or a symlink; retained without traversal")
		root.Bytes.Confidence = "partial"
		return root
	}
	var tracked map[string]bool
	var gitIssues []string
	root.Git, tracked, gitIssues = inspectGit(s.ctx, seed.Path)
	for _, issue := range gitIssues {
		incomplete(&root, issue)
	}
	root.Candidates = candidates(s.ctx, seed.Path, tracked)
	if len(root.Candidates) >= 64 {
		incomplete(&root, "candidate limit reached; other data classified as unknown")
	}
	sort.Slice(root.Candidates, func(i, j int) bool { return root.Candidates[i].Path < root.Candidates[j].Path })
	s.walk(&root, seed.Path, 0, tracked, map[string]bool{})
	preview(&root)
	return root
}
func (s *scanner) walk(root *Root, path string, depth int, tracked map[string]bool, localSeen map[string]bool) {
	if s.ctx.Err() != nil || s.entries >= MaxEntries || depth > 64 {
		incomplete(root, "scan canceled, timed out, or traversal limit reached")
		return
	}
	s.entries++
	info, err := os.Lstat(path)
	if err != nil {
		incomplete(root, "entry inaccessible; bytes are a lower bound")
		return
	}
	if info.IsDir() {
		if info.Mode().Perm()&0500 != 0500 {
			incomplete(root, "directory permission denied; bytes are a lower bound")
			return
		}
		s.walkDirectory(root, path, depth, tracked, localSeen)
		return
	}
	relative, _ := filepath.Rel(root.Path, path)
	category := categoryFor(path, relative, tracked, root.Candidates)
	value := emptyBytes()
	if info.Mode()&os.ModeSymlink != 0 {
		value.Symlinks = 1
		s.addCandidates(root, path, value, path)
		addBytes(&s.total, value)
	} else if info.Mode().IsRegular() {
		key, allocated, shared, known := fileIdentity(info)
		value.Logical, value.Files = info.Size(), 1
		if known {
			*value.Allocated = allocated
		} else {
			value.Allocated = nil
			value.Confidence = "partial"
		}
		if shared {
			value.SharedLogical = value.Logical
		}
		s.addCandidates(root, path, value, key)
		if key != "" && localSeen[key] {
			return
		}
		localSeen[key] = true
		if key == "" || !s.seen[key] {
			addBytes(&s.total, value)
			s.seen[key] = true
		}
	}
	addBytes(&root.Bytes, value)
	bucket, ok := root.Categories[category]
	if !ok {
		bucket = emptyBytes()
	}
	addBytes(&bucket, value)
	root.Categories[category] = bucket
}
func (s *scanner) walkDirectory(root *Root, path string, depth int, tracked map[string]bool, localSeen map[string]bool) {
	file, err := openDirectory(path)
	if err != nil {
		incomplete(root, "directory unreadable; bytes are a lower bound")
		return
	}
	defer file.Close()
	for {
		if s.ctx.Err() != nil || s.entries >= MaxEntries {
			incomplete(root, "scan canceled, timed out, or entry limit reached")
			return
		}
		entries, err := file.ReadDir(128)
		for _, entry := range entries {
			s.walk(root, filepath.Join(path, entry.Name()), depth+1, tracked, localSeen)
		}
		if errors.Is(err, io.EOF) {
			return
		}
		if err != nil {
			incomplete(root, "directory read incomplete; bytes are a lower bound")
			return
		}
	}
}
func incomplete(root *Root, reason string) {
	root.Bytes.Confidence = "partial"
	if len(root.Issues) < 8 {
		root.Issues = append(root.Issues, reason)
	}
}

func (s *scanner) addCandidates(root *Root, path string, value Bytes, key string) {
	if key == "" {
		key = path
	}
	if s.candidateSeen == nil {
		s.candidateSeen = map[string]map[string]bool{}
	}
	for i := range root.Candidates {
		candidate := &root.Candidates[i]
		if !contains(candidate.Path, path) {
			continue
		}
		if s.candidateSeen[candidate.Path] == nil {
			s.candidateSeen[candidate.Path] = map[string]bool{}
		}
		if !s.candidateSeen[candidate.Path][key] {
			addBytes(&candidate.Bytes, value)
			s.candidateSeen[candidate.Path][key] = true
		}
	}
}
