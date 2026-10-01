package checklist_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/VinnyVanGogh/staypoint/internal/checklist"
)

const (
	buildSHA  = "8859508aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	olderSHA  = "f64df3ebbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	strayDir  = "/nonexistent-staypoint-repo"
	shortSHA  = "8859508"
	notBuilt  = "deadbee"
	olderShrt = "f64df3e"
)

func writeManifest(t *testing.T, m checklist.BuildManifest) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "build-manifest.json")
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("STAYPOINT_BUILD_MANIFEST", path)
}

func items(commits ...string) []checklist.Item {
	var out []checklist.Item
	for i, c := range commits {
		out = append(out, checklist.Item{ID: "item-" + c, Section: "S" + string(rune('A'+i)), CommitHash: c})
	}
	return out
}

func TestVerifyCommitsUsesManifestWithoutRepoAccess(t *testing.T) {
	writeManifest(t, checklist.BuildManifest{
		Commit: shortSHA, FullSHA: buildSHA, InMain: true, MainSHA: shortSHA,
		Ancestors: []string{buildSHA, olderSHA},
	})

	res, err := checklist.VerifyCommits(context.Background(), strayDir, shortSHA, items(olderShrt, shortSHA))
	if err != nil {
		t.Fatal(err)
	}
	if !res.Verified || res.ValidCommits != 2 || res.MainCommit != shortSHA {
		t.Fatalf("expected both commits verified from manifest, got %+v", res)
	}
}

func TestVerifyCommitsManifestBlocksCommitNotInBuild(t *testing.T) {
	writeManifest(t, checklist.BuildManifest{
		Commit: shortSHA, FullSHA: buildSHA, InMain: true, Ancestors: []string{buildSHA},
	})

	res, _ := checklist.VerifyCommits(context.Background(), strayDir, shortSHA, items(notBuilt))
	if res.Verified || len(res.MissingCommits) != 1 {
		t.Fatalf("expected %s blocked, got %+v", notBuilt, res)
	}
	w := res.MissingCommits[0]
	if !w.MissingFromBinary || !strings.Contains(w.Reason, "reinstall-daemon.sh") {
		t.Fatalf("expected rebuild guidance, got %+v", w)
	}
}

func TestVerifyCommitsManifestBlocksUnmergedOrDirtyBuild(t *testing.T) {
	for name, m := range map[string]checklist.BuildManifest{
		"unmerged": {Commit: shortSHA, FullSHA: buildSHA, InMain: false, Ancestors: []string{buildSHA}},
		"dirty":    {Commit: shortSHA, FullSHA: buildSHA, InMain: true, Dirty: true, Ancestors: []string{buildSHA}},
	} {
		t.Run(name, func(t *testing.T) {
			writeManifest(t, m)
			res, _ := checklist.VerifyCommits(context.Background(), strayDir, shortSHA, items(shortSHA))
			if res.Verified {
				t.Fatalf("expected %s build to block the gate, got %+v", name, res)
			}
		})
	}
}

func TestVerifyCommitsIgnoresManifestForOtherBuild(t *testing.T) {
	repoDir, c1, _ := setupTestGitRepo(t)
	writeManifest(t, checklist.BuildManifest{
		Commit: shortSHA, FullSHA: buildSHA, InMain: true, Ancestors: []string{buildSHA},
	})

	// Running binary is c1, not the manifest's build: fall back to git.
	res, _ := checklist.VerifyCommits(context.Background(), repoDir, c1, items(c1))
	if !res.Verified {
		t.Fatalf("expected git fallback to verify %s, got %+v", c1, res)
	}
}

func TestVerifyCommitsUnreadableRepoSaysSoInsteadOfMissing(t *testing.T) {
	t.Setenv("STAYPOINT_BUILD_MANIFEST", filepath.Join(t.TempDir(), "absent.json"))
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".git"), []byte("gitdir: /nonexistent\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	res, _ := checklist.VerifyCommits(context.Background(), dir, shortSHA, items(olderShrt))
	if res.Verified || len(res.MissingCommits) != 1 {
		t.Fatalf("expected blocked, got %+v", res)
	}
	reason := res.MissingCommits[0].Reason
	if strings.Contains(reason, "does not exist") || !strings.Contains(reason, "cannot verify") {
		t.Fatalf("expected an honest cannot-verify reason, got %q", reason)
	}
}
