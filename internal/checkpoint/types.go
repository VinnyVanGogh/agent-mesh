package checkpoint

import (
	"time"
)

// Checkpoint represents metadata for an ephemeral micro-snapshot.
type Checkpoint struct {
	ID        string        `json:"id"`
	SessionID string        `json:"session_id"`
	CommitSHA string        `json:"commit_sha"`
	TreeSHA   string        `json:"tree_sha"`
	ParentSHA string        `json:"parent_sha,omitempty"`
	Timestamp time.Time     `json:"timestamp"`
	Message   string        `json:"message"`
	Duration  time.Duration `json:"duration"`
	FileCount int           `json:"file_count"`
	Ref       string        `json:"ref"`
}

// CreateOptions configures checkpoint generation.
type CreateOptions struct {
	WorkDir          string        // Working directory (default: current dir)
	SessionID        string        // Agent session identifier (optional)
	Message          string        // Note/label
	ExcludeUntracked bool          // If true, only stage tracked files (default: false, includes untracked)
	Timeout          time.Duration // Context timeout (default: 5s)
}

// UndoOptions configures tree restoration.
type UndoOptions struct {
	WorkDir         string
	SessionID       string
	CheckpointID    string // Specific ID or empty for most recent
	KeepUntracked   bool   // If true, do not delete files created after checkpoint
	CleanIgnored    bool   // If true, remove untracked ignored files and directories
	DryRun          bool   // If true, preview diff without modifying disk
	DisableSafetyCP bool   // If true, do not create pre-undo snapshot (default false = safety enabled)
}

// FileDiffStat holds per-file change counts between a checkpoint and the working tree.
type FileDiffStat struct {
	Path    string `json:"path"`
	Added   int    `json:"added"`
	Removed int    `json:"removed"`
}

// UndoResult describes the changes rolled back.
type UndoResult struct {
	RestoredTo          Checkpoint  `json:"restored_to"`
	SafetyCP            *Checkpoint `json:"safety_checkpoint,omitempty"`
	FilesReverted       []string    `json:"files_reverted"`
	FilesRemoved        []string    `json:"files_removed"`
	FilesIgnoredRemoved []string    `json:"files_ignored_removed,omitempty"`
	DiffStat            string      `json:"diff_stat"`
}
