package reporting

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/VinnyVanGogh/staypoint/internal/config"
)

// BenchmarkPDFGeneration benchmarks generating an executive summary PDF via headless Chrome
// using chromedp. Reports total elapsed wall time and operations on the host machine.
func BenchmarkPDFGeneration(b *testing.B) {
	if !isChromeInstalled() {
		b.Skip("Google Chrome / Chromium not installed; skipping PDF benchmark")
	}

	tempDir, err := os.MkdirTemp("", "pdf-bench-*")
	if err != nil {
		b.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tempDir)

	dbPath := filepath.Join(tempDir, "bench_telemetry.db")
	cfg := &config.Config{
		TelemetryDBPath: dbPath,
		WorkEmail:       "engineer@company.com",
		PersonalEmail:   "engineer@personal.com",
		CompanyName:     "Benchmark Corp",
		EngineerName:    "Benchmarking Engineer",
		HourlyRate:      150.0,
	}

	// Warmup / initial render to prime Chrome instance and verify generation works
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	warmupOutput := filepath.Join(tempDir, "warmup.pdf")
	start := time.Now()
	if err := RenderReport(ctx, "work", cfg, warmupOutput); err != nil {
		b.Fatalf("PDF rendering failed: %v", err)
	}
	warmupElapsed := time.Since(start)
	b.Logf("Initial single PDF render elapsed wall time: %v", warmupElapsed)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		outPath := filepath.Join(tempDir, "test.pdf")
		if err := RenderReport(ctx, "work", cfg, outPath); err != nil {
			b.Fatalf("RenderReport failed at iteration %d: %v", i, err)
		}
	}
}
