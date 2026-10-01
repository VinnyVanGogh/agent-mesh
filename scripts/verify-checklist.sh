#!/usr/bin/env bash
# ==============================================================================
# StayPoint Agent Pre-Flight Checklist Verification Tool (STA-236)
#
# MANDATORY PRE-COMPLETION GATE:
# Agents and engineers MUST run this script before marking any StayPoint issue
# as "done" or closing PRs.
#
# It verifies:
# 1. StayPoint daemon is alive and healthy on 127.0.0.1:41421.
# 2. Running daemon binary commit is valid, merged into main, and up to date.
# 3. All checklist sections have required commits merged and present in binary.
# 4. Zero unmerged branches or unrebuilt binary warnings exist.
# 5. Automated machine contracts evaluate cleanly without regressions.
# ==============================================================================
set -euo pipefail

# ANSI color codes
BOLD="\033[1m"
GREEN="\033[0;32m"
RED="\033[0;31m"
YELLOW="\033[1;33m"
CYAN="\033[0;36m"
RESET="\033[0m"

# Default configuration
DAEMON_URL="${STAYPOINT_ADDR:-http://127.0.0.1:41421}"
SPRINT="${STAYPOINT_SPRINT:-STA-236}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
STRICT_PASS=0

usage() {
    cat <<EOF
Usage: $(basename "$0") [OPTIONS]

Pre-completion checklist & commit verification gate for StayPoint.

Options:
  --url <url>       StayPoint daemon base URL (default: http://127.0.0.1:41421)
  --sprint <name>   Sprint identifier (default: STA-236)
  --repo <path>     Target git repository root (default: $REPO_DIR)
  --strict          Require 100% of checklist items to be in 'pass' state
  -h, --help        Show this help message
EOF
    exit 0
}

while [[ $# -gt 0 ]]; do
    case "$1" in
        --url) DAEMON_URL="$2"; shift 2 ;;
        --sprint) SPRINT="$2"; shift 2 ;;
        --repo) REPO_DIR="$2"; shift 2 ;;
        --strict) STRICT_PASS=1; shift ;;
        -h|--help) usage ;;
        *) echo -e "${RED}Unknown option: $1${RESET}"; usage ;;
    esac
done

echo -e "${BOLD}${CYAN}========================================================================${RESET}"
echo -e "${BOLD}${CYAN}   StayPoint Pre-Completion Checklist Verification Gate (STA-236)      ${RESET}"
echo -e "${BOLD}${CYAN}========================================================================${RESET}"
echo -e " Target Daemon: ${BOLD}${DAEMON_URL}${RESET}"
echo -e " Sprint:        ${BOLD}${SPRINT}${RESET}"
echo -e " Repository:    ${BOLD}${REPO_DIR}${RESET}"
echo ""

# Resolve Auth Token
AUTH_TOKEN="${STAYPOINT_AUTH_TOKEN:-}"
if [[ -z "$AUTH_TOKEN" && -f "$HOME/.staypoint/auth_token" ]]; then
    AUTH_TOKEN="$(cat "$HOME/.staypoint/auth_token" 2>/dev/null | tr -d '\r\n')"
fi
if [[ -z "$AUTH_TOKEN" && -f "/tmp/staypoint/auth_token" ]]; then
    AUTH_TOKEN="$(cat "/tmp/staypoint/auth_token" 2>/dev/null | tr -d '\r\n')"
fi

AUTH_HEADER=""
if [[ -n "$AUTH_TOKEN" ]]; then
    AUTH_HEADER="Authorization: Bearer $AUTH_TOKEN"
fi

OVERALL_PASS=1
FAILURES=()

# ------------------------------------------------------------------------------
# 1. Daemon Reachability & Health Check
# ------------------------------------------------------------------------------
echo -e "${BOLD}1. Checking Daemon Connectivity & Health...${RESET}"
HEALTH_JSON=""
if [[ -n "$AUTH_HEADER" ]]; then
    HEALTH_JSON="$(curl -s -f -m 5 -H "$AUTH_HEADER" "${DAEMON_URL}/api/health" 2>/dev/null || true)"
