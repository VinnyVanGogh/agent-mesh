package checkpoint

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// BenchmarkCreateCheckpoint_DirtyTree measures checkpoint latency on a realistic dirty repository
// with 50 modified files and 10 untracked files.
func BenchmarkCreateCheckpoint_DirtyTree(b *testing.B) {
	tempDir, err := os.MkdirTemp("", "mesh-bench-cp-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	runGit(ctx, tempDir, nil, "init")
	runGit(ctx, tempDir, nil, "config", "user.name", "Bench User")
	runGit(ctx, tempDir, nil, "config", "user.email", "bench@mesh.local")

	// Create 50 base tracked files
	for i := 0; i < 50; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("tracked_file_%03d.go", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("package main\n// version 1 of file %d\n", i)), 0644)
	}
	runGit(ctx, tempDir, nil, "add", "-A")
	runGit(ctx, tempDir, nil, "commit", "-m", "initial commit with 50 files")

	// Now modify all 50 tracked files
	for i := 0; i < 50; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("tracked_file_%03d.go", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("package main\n// version 2 modified file %d\nfunc F%d() {}\n", i, i)), 0644)
	}

	// Create 10 untracked files
	for i := 0; i < 10; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("untracked_file_%03d.txt", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("untracked artifact %d\n", i)), 0644)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		opts := CreateOptions{
			WorkDir:   tempDir,
			SessionID: "bench-session",
			Message:   fmt.Sprintf("checkpoint %d", i),
			Timeout:   5 * time.Second,
		}
		cp, err := CreateCheckpoint(ctx, opts)
		if err != nil {
			b.Fatalf("checkpoint failed: %v", err)
		}
		if cp == nil || cp.CommitSHA == "" {
			b.Fatalf("checkpoint returned empty result")
		}
	}
}

// TestBenchmarkCheckpointDistribution measures and prints the actual distribution (min, p50, p95, max)
func TestBenchmarkCheckpointDistribution(t *testing.T) {
	tempDir, err := os.MkdirTemp("", "mesh-dist-cp-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	ctx := context.Background()
	runGit(ctx, tempDir, nil, "init")
	runGit(ctx, tempDir, nil, "config", "user.name", "Bench User")
	runGit(ctx, tempDir, nil, "config", "user.email", "bench@mesh.local")

	// 50 tracked files
	for i := 0; i < 50; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("tracked_%03d.go", i))
		_ = os.WriteFile(path, []byte("package main\n"), 0644)
	}
	runGit(ctx, tempDir, nil, "add", "-A")
	runGit(ctx, tempDir, nil, "commit", "-m", "init")

	// Modify 50 files
	for i := 0; i < 50; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("tracked_%03d.go", i))
		_ = os.WriteFile(path, []byte(fmt.Sprintf("package main\nfunc F%d() {}\n", i)), 0644)
	}
	// 10 untracked files
	for i := 0; i < 10; i++ {
		path := filepath.Join(tempDir, fmt.Sprintf("untracked_%03d.txt", i))
		_ = os.WriteFile(path, []byte("data\n"), 0644)
	}

	var samples []time.Duration
	iterations := 20
	for i := 0; i < iterations; i++ {
		start := time.Now()
		opts := CreateOptions{
			WorkDir:   tempDir,
			SessionID: "dist-session",
			Message:   fmt.Sprintf("cp-%d", i),
		}
		_, err := CreateCheckpoint(ctx, opts)
		if err != nil {
			t.Fatalf("checkpoint failed: %v", err)
		}
		samples = append(samples, time.Since(start))
	}

	// Sort samples
	for i := 0; i < len(samples); i++ {
		for j := i + 1; j < len(samples); j++ {
			if samples[i] > samples[j] {
				samples[i], samples[j] = samples[j], samples[i]
			}
		}
	}

	minD := samples[0]
	medianD := samples[len(samples)/2]
	p95D := samples[int(float64(len(samples))*0.95)]
	maxD := samples[len(samples)-1]

	var sum time.Duration
	for _, d := range samples {
		sum += d
	}
	avgD := sum / time.Duration(len(samples))

	t.Logf("CHECKPOINT DISTRIBUTION (50 modified, 10 untracked): min=%v, median=%v, p95=%v, max=%v, avg=%v",
		minD.Round(time.Millisecond), medianD.Round(time.Millisecond),
		p95D.Round(time.Millisecond), maxD.Round(time.Millisecond), avgD.Round(time.Millisecond))
}
