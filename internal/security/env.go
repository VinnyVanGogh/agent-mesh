package security

import (
	"os"
	"strings"
)

// baseEnvAllow are variable names safe to hand to any child process.
var baseEnvAllow = map[string]bool{
	"PATH": true, "HOME": true, "USER": true, "LOGNAME": true, "SHELL": true,
	"TERM": true, "COLORTERM": true, "LANG": true, "LANGUAGE": true, "TZ": true,
	"TMPDIR": true, "TEMP": true, "TMP": true, "PWD": true, "EDITOR": true,
	"NO_COLOR": true, "CI": true,
	// Windows essentials.
	"SYSTEMROOT": true, "SYSTEMDRIVE": true, "USERPROFILE": true, "APPDATA": true,
	"LOCALAPPDATA": true, "PATHEXT": true, "COMSPEC": true, "WINDIR": true,
}

var baseEnvAllowPrefix = []string{"LC_", "XDG_", "GIT_"}

// secretNameHints mark a variable as secret-bearing even when an allowed prefix matches.
var secretNameHints = []string{"TOKEN", "SECRET", "PASSWORD", "PASSWD", "CREDENTIAL", "PRIVATE", "API_KEY", "APIKEY", "ACCESS_KEY", "AUTHORIZATION"}

func looksSecret(name string) bool {
	up := strings.ToUpper(name)
	for _, h := range secretNameHints {
		if strings.Contains(up, h) {
			return true
		}
	}
	return strings.HasSuffix(up, "_KEY")
}

// SanitizeEnv filters environ (KEY=VALUE entries) down to a safe allowlist.
// Anything else, including cloud and master tokens, is dropped. extra names
// variables a specific task legitimately needs (e.g. ANTHROPIC_API_KEY for a
// provider CLI); those pass through if present. Order is preserved.
func SanitizeEnv(environ []string, extra ...string) []string {
	want := make(map[string]bool, len(extra))
	for _, n := range extra {
		want[n] = true
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, ok := strings.Cut(kv, "=")
		if !ok || name == "" {
			continue
		}
		if want[name] {
			out = append(out, kv)
			continue
		}
		if looksSecret(name) {
			continue
		}
		if baseEnvAllow[name] {
			out = append(out, kv)
			continue
		}
		for _, p := range baseEnvAllowPrefix {
			if strings.HasPrefix(name, p) {
				out = append(out, kv)
				break
			}
		}
	}
	return out
}

// ChildEnv is SanitizeEnv applied to the current process environment. Use it
// instead of os.Environ() whenever spawning a child process.
func ChildEnv(extra ...string) []string { return SanitizeEnv(os.Environ(), extra...) }