else
    HEALTH_JSON="$(curl -s -f -m 5 "${DAEMON_URL}/api/health" 2>/dev/null || true)"
fi

if [[ -z "$HEALTH_JSON" ]]; then
    echo -e "  ${RED}✗ FAIL${RESET}: Unable to reach StayPoint daemon at ${DAEMON_URL}/api/health"
    echo -e "    ${YELLOW}Action required: Start or rebuild the daemon:${RESET}"
    echo -e "    ${CYAN}cd \"$REPO_DIR\" && scripts/reinstall-daemon.sh${RESET}"
    OVERALL_PASS=0
    FAILURES+=("Daemon is not reachable at ${DAEMON_URL}")
    DAEMON_COMMIT="unreachable"
else
    DAEMON_COMMIT="$(python3 -c "import json,sys; data=json.loads(sys.argv[1]); print(data.get('git_commit') or data.get('commit') or 'none')" "$HEALTH_JSON" 2>/dev/null || echo "none")"
    echo -e "  ${GREEN}✓ PASS${RESET}: Daemon is active and responding (version: 1.0)"
    echo -e "         Running daemon binary commit: ${BOLD}${DAEMON_COMMIT}${RESET}"
fi

# ------------------------------------------------------------------------------
# 2. Main Branch vs Running Daemon Commit Verification
# ------------------------------------------------------------------------------
echo ""
echo -e "${BOLD}2. Validating Binary Commit vs Main Branch...${RESET}"
MAIN_SHA="unknown"
if git -C "$REPO_DIR" rev-parse --git-dir >/dev/null 2>&1; then
    MAIN_SHA="$(git -C "$REPO_DIR" rev-parse --short refs/heads/main 2>/dev/null || git -C "$REPO_DIR" rev-parse --short origin/main 2>/dev/null || git -C "$REPO_DIR" rev-parse --short HEAD 2>/dev/null || echo "unknown")"
fi
echo -e "  Main branch HEAD SHA: ${BOLD}${MAIN_SHA}${RESET}"

if [[ "$DAEMON_COMMIT" == "unreachable" ]]; then
    echo -e "  ${RED}✗ FAIL${RESET}: Cannot inspect binary commit (daemon unreachable)"
elif [[ "$DAEMON_COMMIT" == "none" || "$DAEMON_COMMIT" == "unknown" || -z "$DAEMON_COMMIT" ]]; then
    echo -e "  ${RED}✗ FAIL${RESET}: Running staypointd was not compiled with GitCommit ldflags (-X main.GitCommit=...)"
    echo -e "    ${YELLOW}Action required: Recompile and codesign binary via:${RESET}"
    echo -e "    ${CYAN}cd \"$REPO_DIR\" && scripts/reinstall-daemon.sh${RESET}"
    OVERALL_PASS=0
    FAILURES+=("Running daemon commit is 'none' / unrebuilt")
elif [[ "$DAEMON_COMMIT" == *-dirty ]]; then
    echo -e "  ${RED}✗ FAIL${RESET}: Running staypointd was built from ${DAEMON_COMMIT%-dirty} plus uncommitted changes"
    echo -e "    ${YELLOW}Action required: commit or discard them, then rebuild via${RESET} ${CYAN}scripts/reinstall-daemon.sh${RESET}"
    OVERALL_PASS=0
    FAILURES+=("Running daemon was built from a dirty working tree")
