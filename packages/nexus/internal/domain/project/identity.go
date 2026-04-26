package project

import (
	"fmt"
	"net/url"
	"path/filepath"
	"strings"
)

// NormalizeRepoURL returns a canonical comparison key for project/workspace repo paths.
func NormalizeRepoURL(repo string) string {
	r := strings.TrimSpace(repo)
	r = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git"))
	if r == "" {
		return ""
	}
	// Normalize common URL forms to host/path (without scheme), so
	// https://github.com/org/repo(.git) and github.com/org/repo converge.
	if strings.Contains(r, "://") {
		if u, err := url.Parse(r); err == nil && u.Host != "" {
			host := strings.ToLower(strings.TrimSpace(u.Host))
			path := strings.Trim(strings.TrimSuffix(u.Path, ".git"), "/")
			if path != "" {
				r = host + "/" + path
			}
		}
	}
	// Normalize SCP-like SSH URL: git@github.com:org/repo(.git)
	if at := strings.IndexByte(r, '@'); at >= 0 {
		rest := r[at+1:]
		if colon := strings.IndexByte(rest, ':'); colon > 0 {
			host := strings.ToLower(strings.TrimSpace(rest[:colon]))
			path := strings.Trim(strings.TrimSuffix(rest[colon+1:], ".git"), "/")
			if host != "" && path != "" {
				r = host + "/" + path
			}
		}
	}
	r = strings.TrimSpace(strings.TrimSuffix(strings.TrimSuffix(r, "/"), ".git"))
	return r
}

// DeriveIDFromRepo generates a deterministic project id from repository path/URL.
func DeriveIDFromRepo(repoURL string) string {
	key := NormalizeRepoURL(repoURL)
	h := uint32(2166136261)
	for i := 0; i < len(key); i++ {
		h ^= uint32(key[i])
		h *= 16777619
	}
	return fmt.Sprintf("proj-%08x", h)
}

// InferNameFromRepo chooses a human-readable project name from repo path/URL.
func InferNameFromRepo(repoURL string) string {
	r := NormalizeRepoURL(repoURL)
	if r == "" {
		return "project"
	}
	// Normalize separators so path.Base can extract a stable final segment.
	clean := strings.ReplaceAll(r, "\\", "/")
	name := filepath.Base(clean)
	name = strings.TrimSpace(name)
	if name == "." || name == "/" || name == "" {
		return "project"
	}
	return name
}
