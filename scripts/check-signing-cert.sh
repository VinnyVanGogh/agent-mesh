#!/usr/bin/env bash
# Check the codesigning certificate used by reinstall-daemon.sh.
#
# staypointd must be signed with a real certificate so macOS remembers the
# Documents-folder permission across rebuilds. If the certificate goes missing
# or expires, signing falls back to ad-hoc and the permission popup returns on
# every rebuild.
#
# Exit codes: 0 = ok, 1 = missing or expired, 2 = expires within WARN_DAYS.
# Prints "IDENTITY=<sha1>" and "DAYS_LEFT=<n>" lines for callers to parse.
#
#   --notify   also post a macOS notification when not ok (used by the daily
#              reminder LaunchAgent; a notification banner, never a modal).
#   --quiet    print only the machine-readable lines.
set -uo pipefail

WARN_DAYS="${STAYPOINT_CERT_WARN_DAYS:-30}"
NOTIFY=0
QUIET=0
for arg in "$@"; do
    case "$arg" in
        --notify) NOTIFY=1 ;;
        --quiet) QUIET=1 ;;
    esac
done

say() { [[ "$QUIET" -eq 1 ]] || echo "$@"; }

notify() {
    [[ "$NOTIFY" -eq 1 ]] || return 0
    /usr/bin/osascript -e "display notification \"$1\" with title \"StayPoint signing certificate\" sound name \"Funk\"" >/dev/null 2>&1 || true
}

if [[ "$(uname)" != "Darwin" ]]; then
    say "Not macOS; no signing certificate needed."
    exit 0
fi

# `find-identity -v` lists only valid (unexpired, trusted) identities, so an
# expired certificate shows up here as missing.
IDENTITY="${STAYPOINT_SIGN_IDENTITY:-$(security find-identity -v -p codesigning 2>/dev/null | awk 'NR==1 && $2 ~ /^[0-9A-F]{40}$/ {print $2}')}"
if [[ -z "$IDENTITY" ]]; then
    MSG="No valid codesigning certificate (missing or expired). staypointd will be ad-hoc signed and macOS will re-prompt for Documents access after every rebuild. Renew it in Xcode > Settings > Accounts > Manage Certificates."
    say "✗ $MSG"
    notify "Certificate missing or expired. Renew in Xcode > Settings > Accounts."
    exit 1
fi
echo "IDENTITY=$IDENTITY"

# Find the certificate whose SHA-1 matches the identity and read its expiry.
PEM="$(security find-certificate -a -Z -p 2>/dev/null | awk -v want="$IDENTITY" '
    /^SHA-1 hash:/ { cur = $3 }
    /BEGIN CERTIFICATE/ { if (cur == want) grab = 1 }
    grab { print }
    /END CERTIFICATE/ { if (grab) exit }
')"
if [[ -z "$PEM" ]]; then
    # A hash passed via STAYPOINT_SIGN_IDENTITY may be a name, not a SHA-1.
    PEM="$(security find-certificate -p -c "$IDENTITY" 2>/dev/null)"
fi
if [[ -z "$PEM" ]]; then
    say "✗ Could not read certificate for identity $IDENTITY"
    notify "Could not read the signing certificate."
    exit 1
fi

END="$(echo "$PEM" | /usr/bin/openssl x509 -noout -enddate 2>/dev/null | cut -d= -f2)"
END_EPOCH="$(date -j -u -f "%b %e %T %Y %Z" "$END" +%s 2>/dev/null || echo 0)"
DAYS_LEFT=$(( (END_EPOCH - $(date +%s)) / 86400 ))
echo "DAYS_LEFT=$DAYS_LEFT"

if [[ "$END_EPOCH" -le "$(date +%s)" ]]; then
    say "✗ Signing certificate expired on $END. Renew it in Xcode > Settings > Accounts > Manage Certificates."
    notify "Certificate EXPIRED on $END. Renew in Xcode > Settings > Accounts."
    exit 1
fi
if [[ "$DAYS_LEFT" -le "$WARN_DAYS" ]]; then
    say "⚠ Signing certificate expires in $DAYS_LEFT days ($END). Renew it in Xcode > Settings > Accounts > Manage Certificates, then run scripts/reinstall-daemon.sh."
    notify "Certificate expires in $DAYS_LEFT days ($END). Renew in Xcode > Settings > Accounts."
    exit 2
fi

say "✓ Signing certificate valid for $DAYS_LEFT more days ($END)."
exit 0