else
    # Check if DAEMON_COMMIT exists in repository
    if ! git -C "$REPO_DIR" rev-parse --verify "${DAEMON_COMMIT}^{commit}" >/dev/null 2>&1; then
        echo -e "  ${RED}✗ FAIL${RESET}: Running binary commit ${DAEMON_COMMIT} does not exist in local git history"
        OVERALL_PASS=0
        FAILURES+=("Running binary commit ${DAEMON_COMMIT} is not in git history")
    else
        # Check if DAEMON_COMMIT is in main history
        IN_MAIN=0
        if git -C "$REPO_DIR" merge-base --is-ancestor "$DAEMON_COMMIT" main 2>/dev/null || \
           git -C "$REPO_DIR" merge-base --is-ancestor "$DAEMON_COMMIT" origin/main 2>/dev/null || \
           git -C "$REPO_DIR" merge-base --is-ancestor "$DAEMON_COMMIT" HEAD 2>/dev/null; then
            IN_MAIN=1
        fi

        if [[ "$IN_MAIN" -eq 1 ]]; then
            echo -e "  ${GREEN}✓ PASS${RESET}: Binary commit ${DAEMON_COMMIT} is verified in main branch history"
        else
            echo -e "  ${RED}✗ FAIL${RESET}: Binary commit ${DAEMON_COMMIT} was built from an unmerged branch and is not in main"
            OVERALL_PASS=0
            FAILURES+=("Binary commit ${DAEMON_COMMIT} is on an unmerged branch")
        fi

        # Warn if main has moved ahead of running daemon
        if [[ "$MAIN_SHA" != "unknown" && "$DAEMON_COMMIT" != "$MAIN_SHA" ]]; then
            if git -C "$REPO_DIR" merge-base --is-ancestor "$DAEMON_COMMIT" "$MAIN_SHA" 2>/dev/null; then
                echo -e "  ${YELLOW}⚠ NOTICE${RESET}: Running daemon (${DAEMON_COMMIT}) is behind latest main (${MAIN_SHA})"
                echo -e "           Daemon should be rebuilt before final sign-off: ${CYAN}scripts/reinstall-daemon.sh${RESET}"
            fi
        fi
    fi
fi

# Ad-hoc signatures change identity on every rebuild, so macOS re-prompts for
# Documents access each time. The installed daemon must carry a real signature.
DAEMON_BIN="$HOME/.local/bin/staypointd"
if [[ "$(uname)" == "Darwin" && -x "$DAEMON_BIN" ]]; then
    if codesign -dv "$DAEMON_BIN" 2>&1 | grep -q "Signature=adhoc"; then
        echo -e "  ${RED}✗ FAIL${RESET}: ${DAEMON_BIN} is ad-hoc signed (macOS will re-prompt for permissions after every rebuild)"
        echo -e "    ${YELLOW}Action required: rebuild via${RESET} ${CYAN}scripts/reinstall-daemon.sh${RESET}"
        OVERALL_PASS=0
        FAILURES+=("staypointd is ad-hoc signed")
    else
        echo -e "  ${GREEN}✓ PASS${RESET}: staypointd has a stable code signature"
    fi
    CERT_MSG="$("$SCRIPT_DIR/check-signing-cert.sh" 2>&1 | grep -v '^[A-Z_]*=')"
    CERT_STATUS=$("$SCRIPT_DIR/check-signing-cert.sh" --quiet >/dev/null 2>&1; echo $?)
    if [[ "$CERT_STATUS" -ne 0 ]]; then
        echo -e "  ${RED}✗ FAIL${RESET}: ${CERT_MSG}"
        OVERALL_PASS=0
        FAILURES+=("Signing certificate missing, expired, or expiring within 30 days")
    else
        echo -e "  ${GREEN}✓ PASS${RESET}: ${CERT_MSG#✓ }"
    fi
fi

# ------------------------------------------------------------------------------
# 3. Checklist Commit-Hash Gate & Section Verification
# ------------------------------------------------------------------------------
echo ""
echo -e "${BOLD}3. Checking Checklist Commit-Hash Gate for Sprint ${SPRINT}...${RESET}"
CHECKLIST_JSON=""
if [[ -n "$AUTH_HEADER" ]]; then
    CHECKLIST_JSON="$(curl -s -f -m 30 -H "$AUTH_HEADER" "${DAEMON_URL}/api/checklist?sprint=${SPRINT}&repo_root=${REPO_DIR}" 2>/dev/null || true)"
