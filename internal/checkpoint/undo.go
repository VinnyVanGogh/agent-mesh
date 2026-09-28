package checkpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Undo restores the working tree and index to a prior checkpoint.
// A safety pre-undo snapshot is always created first unless explicitly disabled.
func Undo(ctx context.Context, opts UndoOptions) (*UndoResult, error) {
	workDir := opts.WorkDir
	if workDir == "" {
		var err error
		workDir, err = os.Getwd()
		if err != nil {
			return nil, fmt.Errorf("failed to get current directory: %w", err)
		}
	}

	rootDir, _, err := getGitPaths(ctx, workDir)
	if err != nil {
		return nil, err
	}

	// 1. Resolve target checkpoint
	targetRef := opts.CheckpointID
	if targetRef == "" || targetRef == "latest" {
		targetRef = "refs/staypoint/checkpoints/latest"
	} else if !strings.HasPrefix(targetRef, "refs/") && len(targetRef) != 40 {
		// Look up by ID
		matchingRefs, err := runGit(ctx, rootDir, nil, "for-each-ref", "--format=%(refname) %(objectname)", fmt.Sprintf("refs/staypoint/checkpoints/*/%s", targetRef))
		if err == nil && len(strings.TrimSpace(matchingRefs)) > 0 {
			lines := strings.Split(strings.TrimSpace(matchingRefs), "\n")
			targetRef = strings.Fields(lines[0])[0]
		}
	}

	targetSHA, err := runGit(ctx, rootDir, nil, "rev-parse", targetRef)
	if err != nil {
		return nil, fmt.Errorf("checkpoint not found: %s", opts.CheckpointID)
	}

	targetMsg, _ := runGit(ctx, rootDir, nil, "log", "-1", "--format=%s", targetSHA)
	targetCP := Checkpoint{
		ID:        opts.CheckpointID,
		CommitSHA: targetSHA,
		Message:   targetMsg,
		Ref:       targetRef,
	}

	// 2. Compute diffstat against current working directory
	diffStat, _ := runGit(ctx, rootDir, nil, "diff", targetSHA, "--stat")

	// 3. Find modified files between target and working tree
	diffFilesOut, _ := runGit(ctx, rootDir, nil, "diff", targetSHA, "--name-only")
	var filesReverted []string
	for _, f := range strings.Split(strings.TrimSpace(diffFilesOut), "\n") {
		if strings.TrimSpace(f) != "" {
			filesReverted = append(filesReverted, strings.TrimSpace(f))
		}
	}

	// 4. Find untracked files
	untrackedOut, _ := runGit(ctx, rootDir, nil, "status", "--porcelain")
	var filesRemoved []string
	for _, l := range strings.Split(strings.TrimSpace(untrackedOut), "\n") {
		if strings.HasPrefix(l, "?? ") {
			f := strings.TrimSpace(strings.TrimPrefix(l, "?? "))
			if f != "" {
				filesRemoved = append(filesRemoved, f)
			}
		}
	}

	// 5. Find untracked ignored files and directories if requested, protecting pre-existing files
	var filesIgnoredRemoved []string
	if opts.CleanIgnored {
		baselineSet := make(map[string]bool)
		commitBody, _ := runGit(ctx, rootDir, nil, "log", "-1", "--format=%B", targetSHA)
		if idx := strings.Index(commitBody, "Staypoint-Baseline-Ignored:\n"); idx != -1 {
			lines := strings.Split(commitBody[idx+len("Staypoint-Baseline-Ignored:\n"):], "\n")
			for _, line := range lines {
				line = strings.TrimSpace(line)
				if line != "" {
					baselineSet[line] = true
				}
			}
		}

		ignoredOut, _ := runGit(ctx, rootDir, nil, "status", "--porcelain", "--ignored")
		for _, l := range strings.Split(strings.TrimSpace(ignoredOut), "\n") {
			if strings.HasPrefix(l, "!! ") {
				f := strings.TrimSpace(strings.TrimPrefix(l, "!! "))
				f = strings.Trim(f, "\"")
				if f == "" {
					continue
				}
				// Never delete files that were already present when the target checkpoint was created
				if baselineSet[f] {
					continue
				}
				// Protect common environment configuration files
				baseName := filepath.Base(f)
				if strings.HasPrefix(baseName, ".env") || strings.HasSuffix(baseName, ".key") || strings.HasSuffix(baseName, ".pem") {
					continue
				}
				filesIgnoredRemoved = append(filesIgnoredRemoved, f)
			}
		}
	}

	// If dry run, return preview
	if opts.DryRun {
		return &UndoResult{
			RestoredTo:          targetCP,
			FilesReverted:       filesReverted,
			FilesRemoved:        filesRemoved,
			FilesIgnoredRemoved: filesIgnoredRemoved,
			DiffStat:            diffStat,
		}, nil
	}

	// 6. Create Safety Pre-Undo Checkpoint (unless explicitly disabled)
	var safetyCP *Checkpoint
	if !opts.DisableSafetyCP {
		safetySession := opts.SessionID
		if safetySession == "" {
			safetySession = "safety"
		}
		safetyOpts := CreateOptions{
			WorkDir:   rootDir,
			SessionID: safetySession,
			Message:   fmt.Sprintf("Pre-undo safety snapshot before reverting to %s", targetCP.ID),
		}
		safetyCP, _ = CreateCheckpoint(ctx, safetyOpts)
		if safetyCP != nil {
			// Update special pre-undo ref
			_, _ = runGit(ctx, rootDir, nil, "update-ref", "refs/staypoint/checkpoints/pre-undo", safetyCP.CommitSHA)
		}
	}

	// 7. Restore index and working tree to target commit across the entire repository
	if _, err := runGit(ctx, rootDir, nil, "checkout", targetSHA, "--", ":/"); err != nil {
		return nil, fmt.Errorf("failed to restore working tree: %w", err)
	}

	// 8. Clean newly created untracked files if requested
	if !opts.KeepUntracked {
		for _, f := range filesRemoved {
			fullPath := filepath.Join(rootDir, f)
			_ = os.RemoveAll(fullPath)
		}
	}

	// 9. Clean untracked ignored files and directories if requested
	if opts.CleanIgnored {
		for _, f := range filesIgnoredRemoved {
			fullPath := filepath.Join(rootDir, f)
			_ = os.RemoveAll(fullPath)
		}
	}

	return &UndoResult{
		RestoredTo:          targetCP,
		SafetyCP:            safetyCP,
		FilesReverted:       filesReverted,
		FilesRemoved:        filesRemoved,
		FilesIgnoredRemoved: filesIgnoredRemoved,
		DiffStat:            diffStat,
	}, nil
}

// Redo restores the state prior to the last undo operation using the pre-undo safety snapshot.
func Redo(ctx context.Context, workDir, sessionID string) (*UndoResult, error) {
	return Undo(ctx, UndoOptions{
		WorkDir:         workDir,
		SessionID:       sessionID,
		CheckpointID:    "refs/staypoint/checkpoints/pre-undo",
		DisableSafetyCP: true,
	})
}
