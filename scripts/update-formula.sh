#!/usr/bin/env bash
# update-formula.sh — Fetch release archives from GitHub and patch Formula/staypoint.rb
# Usage: scripts/update-formula.sh --version v0.2.0 [--tap-dir ../homebrew-tap]
set -euo pipefail

REPO="VinnyVanGogh/staypoint"
VERSION=""
TAP_DIR=""
DRY_RUN=false

usage() {
  echo "Usage: $0 --version <vX.Y.Z> [--tap-dir <path>] [--dry-run]"
  exit 1
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --version) VERSION="$2"; shift 2 ;;
    --tap-dir) TAP_DIR="$2"; shift 2 ;;
    --dry-run) DRY_RUN=true; shift ;;
    *) usage ;;
  esac
done

[[ -z "$VERSION" ]] && usage

# Strip leading 'v' for filenames, keep it for URLs
TAG="$VERSION"
VER="${VERSION#v}"

# Resolve tap directory
if [[ -z "$TAP_DIR" ]]; then
  SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  TAP_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)/homebrew-tap"
fi
FORMULA="$TAP_DIR/Formula/staypoint.rb"

if [[ ! -f "$FORMULA" ]]; then
  echo "ERROR: Formula not found at $FORMULA" >&2
  exit 1
fi

echo "Updating Formula/staypoint.rb → $TAG"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

BASE_URL="https://github.com/$REPO/releases/download/$TAG"

compute_sha256() {
  local filename="$1"
  local url="$BASE_URL/$filename"
  local dest="$TMPDIR/$filename"
  echo -n "  Downloading $filename ... " >&2
  if curl -fsSL --retry 3 -o "$dest" "$url"; then
    echo "OK" >&2
  else
    echo "FAILED (release asset may not exist yet)" >&2
    echo "PLACEHOLDER_${filename}"
    return 0
  fi
  shasum -a 256 "$dest" | awk '{print $1}'
}

# Compute checksums for all 4 platform archives
DARWIN_ARM64_SHA=$(compute_sha256 "staypoint_${VER}_darwin_arm64.tar.gz")
DARWIN_AMD64_SHA=$(compute_sha256 "staypoint_${VER}_darwin_amd64.tar.gz")
LINUX_ARM64_SHA=$(compute_sha256 "staypoint_${VER}_linux_arm64.tar.gz")
LINUX_AMD64_SHA=$(compute_sha256 "staypoint_${VER}_linux_amd64.tar.gz")

echo ""
echo "Checksums:"
echo "  darwin/arm64 : $DARWIN_ARM64_SHA"
echo "  darwin/amd64 : $DARWIN_AMD64_SHA"
echo "  linux/arm64  : $LINUX_ARM64_SHA"
echo "  linux/amd64  : $LINUX_AMD64_SHA"

if $DRY_RUN; then
  echo ""
  echo "[dry-run] Would patch $FORMULA — no changes written."
  exit 0
fi

# Patch version, URLs, and SHAs in-place using a Python one-liner to avoid
# cross-platform sed -i differences between macOS and Linux.
python3 - "$FORMULA" "$TAG" "$VER" \
  "$DARWIN_ARM64_SHA" "$DARWIN_AMD64_SHA" \
  "$LINUX_ARM64_SHA"  "$LINUX_AMD64_SHA" <<'PYEOF'
import re, sys

formula_path, tag, ver, d_arm, d_amd, l_arm, l_amd = sys.argv[1:]

with open(formula_path) as fh:
    src = fh.read()

# Version line
src = re.sub(r'version "\S+"', f'version "{ver}"', src)

# darwin arm64 url + sha
src = re.sub(
    r'(on_macos do\s+if Hardware::CPU\.arm\?\s+url ")https://[^"]+(")',
    rf'\1https://github.com/VinnyVanGogh/staypoint/releases/download/{tag}/staypoint_{ver}_darwin_arm64.tar.gz\2',
    src, flags=re.DOTALL
)
src = re.sub(
    r'(on_macos do\s+if Hardware::CPU\.arm\?.*?sha256 ")([0-9a-f]+)(")',
    rf'\g<1>{d_arm}\3',
    src, flags=re.DOTALL
)

# darwin amd64 url + sha (else branch)
src = re.sub(
    r'(else\s+url ")https://github\.com/VinnyVanGogh/staypoint/releases/download/[^"]*darwin_amd64[^"]*(")',
    rf'\1https://github.com/VinnyVanGogh/staypoint/releases/download/{tag}/staypoint_{ver}_darwin_amd64.tar.gz\2',
    src
)
src = re.sub(
    r'(else\s+url "https://[^"]*darwin_amd64[^"]*"\s+sha256 ")([0-9a-f]+)(")',
    rf'\g<1>{d_amd}\3',
    src
)

# linux arm64 url + sha
src = re.sub(
    r'(on_linux do\s+if Hardware::CPU\.arm\?\s+url ")https://[^"]+(")',
    rf'\1https://github.com/VinnyVanGogh/staypoint/releases/download/{tag}/staypoint_{ver}_linux_arm64.tar.gz\2',
    src, flags=re.DOTALL
)
src = re.sub(
    r'(on_linux do\s+if Hardware::CPU\.arm\?.*?sha256 ")([0-9a-f]+)(")',
    rf'\g<1>{l_arm}\3',
    src, flags=re.DOTALL
)

# linux amd64 url + sha (else branch under on_linux)
src = re.sub(
    r'(else\s+url ")https://github\.com/VinnyVanGogh/staypoint/releases/download/[^"]*linux_amd64[^"]*(")',
    rf'\1https://github.com/VinnyVanGogh/staypoint/releases/download/{tag}/staypoint_{ver}_linux_amd64.tar.gz\2',
    src
)
src = re.sub(
    r'(else\s+url "https://[^"]*linux_amd64[^"]*"\s+sha256 ")([0-9a-f]+)(")',
    rf'\g<1>{l_amd}\3',
    src
)

# Update bottle root_url
src = re.sub(
    r'root_url "https://github\.com/VinnyVanGogh/staypoint/releases/download/[^"]+"',
    f'root_url "https://github.com/VinnyVanGogh/staypoint/releases/download/{tag}"',
    src
)

with open(formula_path, 'w') as fh:
    fh.write(src)

print(f"Patched {formula_path}")
PYEOF

echo ""
echo "Done. Review changes:"
echo "  git -C \"$TAP_DIR\" diff Formula/staypoint.rb"
echo ""
echo "Then commit and push:"
echo "  cd \"$TAP_DIR\" && git add Formula/staypoint.rb && git commit -m \"feat: update staypoint to $TAG\" && git push"