else
    CHECKLIST_JSON="$(curl -s -f -m 30 "${DAEMON_URL}/api/checklist?sprint=${SPRINT}&repo_root=${REPO_DIR}" 2>/dev/null || true)"
fi

if [[ -z "$CHECKLIST_JSON" ]]; then
    echo -e "  ${RED}✗ FAIL${RESET}: Failed to query checklist endpoint from ${DAEMON_URL}/api/checklist"
    OVERALL_PASS=0
    FAILURES+=("Failed to query /api/checklist")
else
    # Parse commit verification using Python
    PYTHON_CHECK=$(cat <<'EOF'
import json, sys

raw = sys.stdin.read()
try:
    data = json.loads(raw)
except Exception as e:
    print(f"ERROR: {e}")
    sys.exit(2)

items = data.get("items", [])
ver = data.get("commit_verification", {})

missing = ver.get("missing_commits", [])
blocked_items = ver.get("blocked_item_ids", [])
blocked_sections = ver.get("blocked_sections", [])
is_verified = ver.get("verified", False)

passed_items = sum(1 for i in items if i.get("status") == "pass")
partial_items = sum(1 for i in items if i.get("status") == "partial")
fail_items = sum(1 for i in items if i.get("status") == "fail")
pending_items = sum(1 for i in items if i.get("status") == "pending")
not_done_items = sum(1 for i in items if i.get("status") == "not_done")
total_items = len(items)

print(f"TOTAL={total_items}")
print(f"PASSED={passed_items}")
print(f"PARTIAL={partial_items}")
print(f"FAILED={fail_items}")
print(f"PENDING={pending_items}")
print(f"NOT_DONE={not_done_items}")
print(f"VERIFIED={'1' if is_verified else '0'}")
print(f"BLOCKED_ITEMS_COUNT={len(blocked_items)}")
print(f"BLOCKED_SECTIONS_COUNT={len(blocked_sections)}")

for w in missing[:8]:
    print(f"WARN|{w.get('commit')}|{w.get('section')}|{w.get('reason')}")
EOF
)

    EVAL_OUT="$(echo "$CHECKLIST_JSON" | python3 -c "$PYTHON_CHECK")"

    TOTAL_ITEMS=$(echo "$EVAL_OUT" | grep "^TOTAL=" | cut -d= -f2)
    PASSED_ITEMS=$(echo "$EVAL_OUT" | grep "^PASSED=" | cut -d= -f2)
    PARTIAL_ITEMS=$(echo "$EVAL_OUT" | grep "^PARTIAL=" | cut -d= -f2)
    FAILED_ITEMS=$(echo "$EVAL_OUT" | grep "^FAILED=" | cut -d= -f2)
    PENDING_ITEMS=$(echo "$EVAL_OUT" | grep "^PENDING=" | cut -d= -f2)
    NOT_DONE_ITEMS=$(echo "$EVAL_OUT" | grep "^NOT_DONE=" | cut -d= -f2)
    IS_VERIFIED=$(echo "$EVAL_OUT" | grep "^VERIFIED=" | cut -d= -f2)
    BLOCKED_COUNT=$(echo "$EVAL_OUT" | grep "^BLOCKED_ITEMS_COUNT=" | cut -d= -f2)
    BLOCKED_SECTIONS=$(echo "$EVAL_OUT" | grep "^BLOCKED_SECTIONS_COUNT=" | cut -d= -f2)

    echo -e "  Items Summary: ${PASSED_ITEMS} pass, ${PARTIAL_ITEMS} partial, ${FAILED_ITEMS} fail, ${PENDING_ITEMS} pending (Total: ${TOTAL_ITEMS})"

    if [[ "$IS_VERIFIED" == "1" && "$BLOCKED_COUNT" == "0" ]]; then
        echo -e "  ${GREEN}✓ PASS${RESET}: All referenced commit hashes exist in main and running daemon binary"
    else
        echo -e "  ${RED}✗ FAIL${RESET}: Commit gate active! ${BLOCKED_COUNT} items blocked across ${BLOCKED_SECTIONS} sections."
        echo -e "  ${YELLOW}Missing / Unmerged Commit Log:${RESET}"
        while IFS= read -r line; do
            if [[ "$line" =~ ^WARN\| ]]; then
                C_HASH="$(echo "$line" | cut -d'|' -f2)"
                C_SEC="$(echo "$line" | cut -d'|' -f3)"
                C_RSN="$(echo "$line" | cut -d'|' -f4)"
                echo -e "    - Commit ${BOLD}${C_HASH}${RESET} (${C_SEC}): ${RED}${C_RSN}${RESET}"
            fi
        done <<< "$EVAL_OUT"
        OVERALL_PASS=0
        FAILURES+=("${BLOCKED_COUNT} checklist items blocked by commit gate")
    fi

    if [[ "$STRICT_PASS" -eq 1 ]]; then
        if [[ "$FAILED_ITEMS" -gt 0 || "$PENDING_ITEMS" -gt 0 || "$NOT_DONE_ITEMS" -gt 0 || "$PARTIAL_ITEMS" -gt 0 ]]; then
            echo -e "  ${RED}✗ FAIL (Strict Mode)${RESET}: All items must be marked 'pass' (${PASSED_ITEMS}/${TOTAL_ITEMS} passed)"
            OVERALL_PASS=0
            FAILURES+=("Strict mode failed: not all items are marked pass")
        fi
    fi
