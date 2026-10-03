package security

import (
	"os"
	"path/filepath"
	"strings"
)

// Tier is a command risk class.
type Tier int

const (
	// Green is read-only.
	Green Tier = iota
	// Yellow is a local edit; safe to run under an auto-checkpoint.
	Yellow
	// Red is destructive, secret-touching or exfiltrating; needs human confirmation.
	Red
)

func (t Tier) String() string {
	switch t {
	case Green:
		return "green"
	case Yellow:
		return "yellow"
	default:
		return "red"
	}
}

// Verdict is the classification of a command line.
type Verdict struct {
	Tier    Tier
	Reasons []string
}

func (v *Verdict) raise(t Tier, reason string) {
	if t > v.Tier {
		v.Tier = t
	}
	if t == Red {
		v.Reasons = append(v.Reasons, reason)
	}
}

func (v *Verdict) merge(o Verdict) {
	if o.Tier > v.Tier {
		v.Tier = o.Tier
	}
	v.Reasons = append(v.Reasons, o.Reasons...)
}

// Classifier assigns tiers. Worktree, when non-nil, additionally makes any
// path argument that resolves outside it Red.
type Classifier struct {
	Worktree *Boundary
	Home     string // defaults to the user's home directory
}

// Classify classifies a shell command line. Unparseable input is Red (fail closed).
func (c *Classifier) Classify(line string) Verdict {
	return c.classifyLine(line, 0)
}

// ClassifyArgv classifies an already-split command (no shell involved).
func (c *Classifier) ClassifyArgv(argv []string) Verdict {
	var v Verdict
	c.classifySegment(segment{argv: argv}, &v, 0)
	return v
}

const maxDepth = 8

func (c *Classifier) classifyLine(line string, depth int) Verdict {
	var v Verdict
	if depth > maxDepth {
		v.raise(Red, "command nesting too deep to analyse")
		return v
	}
	segs, subs, err := parseShell(line)
	if err != nil {
		v.raise(Red, "unparseable command: "+err.Error())
		return v
	}
	for _, s := range subs {
		v.merge(c.classifyLine(s, depth+1))
	}
	for i, s := range segs {
		c.classifySegment(s, &v, depth)
		// remote-shell pipe: anything | sh
		if i > 0 && segs[i-1].piped {
			if name := baseCmd(stripPrefixes(s.argv)); shells[name] {
				v.raise(Red, "piping into a shell ("+name+")")
			}
		}
	}
	return v
}

var shells = map[string]bool{"sh": true, "bash": true, "zsh": true, "dash": true, "ksh": true, "fish": true}

var greenCmds = map[string]bool{
	"ls": true, "cat": true, "head": true, "tail": true, "wc": true, "grep": true, "egrep": true,
	"fgrep": true, "rg": true, "pwd": true, "echo": true, "printf": true, "which": true, "type": true,
	"file": true, "stat": true, "du": true, "df": true, "tree": true, "sort": true, "uniq": true,
	"cut": true, "tr": true, "diff": true, "cmp": true, "jq": true, "true": true, "false": true,
	"date": true, "basename": true, "dirname": true, "realpath": true, "readlink": true, "test": true,
	"[": true, "whoami": true, "uname": true, "id": true, "nl": true, "column": true, "less": true,
	"more": true, "sleep": true, "seq": true, "expr": true, "md5sum": true, "shasum": true, "sha256sum": true,
	"awk": true, "sed": true, "find": true, "xargs": true, "cd": true,
}

var greenGit = map[string]bool{
	"status": true, "log": true, "diff": true, "show": true, "rev-parse": true, "ls-files": true,
	"blame": true, "describe": true, "shortlog": true, "grep": true, "ls-tree": true, "cat-file": true,
	"rev-list": true, "remote": true, "branch": true, "tag": true, "config": true, "fetch": true,
}

var greenGo = map[string]bool{"version": true, "list": true, "vet": true, "env": true, "doc": true}

