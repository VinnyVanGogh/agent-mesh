# Security floor for autonomous runs (T8)

Package: `internal/security`. Nothing here polls or wakes on a timer; it is all
called inline by whatever launches or records agent activity.

| Piece | API | Wired into |
|-------|-----|-----------|
| Secret redactor | `Redact`, `RedactBytes`, streaming `NewWriter` | `logging.SetupLogger` (all log records), `wire.Post` (persisted message content) |
| Child env sanitizer | `SanitizeEnv`, `ChildEnv(extra...)` | `checkpoint.runGit`, `Gate.Command` |
| Command tiers | `Classifier.Classify` (shell line), `ClassifyArgv` | `Gate` |
| Path boundary | `Boundary.Resolve/Check` (symlink-aware) | `Classifier` (Worktree), `Gate` |
| Confirmation gate | `Gate.Authorize`, `Gate.Command` | future autonomous harness (T-phase 3) must launch through it |

## Redaction
Anthropic `sk-ant-*`, OpenAI `sk-*` / `sk-proj-*`, Google `AIza*`, GitHub `ghp_/gho_/ghu_/ghs_/ghr_` and
`github_pat_*`, AWS `AKIA/ASIA*`, `Bearer` tokens, PEM private key blocks (also when JSON-escaped).
Markers look like `[REDACTED:anthropic-key]`. `Writer` buffers by line and by whole PEM block so a secret split
across writes is still caught. Any new persisted or emitted stream must go through it.

## Env sanitizer
Allowlist, not denylist: `PATH HOME USER SHELL TERM LANG TZ TMPDIR ...`, `LC_*`, `XDG_*`, `GIT_*`.
Names that look secret (`TOKEN SECRET PASSWORD CREDENTIAL API_KEY *_KEY ...`) are dropped even under an allowed
prefix. A task that needs a credential names it explicitly (`extra`), e.g. `ANTHROPIC_API_KEY`.
`SSH_AUTH_SOCK` is not passed by default.

Not converted: `syscall.Exec` launches in `cmd/staypoint/root.go` and `continue_cmd.go` hand the user's own
interactive CLI (`claude`, `agy`) its full environment on purpose; sanitizing would break the user's login.
Autonomous launches must use `Gate.Command`.

## Tiers
* **Green**: read-only (`ls cat grep rg git status/log/diff ...`).
* **Yellow**: anything else that stays local (edits, builds, `git commit`, redirect to a file). Runs under an auto-checkpoint.
* **Red**: needs a human. Recursive `rm`, `git reset --hard`, `git clean -f`, forced/deleting `git push`, `sudo/doas/su`,
  `dd/mkfs`, any touch of `~/.ssh ~/.aws ~/.gnupg /etc`, raw network tools (`nc ssh scp rsync socat ...`),
  `curl/wget` that upload, `... | sh`, opaque `sh script`, unparseable input, and any path argument outside the worktree.

The classifier parses quoting, `; && || |`, subshells, `$(...)`/backticks/process substitution, redirects, `VAR=x`
prefixes, and unwraps `env nohup time timeout xargs find -exec sh -c eval`. It is deliberately conservative: false
Reds cost a prompt, false Greens cost a compromise.

Known limits (accepted for MVP, revisit with OS sandboxing which is out of scope here):
* Variable/glob expansion is not evaluated (`rm -rf $DIR`, `cat ~/.s*/id_*`). The env sanitizer and worktree boundary reduce impact but do not close this.
* Plain `curl URL` is Yellow: a GET can still leak data through the URL.
* Interpreters (`python -c`, `node -e`) are Yellow; their bodies are not analysed.
* Path checks are lexical plus symlink resolution at classification time (TOCTOU is possible).

## Path boundary
`Boundary.Resolve` cleans the path, resolves symlinks on the longest existing prefix (so an in-tree link cannot
point out, and a not-yet-created file is still checked), then requires it to be under the resolved worktree root.

## Design review: headless credential vault (no crypto built)
Status: **needs a threat-model decision before any code.** RES-47 proposes AES-256-GCM + Argon2id with a
machine-derived salt. Concerns to confirm:

1. **Who is the attacker?** A same-user process (malware, a prompt-injected agent) can read a `0600` file, the
   machine-derived key material and the daemon's memory. Encryption whose key the daemon can derive unattended
   only defeats offline theft of the file (backup leak, disk image), not a local attacker.
2. **Machine-derived salt is not a secret.** It adds no security against a same-user attacker; Argon2id only helps
   if there is a human-supplied passphrase, which a headless daemon does not have.
3. **Options**
   * A. Interactive: OS keychain (`security`/Secret Service/DPAPI). Best; unusable headless.
   * B. Headless, no crypto: `0600` file in a `0700` dir, never in the worktree, never injected into agent env
     except by explicit `extra`. Honest about what it protects. Cheapest.
   * C. Headless with crypto: sealed by a key held in the OS keychain (or TPM/Secure Enclave where present) so the file
     alone is useless. Real defence against offline theft; requires the keychain to be reachable by the daemon.
   * D. Passphrase at daemon start (Argon2id): strong, but not unattended.
4. **Recommendation:** A where a keychain exists; otherwise B for MVP with C as a follow-up. Do not ship
   "encrypted file with machine-derived key" as a headline security feature; it reads as protection it does not give.
   BYOC stays intact: StayPoint stores nothing it does not already find in the user's own CLIs.