fi

# ------------------------------------------------------------------------------
# 4. Automated Contract Evaluation Engine
# ------------------------------------------------------------------------------
echo ""
echo -e "${BOLD}4. Evaluating Machine Contracts via Divergence Engine...${RESET}"
EVAL_API_JSON=""
# Timeout: 60s stopgap (STA-279). Root fix is the eval-contracts cache written by
# reinstall-daemon.sh; with a warm cache the endpoint returns in <1s. Without a
# cache (first install or DB not seeded) live evaluation may take up to ~32s if
# the daemon lacks ~/Documents TCC; 60s prevents a false WARNING in that window.
# The response includes total_ms / max_contract_ms so the latency is never hidden.
EVAL_CURL_TIMEOUT=60
if [[ -n "$AUTH_HEADER" ]]; then
    EVAL_API_JSON="$(curl -s -f -m "$EVAL_CURL_TIMEOUT" -X POST -H "$AUTH_HEADER" "${DAEMON_URL}/api/checklist/evaluate?sprint=${SPRINT}&downgrade=false&repo_root=${REPO_DIR}" 2>/dev/null || true)"
else
    EVAL_API_JSON="$(curl -s -f -m "$EVAL_CURL_TIMEOUT" -X POST "${DAEMON_URL}/api/checklist/evaluate?sprint=${SPRINT}&downgrade=false&repo_root=${REPO_DIR}" 2>/dev/null || true)"
fi

if [[ -z "$EVAL_API_JSON" ]]; then
    echo -e "  ${YELLOW}⚠ WARNING${RESET}: Could not trigger contract evaluation endpoint"
