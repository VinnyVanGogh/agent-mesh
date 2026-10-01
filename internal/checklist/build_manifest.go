package checklist

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// BuildManifest records, at build time, which commits are compiled into the
// installed staypointd. scripts/reinstall-daemon.sh writes it next to the auth
// token.
//
// The commit gate reads this instead of shelling out to git. Under launchd,
// macOS blocks reads of ~/Documents (where the repo lives) until staypointd is
// granted Documents access, and a blocked git does not fail: it hangs until the
// timeout kills it. Every commit then looked "missing" and the checklist page
// took ~25s to load.
type BuildManifest struct {
	Commit  string `json:"commit"`
	FullSHA string `json:"full_sha"`
	// InMain is whether FullSHA was an ancestor of origin/main when built.
	// main only moves forward, so a true value stays true.
	InMain  bool   `json:"in_main"`
	MainSHA string `json:"main_sha"`
	// Dirty means the build included uncommitted changes to tracked files.
	Dirty     bool     `json:"dirty"`
	BuiltAt   string   `json:"built_at"`
	Ancestors []string `json:"ancestors"`
}

// BuildManifestPath returns where reinstall-daemon.sh writes the manifest.
func BuildManifestPath() string {
	if p := os.Getenv("STAYPOINT_BUILD_MANIFEST"); p != "" {
		return p
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".staypoint", "build-manifest.json")
}

// LoadBuildManifest reads and validates a manifest file.
func LoadBuildManifest(path string) (*BuildManifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m BuildManifest
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if m.FullSHA == "" || len(m.Ancestors) == 0 {
		return nil, fmt.Errorf("%s has no full_sha or ancestors", path)
	}
	for i, a := range m.Ancestors {
		m.Ancestors[i] = strings.ToLower(strings.TrimSpace(a))
	}
	sort.Strings(m.Ancestors)
	return &m, nil
}

// Contains reports whether sha (full or abbreviated) is reachable from the
// build commit.
func (m *BuildManifest) Contains(sha string) bool {
	sha = strings.ToLower(strings.TrimSpace(sha))
	if len(sha) < 7 {
		return false
	}
	i := sort.SearchStrings(m.Ancestors, sha)
	return i < len(m.Ancestors) && strings.HasPrefix(m.Ancestors[i], sha)
}

// Describes reports whether this manifest was written for the given running
// binary commit, so a stale manifest from an older build is never trusted.
func (m *BuildManifest) Describes(binaryCommit string) bool {
	c := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(binaryCommit), "-dirty"))
	return len(c) >= 7 && strings.HasPrefix(strings.ToLower(m.FullSHA), c)
}

var manifestCache struct {
	sync.Mutex
	path    string
	modTime time.Time
	m       *BuildManifest
}

// manifestFor returns the installed build manifest if it describes
// binaryCommit, re-reading the file only when it changes.
func manifestFor(binaryCommit string) *BuildManifest {
	path := BuildManifestPath()
	if path == "" {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil
	}
	manifestCache.Lock()
	defer manifestCache.Unlock()
	if manifestCache.path != path || !manifestCache.modTime.Equal(info.ModTime()) {
		m, err := LoadBuildManifest(path)
		if err != nil {
			m = nil
		}
		manifestCache.path, manifestCache.modTime, manifestCache.m = path, info.ModTime(), m
	}
	if manifestCache.m == nil || !manifestCache.m.Describes(binaryCommit) {
		return nil
	}
	return manifestCache.m
}

// checkCommitAgainstManifest answers the gate from build-time data alone.
func checkCommitAgainstManifest(m *BuildManifest, commitSHA string) CommitStatus {
	status := CommitStatus{Commit: commitSHA}
	if !m.Contains(commitSHA) {
		status.MissingFromBinary = true
		status.Reason = fmt.Sprintf("commit %s is not compiled into the running staypointd (built from %s). Merge it to main, then run scripts/reinstall-daemon.sh", commitSHA, m.Commit)
		return status
	}
	status.InBinary = true
	status.InMain = m.InMain
	if !m.InMain {
		status.MissingFromMain = true
		status.Reason = fmt.Sprintf("running staypointd was built from %s, which is not in main. Rebuild from main with scripts/reinstall-daemon.sh", m.Commit)
		return status
	}
	if m.Dirty {
		status.Reason = fmt.Sprintf("running staypointd was built from %s plus uncommitted changes. Commit or discard them, then run scripts/reinstall-daemon.sh", m.Commit)
		return status
	}
	status.Valid = true
	return status
}
