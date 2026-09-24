package router

import (
	"bytes"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// BenchmarkRenderStatusline measures the internal rendering latency of the statusline.
// It includes config reading, git info, and string assembly, but excludes binary process startup.
func BenchmarkRenderStatusline(b *testing.B) {
	var buf bytes.Buffer
	emptyInput := strings.NewReader("")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		buf.Reset()
		_ = RenderStatusline(&buf, emptyInput)
	}
}

// TestBenchmarkStatuslineProcessLatency measures total end-to-end CLI execution time
// including process fork/exec, Go runtime startup, and stdout write.
func TestBenchmarkStatuslineProcessLatency(t *testing.T) {
	meshBin := "../../bin/mesh"
	iterations := 20
	var samples []time.Duration

	for i := 0; i < iterations; i++ {
		start := time.Now()
		cmd := exec.Command(meshBin, "statusline")
		if err := cmd.Run(); err != nil {
			t.Fatalf("failed to run mesh statusline: %v", err)
		}
		samples = append(samples, time.Since(start))
	}

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

	t.Logf("CLI STATUSLINE PROCESS LATENCY (end-to-end binary fork): min=%v, median=%v, p95=%v, max=%v, avg=%v",
		minD, medianD, p95D, maxD, avgD)
}