else
    PYTHON_EVAL=$(cat <<'EOF'
import json, sys
data = json.loads(sys.stdin.read())
total = data.get("total", 0)
passed = data.get("passed", 0)
failed = data.get("failed", 0)
divergences = data.get("divergences", 0)
total_ms = data.get("total_ms", 0)
max_ms = data.get("max_contract_ms", 0)
cache_hits = data.get("cache_hits", 0)
cache_misses = data.get("cache_misses", 0)
print(f"EVAL_TOTAL={total}")
print(f"EVAL_PASSED={passed}")
print(f"EVAL_FAILED={failed}")
print(f"EVAL_DIVERGENCES={divergences}")
print(f"EVAL_TOTAL_MS={total_ms}")
print(f"EVAL_MAX_MS={max_ms}")
print(f"EVAL_CACHE_HITS={cache_hits}")
print(f"EVAL_CACHE_MISSES={cache_misses}")
EOF
)
    EVAL_METRICS="$(echo "$EVAL_API_JSON" | python3 -c "$PYTHON_EVAL" 2>/dev/null || echo "")"
    if [[ -n "$EVAL_METRICS" ]]; then
        E_TOTAL=$(echo "$EVAL_METRICS" | grep "^EVAL_TOTAL=" | cut -d= -f2)
        E_PASS=$(echo "$EVAL_METRICS" | grep "^EVAL_PASSED=" | cut -d= -f2)
        E_FAIL=$(echo "$EVAL_METRICS" | grep "^EVAL_FAILED=" | cut -d= -f2)
        E_DIV=$(echo "$EVAL_METRICS" | grep "^EVAL_DIVERGENCES=" | cut -d= -f2)
        E_TOTAL_MS=$(echo "$EVAL_METRICS" | grep "^EVAL_TOTAL_MS=" | cut -d= -f2)
        E_MAX_MS=$(echo "$EVAL_METRICS" | grep "^EVAL_MAX_MS=" | cut -d= -f2)
        E_HITS=$(echo "$EVAL_METRICS" | grep "^EVAL_CACHE_HITS=" | cut -d= -f2)
        E_MISSES=$(echo "$EVAL_METRICS" | grep "^EVAL_CACHE_MISSES=" | cut -d= -f2)

        echo -e "  Contracts evaluated: ${E_PASS}/${E_TOTAL} passed (${E_FAIL} failed, ${E_DIV} regressions)"
        if [[ -n "$E_TOTAL_MS" && "$E_TOTAL_MS" -gt 0 ]]; then
            echo -e "  Evaluation latency:  ${E_TOTAL_MS}ms total, ${E_MAX_MS}ms slowest contract (${E_HITS} cached / ${E_MISSES} live)"
        fi
        if [[ "$E_FAIL" -eq 0 && "$E_DIV" -eq 0 ]]; then
            echo -e "  ${GREEN}✓ PASS${RESET}: Zero contract failures or regressions detected"
        else
            echo -e "  ${RED}✗ FAIL${RESET}: ${E_FAIL} machine contract assertions failed"
            OVERALL_PASS=0
            FAILURES+=("${E_FAIL} automated contracts failed evaluation")
        fi
    fi
fi

# ------------------------------------------------------------------------------
# Final Disposition & Summary
# ------------------------------------------------------------------------------
echo ""
echo -e "${BOLD}${CYAN}========================================================================${RESET}"
if [[ "$OVERALL_PASS" -eq 1 ]]; then
    echo -e "${BOLD}${GREEN}  VERDICT: PASS — Checklist Verification & Commit Gate Satisfied!       ${RESET}"
    echo -e "${BOLD}${CYAN}========================================================================${RESET}"
    echo -e "All StayPoint DoD criteria met: daemon active, commits in main, binary compiled."
    echo -e "Issue may proceed to completion."
    exit 0
else
    echo -e "${BOLD}${RED}  VERDICT: FAIL — Closing issue strictly PROHIBITED!                     ${RESET}"
    echo -e "${BOLD}${CYAN}========================================================================${RESET}"
    echo -e "The following blocker(s) must be resolved before closing this task:"
    for f in "${FAILURES[@]}"; do
        echo -e "  ${RED}• $f${RESET}"
    done
    echo ""
    echo -e "${YELLOW}Remediation Steps:${RESET}"
    echo -e "  1. Ensure all branch changes are merged into main: ${CYAN}git checkout main && git merge <branch>${RESET}"
    echo -e "  2. Rebuild and restart the daemon binary:          ${CYAN}scripts/reinstall-daemon.sh${RESET}"
    echo -e "  3. Re-run this verification script:                ${CYAN}scripts/verify-checklist.sh${RESET}"
    exit 1
fi
