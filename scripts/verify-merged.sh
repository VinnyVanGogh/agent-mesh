#!/usr/bin/env bash
# verify-merged.sh — check that all given SHAs (or issue branch tips) are
# merged into origin/main, using ancestry + squash-equivalence.
#
# Usage:
#   scripts/verify-merged.sh <sha|issue-id>...
#   scripts/verify-merged.sh eb4f6a7 STA-413 c4aaad4
#
# Exit 0 and prints "ALL MERGED <origin/main sha>" when every SHA is present.
# Exit 1 and prints "NOT IN MAIN: <sha> <subject>" for each missing SHA.
#
# Issue IDs (e.g. STA-413) are resolved to the HEAD of the matching local
# branch (paperclip/<id-lower> or any branch containing the id string).
set -euo pipefail

REPO="$(cd "$(dirname "$0")/.." && pwd)"

usage() {
    echo "usage: $0 <sha|issue-id>..." >&2
    exit 2
}

[ $# -eq 0 ] && usage

# Fetch first so origin/main is current.
git -C "$REPO" fetch --all --prune --quiet

MAIN_SHA="$(git -C "$REPO" rev-parse --short origin/main)"
MISSING=()

resolve_sha() {
    local arg="$1"
    # If it looks like a SHA (hex chars), resolve directly.
    if [[ "$arg" =~ ^[0-9a-fA-F]{7,40}$ ]]; then
        git -C "$REPO" rev-parse "$arg" 2>/dev/null || echo "$arg"
        return
    fi
    # Try as an issue ID: look for a branch whose name contains the lowercased id.
    local id_lower
    id_lower="$(echo "$arg" | tr '[:upper:]' '[:lower:]')"
    local branch
    branch="$(git -C "$REPO" branch -a --format='%(refname:short)' \
        | grep -i "$id_lower" | head -1 || true)"
    if [ -n "$branch" ]; then
        git -C "$REPO" rev-parse "$branch" 2>/dev/null || echo "$arg"
        return
    fi
    echo "$arg"
}

is_ancestor() {
    local sha="$1"
    git -C "$REPO" merge-base --is-ancestor "$sha" origin/main 2>/dev/null
}

squash_subject() {
    git -C "$REPO" log --format="%s" -1 "$1" 2>/dev/null || true
}

is_squash_equivalent() {
    local sha="$1"
    local subject
    subject="$(squash_subject "$sha")"
    [ -z "$subject" ] && return 1

    # Subject present in origin/main log?
    if git -C "$REPO" log --format="%s" --max-count=2000 origin/main 2>/dev/null \
        | grep -qxF "$subject"; then
        return 0
    fi

    # git cherry: "- " prefix means equivalent
    if git -C "$REPO" cherry origin/main "$sha" 2>/dev/null \
        | grep -q "^- "; then
        return 0
    fi

    return 1
}

for arg in "$@"; do
    sha="$(resolve_sha "$arg")"
    full_sha="$(git -C "$REPO" rev-parse "$sha" 2>/dev/null || echo "$sha")"
    subject="$(git -C "$REPO" log --format="%s" -1 "$full_sha" 2>/dev/null || echo "(unknown)")"

    if is_ancestor "$full_sha" 2>/dev/null || is_squash_equivalent "$full_sha"; then
        echo "in main: $sha ($subject)"
    else
        MISSING+=("$sha $subject")
        echo "NOT IN MAIN: $sha $subject"
    fi
done

if [ ${#MISSING[@]} -gt 0 ]; then
    echo ""
    echo "VERIFICATION FAILED: ${#MISSING[@]} SHA(s) not in origin/main"
    exit 1
fi

echo ""
echo "ALL MERGED $MAIN_SHA"
