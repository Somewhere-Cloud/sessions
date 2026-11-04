package project

import (
	"net/url"
	"strings"
)

// A project identity names the same work on every computer. Two daemons
// cannot share a project ID (it is derived from a local folder), but they can
// agree on what a repository's origin points at, so the fleet view groups by
// this key instead. The form is "git:<host>/<path>" for a checkout with an
// origin, "name:<lowercased name>" for a stored or implicit project without
// one, and "scratch" for the No project group.
const (
	identityGitPrefix  = "git:"
	identityNamePrefix = "name:"
	// ScratchIdentity is shared by every computer's No project group.
	ScratchIdentity = ScratchProjectID
)

// Identity picks the strongest cross-computer evidence available: the
// normalised origin URL, then the name the project was stored under, then the
// folder's own name. An empty result means nothing identifies the folder.
func Identity(origin, storedName, folder string) string {
	if normalized := NormalizeRemote(origin); normalized != "" {
		return identityGitPrefix + normalized
	}
	for _, name := range []string{storedName, folder} {
		if name = strings.ToLower(strings.TrimSpace(name)); name != "" {
			return identityNamePrefix + name
		}
	}
	return ""
}

// NormalizeRemote reduces a git remote URL to host/path so the ssh and https
// spellings of one repository compare equal:
//
//	git@github.com:Owner/Repo.git           -> github.com/owner/repo
//	https://user@GitHub.com/owner/repo/     -> github.com/owner/repo
//	ssh://git@github.com:22/owner/repo.git  -> github.com/owner/repo
//
// The scheme, user, password, and port are dropped, and a trailing ".git" or
// "/" is removed. The whole result is lowercased: hosted forges resolve
// repository paths case-insensitively, and a person who typed the remote in
// two different cases on two computers still means one repository. A local
// path (a remote that is just another folder) is kept as a cleaned path.
func NormalizeRemote(remote string) string {
	remote = strings.TrimSpace(remote)
	if remote == "" {
		return ""
	}
	host, path := splitRemote(remote)
	path = strings.Trim(path, "/")
	path = strings.TrimSuffix(path, ".git")
	path = strings.Trim(path, "/")
	if path == "" {
		return ""
	}
	if host == "" {
		return strings.ToLower(path)
	}
	return strings.ToLower(host + "/" + path)
}

// splitRemote separates a remote into host and repository path, accepting
// URL forms (scheme://[user@]host[:port]/path) and scp-like ssh forms
// ([user@]host:path).
func splitRemote(remote string) (host, path string) {
	if strings.Contains(remote, "://") {
		parsed, err := url.Parse(remote)
		if err == nil {
			if parsed.Scheme == "file" {
				return "", parsed.Path
			}
			return parsed.Hostname(), parsed.Path
		}
		// A URL the standard parser refuses (an ssh port written as
		// host:owner/repo after the scheme, say) falls through to the
		// scp-like reading without its scheme.
		remote = remote[strings.Index(remote, "://")+3:]
	}
	if at := strings.LastIndex(remote, "@"); at >= 0 && !strings.Contains(remote[:at], "/") {
		remote = remote[at+1:]
	}
	if colon := strings.Index(remote, ":"); colon > 0 && !strings.Contains(remote[:colon], "/") {
		return remote[:colon], remote[colon+1:]
	}
	if slash := strings.Index(remote, "/"); slash > 0 && strings.Contains(remote[:slash], ".") {
		return remote[:slash], remote[slash+1:]
	}
	return "", remote
}