var alwaysRed = map[string]string{
	"sudo": "privilege escalation", "doas": "privilege escalation", "su": "privilege escalation",
	"nc": "raw network access", "ncat": "raw network access", "netcat": "raw network access",
	"socat": "raw network access", "telnet": "raw network access", "ftp": "raw network access",
	"scp": "remote copy", "sftp": "remote copy", "ssh": "remote shell", "rsync": "remote copy",
	"mkfs": "filesystem format", "dd": "raw disk write", "shutdown": "system power", "reboot": "system power",
	"halt": "system power", "poweroff": "system power", "crontab": "persistent scheduler",
	"launchctl": "service manager", "systemctl": "service manager", "chroot": "chroot",
	"security": "keychain access", "gpg": "keyring access", "openssl": "key material handling",
}

// wrapper commands whose real command follows their own flags.
var wrappers = map[string]bool{
	"env": true, "nohup": true, "time": true, "nice": true, "command": true, "exec": true,
	"timeout": true, "stdbuf": true, "ionice": true, "builtin": true, "xargs": true,
}

func baseCmd(argv []string) string {
	if len(argv) == 0 {
		return ""
	}
	return strings.ToLower(filepath.Base(argv[0]))
}

func isAssign(s string) bool {
	i := strings.IndexByte(s, '=')
	if i <= 0 {
		return false
	}
	for j, r := range s[:i] {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') || (j > 0 && r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// stripPrefixes drops leading VAR=x assignments.
func stripPrefixes(argv []string) []string {
	for len(argv) > 0 && isAssign(argv[0]) {
		argv = argv[1:]
	}
	return argv
}

func (c *Classifier) classifySegment(s segment, v *Verdict, depth int) {
	argv := stripPrefixes(s.argv)

	for _, r := range s.redirects {
		if r.target != "" && !strings.HasPrefix(r.op, "<") && !strings.HasPrefix(r.op, ">&") {
			if r.target != "/dev/null" {
				v.raise(Yellow, "")
			}
		}
		c.checkPath(r.target, v)
	}
	if len(argv) == 0 {
		return
	}
	name := baseCmd(argv)
	args := argv[1:]

	if why, ok := alwaysRed[name]; ok {
		v.raise(Red, name+": "+why)
	}
	if strings.HasPrefix(name, "mkfs.") {
		v.raise(Red, name+": filesystem format")
	}
	for _, a := range args {
		c.checkPath(a, v)
	}

	switch {
	case wrappers[name]:
		v.raise(Yellow, "")
		if inner := skipWrapper(name, args); len(inner) > 0 {
			c.classifyInner(inner, v, depth)
		}
		return
	case shells[name]:
		v.raise(Yellow, "")
		for i, a := range args {
			if strings.HasPrefix(a, "-") && strings.Contains(a, "c") && !strings.HasPrefix(a, "--") && i+1 < len(args) {
				v.merge(c.classifyLine(args[i+1], depth+1))
				return
			}
		}
		if len(args) == 0 || strings.HasPrefix(args[0], "-") {
			v.raise(Red, name+": interactive or opaque shell")
		} else {
			v.raise(Red, name+": runs an opaque script")
		}
		return
	case name == "eval":
		v.merge(c.classifyLine(strings.Join(args, " "), depth+1))
		v.raise(Yellow, "")
		return
	case name == "rm":
		v.raise(Yellow, "")
		for _, a := range args {
			if a == "--recursive" || (strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.ContainsAny(a, "rR")) {
				v.raise(Red, "rm with recursive flag")
			}
		}
	case name == "git":
		c.classifyGit(args, v)
	case name == "gh":
		c.classifyGh(args, v)
	case name == "curl" || name == "wget":
		c.classifyFetch(name, args, v)
	case name == "go":
		if len(args) > 0 && greenGo[args[0]] {
			return
		}
		v.raise(Yellow, "")
	case name == "find":
		for i, a := range args {
			switch a {
			case "-exec", "-execdir", "-ok", "-okdir":
				v.raise(Yellow, "")
				if i+1 < len(args) {
					c.classifyInner(trimUntil(args[i+1:], ";", "+"), v, depth)
				}
			case "-delete":
				v.raise(Yellow, "")
			}
		}
	case name == "sed":
		for _, a := range args {
			if strings.HasPrefix(a, "-i") || a == "--in-place" || strings.HasPrefix(a, "--in-place=") {
				v.raise(Yellow, "")
			}
		}
	case greenCmds[name]:
		// green unless redirected (handled above)
	default:
		v.raise(Yellow, "")
	}
}

func (c *Classifier) classifyInner(argv []string, v *Verdict, depth int) {
	if depth > maxDepth {
		v.raise(Red, "command nesting too deep to analyse")
		return
	}
	c.classifySegment(segment{argv: argv}, v, depth+1)
}

func trimUntil(a []string, stops ...string) []string {
	for i, s := range a {
		for _, st := range stops {
			if s == st {
				return a[:i]
			}
		}
	}
	return a
}

// wrapperValueFlags lists, per wrapper, flags that consume a separate value.
var wrapperValueFlags = map[string][]string{
	"env":     {"-u", "-C", "-S"},
	"timeout": {"-k", "-s", "--kill-after", "--signal"},
	"nice":    {"-n"},
	"ionice":  {"-c", "-n", "-p"},
	"xargs":   {"-I", "-n", "-P", "-L", "-s", "-d", "-E"},
	"stdbuf":  {"-i", "-o", "-e"},
}

// skipWrapper returns the argv of the command a wrapper launches.
func skipWrapper(name string, args []string) []string {
	i := 0
	for i < len(args) {
		a := args[i]
		switch {
		case isAssign(a):
			i++
		case strings.HasPrefix(a, "-"):
			i++
			for _, f := range wrapperValueFlags[name] {
				if a == f {
					i++
					break
				}
			}
		default:
			if name == "timeout" {
				name = "" // first positional is the duration
				i++
				continue
			}
			return args[i:]
		}
	}
	return nil
}

func (c *Classifier) classifyGit(args []string, v *Verdict) {
	// skip global options such as -C dir, -c k=v
	i := 0
	for i < len(args) && strings.HasPrefix(args[i], "-") {
		if args[i] == "-C" || args[i] == "-c" {
			i++
		}
		i++
	}
	if i >= len(args) {
		return
	}
	sub, rest := args[i], args[i+1:]
	has := func(flags ...string) bool {
		for _, a := range rest {
			for _, f := range flags {
				if a == f || strings.HasPrefix(a, f+"=") {
					return true
				}
			}
		}
		return false
	}
	shortFlag := func(ch byte) bool {
		for _, a := range rest {
			if len(a) > 1 && a[0] == '-' && a[1] != '-' && strings.IndexByte(a, ch) > 0 {
				return true
			}
		}
		return false
	}
	switch sub {
	case "reset":
		if has("--hard", "--merge", "--keep") {
			v.raise(Red, "git reset --hard discards work")
		}
		v.raise(Yellow, "")
	case "clean":
		if has("--force") || shortFlag('f') {
			v.raise(Red, "git clean -f deletes untracked files")
		}
		v.raise(Yellow, "")
	case "push":
		if has("--force", "--force-with-lease", "--mirror", "--delete", "--prune", "--all", "--tags") || shortFlag('f') || shortFlag('d') {
			v.raise(Red, "git push rewrites or deletes remote history")
		}
		for _, a := range rest {
			if strings.HasPrefix(a, "+") || strings.HasPrefix(a, ":") && len(a) > 1 {
				v.raise(Red, "git push rewrites or deletes remote history")
			}
		}
		if pushTargetsMain(rest) {
			v.raise(Red, "git push targets main/master; Board approval required")
		}
		v.raise(Yellow, "")
	case "config":
		// git config --get is read-only; writes are local edits, global writes touch dotfiles
		if !has("--get", "--get-all", "--list", "-l") {
			v.raise(Yellow, "")
		}
		if has("--global", "--system") {
			v.raise(Yellow, "")
		}
	case "branch", "tag", "remote":
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") || a == "-d" || a == "-D" || a == "-m" || a == "-M" || a == "add" ||
				a == "--delete" || a == "set-url" || a == "remove" {
				if a != "-v" && a != "-a" && a != "-r" && a != "--list" && a != "-l" {
					v.raise(Yellow, "")
				}
			}
		}
	case "fetch":
		// network read, updates refs only
	default:
		if !greenGit[sub] {
			v.raise(Yellow, "")
		}
	}
}

// isMainRef reports whether a git ref name is a protected default branch.
func isMainRef(ref string) bool {
	ref = strings.TrimPrefix(ref, "refs/heads/")
	return ref == "main" || ref == "master"
}

// pushTargetsMain reports whether any refspec in the push arg list targets main/master.
// The first non-flag positional is the remote; subsequent ones are refspecs.
func pushTargetsMain(args []string) bool {
	var positionals []string
	for _, a := range args {
		if !strings.HasPrefix(a, "-") {
			positionals = append(positionals, a)
		}
	}
	// index 0 is the remote (if present); refspecs start at index 1.
	for _, ref := range positionals[min(1, len(positionals)):] {
		if i := strings.LastIndex(ref, ":"); i >= 0 {
			if isMainRef(ref[i+1:]) {
				return true
			}
		} else if isMainRef(ref) {
			return true
		}
	}
	return false
}

// ghValueFlags are gh global flags that consume the next token (so the token
// after them is a value, not a subcommand).
var ghValueFlags = map[string]bool{"-R": true, "--repo": true, "--hostname": true}

func (c *Classifier) classifyGh(args []string, v *Verdict) {
	// Skip global flags (and their values) to find the subcommand.
	// --flag=value form is a single token; --flag value form consumes two.
	i := 0
	for i < len(args) {
		a := args[i]
		if !strings.HasPrefix(a, "-") {
			break
		}
		i++
		if ghValueFlags[a] {
			i++ // skip the separate value token
		}
	}
	if i >= len(args) {
		v.raise(Yellow, "")
		return
	}
	sub := args[i]
	rest := args[i+1:]
	switch sub {
	case "pr":
		// Fail closed: any positional "merge" token under gh pr is a merge action.
		for _, a := range rest {
			if !strings.HasPrefix(a, "-") && a == "merge" {
				v.raise(Red, "gh pr merge lands on the default branch; Board approval required")
				return
			}
		}
		v.raise(Yellow, "")
	case "api":
		for j, a := range rest {
			lower := strings.ToLower(a)
			// -XPOST / --method=POST / -X POST / --method POST (mutating methods)
			switch {
			case (a == "-X" || a == "--method") && j+1 < len(rest):
				switch strings.ToUpper(rest[j+1]) {
				case "POST", "PUT", "PATCH", "DELETE":
					v.raise(Red, "gh api mutating method; Board approval required")
					return
				}
			case strings.HasPrefix(lower, "-x") && len(a) > 2:
				switch strings.ToUpper(a[2:]) {
				case "POST", "PUT", "PATCH", "DELETE":
					v.raise(Red, "gh api mutating method; Board approval required")
					return
				}
			case strings.HasPrefix(lower, "--method="):
				switch strings.ToUpper(a[9:]) {
				case "POST", "PUT", "PATCH", "DELETE":
					v.raise(Red, "gh api mutating method; Board approval required")
					return
				}
			// -f/-F/--field/--raw-field/--input (any form) imply a POST body
			case a == "-f" || a == "-F" || strings.HasPrefix(lower, "--field") ||
				strings.HasPrefix(lower, "--raw-field") || lower == "--input" || strings.HasPrefix(lower, "--input="):
				v.raise(Red, "gh api submits data; Board approval required")
				return
			}
			// Merge endpoint by URL
			if !strings.HasPrefix(a, "-") && strings.Contains(lower, "/merge") {
				v.raise(Red, "gh api targets a merge endpoint; Board approval required")
				return
			}
		}
		v.raise(Yellow, "")
	default:
		v.raise(Yellow, "")
	}
}

func (c *Classifier) classifyFetch(name string, args []string, v *Verdict) {
	v.raise(Yellow, "")
	for i, a := range args {
		switch {
		case a == "-d" || strings.HasPrefix(a, "--data") || a == "-F" || strings.HasPrefix(a, "--form") ||
			a == "-T" || a == "--upload-file" || a == "--json" ||
			strings.HasPrefix(a, "--post-") || a == "--body-data" || a == "--body-file":
			v.raise(Red, name+": uploads data (possible exfiltration)")
		case (a == "-X" || a == "--request") && i+1 < len(args):
			switch strings.ToUpper(args[i+1]) {
			case "POST", "PUT", "PATCH", "DELETE":
				v.raise(Red, name+": mutating HTTP method (possible exfiltration)")
			}
		case a == "-K" || a == "--config" || a == "-i" && name == "wget":
			v.raise(Red, name+": reads request definition from a file")
		}
	}
}

func (c *Classifier) home() string {
	if c.Home != "" {
		return c.Home
	}
	h, _ := os.UserHomeDir()
	return h
}

// sensitiveDirs are locations whose reads and writes always need confirmation.
func (c *Classifier) sensitiveDirs() []string {
	dirs := []string{"/etc", "/private/etc"}
	if h := c.home(); h != "" {
		for _, d := range []string{".ssh", ".aws", ".gnupg"} {
			dirs = append(dirs, filepath.Join(h, d))
		}
	}
	return dirs
}

func (c *Classifier) expandHome(p string) string {
	h := c.home()
	switch {
	case h == "":
		return p
	case p == "~" || strings.HasPrefix(p, "~/"):
		return filepath.Join(h, p[1:])
	case p == "$HOME" || strings.HasPrefix(p, "$HOME/"):
		return filepath.Join(h, p[len("$HOME"):])
	case p == "${HOME}" || strings.HasPrefix(p, "${HOME}/"):
		return filepath.Join(h, p[len("${HOME}"):])
	}
	return p
}

var harmlessPaths = map[string]bool{"/dev/null": true, "/dev/stdout": true, "/dev/stderr": true, "/dev/stdin": true, "/dev/tty": true}

func looksLikePath(s string) bool {
	return s == "~" || s == ".." || strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~/") ||
		strings.HasPrefix(s, "$HOME") || strings.HasPrefix(s, "${HOME}") ||
		strings.HasPrefix(s, "../") || strings.Contains(s, "/../") || strings.HasSuffix(s, "/..")
}

// checkPath escalates for sensitive locations and (when a worktree is set) escapes.
func (c *Classifier) checkPath(tok string, v *Verdict) {
	cands := []string{tok}
	if i := strings.IndexByte(tok, '='); i > 0 {
		cands = append(cands, tok[i+1:])
	}
	for _, cand := range cands {
		if cand == "" || strings.Contains(cand, "://") || harmlessPaths[cand] {
			continue
		}
		exp := c.expandHome(cand)
		if !looksLikePath(cand) && exp == cand {
			continue
		}
		clean := filepath.Clean(exp)
		for _, d := range c.sensitiveDirs() {
			if clean == d || strings.HasPrefix(clean, d+string(filepath.Separator)) {
				v.raise(Red, "touches sensitive path "+d)
			}
		}
		if c.Worktree != nil {
			if err := c.Worktree.Check(exp); err != nil {
				v.raise(Red, "path outside worktree: "+cand)
			}
		}
	}
}
