package condenser

import (
	"fmt"
	"strings"
	"testing"
)

// BenchmarkCondenserReduction evaluates reduction percentage and throughput across
// realistic compiler error cascade fixtures (TypeScript, Go panic, Python traceback, Generic repeated output).
func BenchmarkCondenserReduction(b *testing.B) {
	fixtures := []struct {
		name   string
		format Format
		input  string
	}{
		{
			name:   "TypeScript-50-Errors",
			format: FormatTypeScript,
			input: func() string {
				var sb strings.Builder
				for i := 0; i < 50; i++ {
					sb.WriteString(fmt.Sprintf("src/views/item_%d.tsx:%d:5 - error TS2339: Property 'id' does not exist on type 'ItemRecord'.\n", i, 10+i))
					sb.WriteString("    const id = item.id;\n")
					sb.WriteString("               ~~~~\n")
				}
				for i := 0; i < 20; i++ {
					sb.WriteString(fmt.Sprintf("src/types/data_%d.ts:%d:1 - error TS2322: Type 'string' is not assignable to type 'number'.\n", i, 20+i))
					sb.WriteString("    const count: number = \"0\";\n")
					sb.WriteString("          ~~~~~\n")
				}
				return sb.String()
			}(),
		},
		{
			name:   "Go-Panic-Deep-Goroutines",
			format: FormatGo,
			input: func() string {
				var sb strings.Builder
				sb.WriteString("panic: runtime error: invalid memory address or nil pointer dereference\n")
				sb.WriteString("[signal SIGSEGV: code=0x2 addr=0x0 pc=0x104b2a8d4]\n\n")
				sb.WriteString("goroutine 1 [running]:\n")
				sb.WriteString("github.com/VinnyVanGogh/staypoint/internal/db.Query(0x0)\n")
				sb.WriteString("\t/Users/tester/dev/agent-mesh/internal/db/db.go:42 +0x24\n")
				sb.WriteString("runtime.panicmem(...)\n")
				sb.WriteString("\t/usr/local/go/src/runtime/panic.go:260\n")
				sb.WriteString("main.main()\n")
				sb.WriteString("\t/Users/tester/dev/agent-mesh/cmd/mesh/main.go:12 +0x30\n\n")
				for g := 2; g <= 40; g++ {
					sb.WriteString(fmt.Sprintf("goroutine %d [chan receive]:\n", g))
					sb.WriteString("runtime.gopark(0x104d8c890, 0x1400010c0b8, 0xb, 0x17)\n")
					sb.WriteString("\t/usr/local/go/src/runtime/proc.go:420\n")
					sb.WriteString("runtime.chanrecv(0x1400010c060, 0x0, 0x1)\n")
					sb.WriteString("\t/usr/local/go/src/runtime/chan.go:640\n\n")
				}
				return sb.String()
			}(),
		},
		{
			name:   "Python-Deep-Traceback",
			format: FormatPython,
			input: func() string {
				var sb strings.Builder
				sb.WriteString("Traceback (most recent call last):\n")
				sb.WriteString("  File \"entrypoint.py\", line 15, in <module>\n    app.run()\n")
				for i := 0; i < 25; i++ {
					sb.WriteString(fmt.Sprintf("  File \"/usr/local/lib/python3.11/site-packages/pkg_%d/mod.py\", line %d, in dispatch\n", i, 100+i))
					sb.WriteString("    return next_handler(req)\n")
				}
				sb.WriteString("  File \"src/handlers/auth.py\", line 45, in verify\n    raise ValueError(\"Unauthorized token\")\n")
				sb.WriteString("ValueError: Unauthorized token\n")
				return sb.String()
			}(),
		},
		{
			name:   "Generic-Repeated-Logs",
			format: FormatGeneric,
			input: func() string {
				var sb strings.Builder
				for i := 0; i < 100; i++ {
					sb.WriteString("\x1b[31m[ERROR]\x1b[0m Connection to redis host 10.0.0.1:6379 failed: connection refused\n")
				}
				sb.WriteString("Fatal error: max retries reached\n")
				return sb.String()
			}(),
		},
	}

	for _, tc := range fixtures {
		res, err := Condense(tc.input, CondenseOptions{
			Format:      tc.format,
			ShowSavings: false,
		})
		if err != nil {
			b.Fatalf("failed to condense %s: %v", tc.name, err)
		}
		rawBytes := len([]byte(tc.input))
		condensedBytes := len([]byte(res.Condensed))
		reductionPct := 100.0 * (1.0 - float64(condensedBytes)/float64(rawBytes))
		b.Logf("[%s] Raw: %d bytes -> Condensed: %d bytes | Reduction: %.1f%%",
			tc.name, rawBytes, condensedBytes, reductionPct)

		b.Run(tc.name, func(b *testing.B) {
			b.ResetTimer()
			b.SetBytes(int64(rawBytes))
			for i := 0; i < b.N; i++ {
				_, _ = Condense(tc.input, CondenseOptions{
					Format:      tc.format,
					ShowSavings: false,
				})
			}
		})
	}
}
