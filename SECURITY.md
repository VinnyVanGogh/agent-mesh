# Staypoint Security Policy

## 1. Security Architecture & Threat Model

Staypoint is designed under a local-first, zero-trust host boundary model. It supervises autonomous coding agents while protecting developers against data exfiltration, accidental working tree destruction, and unauthorized network exposure.

### Local Loopback Isolation

All network listeners created by Staypoint (such as the remote bridge reverse HTTP listener) bind strictly to localhost (`127.0.0.1` or `::1`). Staypoint never binds to wildcard interfaces (`0.0.0.0`), preventing exposure to local area networks or untrusted Wi-Fi environments.

### Cryptographic Reverse Bridge Tokens

The remote bridge feature allows executing tools across corporate workstations over SSH. The reverse tunnel server requires a bearer token generated via Go standard library `crypto/rand` using 32 bytes (256 bits) of entropy, hex-encoded. Requests without this exact bearer token header are rejected immediately with HTTP 401 Unauthorized.

### Zero Outbound Telemetry

Staypoint does not transmit any usage statistics, telemetry events, crash reports, or source code to remote analytics services. All SQLite databases, cursor trackers, and logs reside exclusively on the host filesystem under `~/.staypoint/`.

### Process Execution Hygiene

When launching external tools or agent runtimes, Staypoint resolves binaries using `exec.LookPath` and passes arguments as discrete string arrays directly to `syscall.Exec` or `exec.CommandContext`. Shell string concatenation (`sh -c` or `bash -c`) is avoided, eliminating shell injection vulnerabilities from user prompt arguments.

### Isolated Git Ref Namespace

Ephemeral micro-checkpoints are stored in the repository object database under `refs/staypoint/checkpoints/` using an isolated index file (`GIT_INDEX_FILE=.git/staypoint_index`). Checkpoints are not pushed to remote origins during normal `git push` operations because standard push configurations only track `refs/heads/*`.

---

## 2. Reporting a Vulnerability

If you discover a security issue or vulnerability in Staypoint:

1. **Do not open a public GitHub issue.**
2. Report the vulnerability privately via GitHub Security Advisories or by emailing the maintainer directly at:
   `security@staypoint.dev` (or the repository maintainer email).
3. Include detailed steps to reproduce the issue, proof of concept code, and the affected version of Staypoint.
4. The maintainer will acknowledge receipt within 48 hours and provide an estimated timeline for remediation.

---

## 3. Autonomous-run security floor

Secret redaction on persisted/emitted streams, a sanitized child environment, Green/Yellow/Red command tiers with
human confirmation for Red, and a worktree path boundary are implemented in `internal/security`.
See `docs/SECURITY-FLOOR.md` for behaviour, known limits and the open credential-vault design review.
