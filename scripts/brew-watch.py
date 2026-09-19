#!/usr/bin/env python3
"""Watch Agent-Mesh's journey through CI/CD, GitHub Release, and Homebrew Tap.

Pipeline Stages:
    1. Tag Pushed       -> v0.1.0 git tag detected
    2. CI/CD Matrix     -> GitHub Actions builds & tests passing
    3. GoReleaser       -> Multi-platform binaries attached to GitHub Release
    4. Tap Synced       -> Formula/mesh.rb committed to VinnyVanGogh/homebrew-tap
    5. LIVE ON BREW     -> 'brew tap' resolves and formula is installable!

Usage:
    python3 scripts/brew-watch.py          # Continuous monitor with notifications
    python3 scripts/brew-watch.py --peek   # Print current picture once and exit
"""

import json
import os
import shutil
import subprocess
import sys
import time
import urllib.error
import urllib.request

REPO_MAIN = "VinnyVanGogh/agent-mesh"
REPO_TAP = "VinnyVanGogh/homebrew-tap"
TAG = "v0.1.0"
VERSION = "0.1.0"

PEEK = "--peek" in sys.argv

def run_cmd(args, timeout=30):
    try:
        out = subprocess.run(args, capture_output=True, text=True, timeout=timeout)
        return out.stdout.strip() if out.returncode == 0 else None
    except Exception:
        return None

def notify(title, message):
    # 1. macOS native notification
    if shutil.which("osascript"):
        script = f'display notification "{message}" with title "{title}" sound name "Glass"'
        subprocess.run(["osascript", "-e", script], capture_output=True)
    # 2. terminal-notifier fallback
    if shutil.which("terminal-notifier"):
        subprocess.run(["terminal-notifier", "-title", title, "-message", message], capture_output=True)

def check_ci_status():
    raw = run_cmd(["gh", "run", "list", "--repo", REPO_MAIN, "--limit", "1", "--json", "status,conclusion,databaseId,headBranch,headSha"])
    if not raw:
        return {"state": "unknown", "id": None}
    try:
        runs = json.loads(raw)
        if runs:
            r = runs[0]
            status = r.get("status")
            conclusion = r.get("conclusion")
            return {
                "id": r.get("databaseId"),
                "status": status,
                "conclusion": conclusion,
                "branch": r.get("headBranch")
            }
    except Exception:
        pass
    return {"state": "unknown", "id": None}

def check_github_release():
    raw = run_cmd(["gh", "release", "view", TAG, "--repo", REPO_MAIN, "--json", "assets,isDraft,isPrerelease,tagName,url"])
    if not raw:
        return None
    try:
        data = json.loads(raw)
        assets = [a["name"] for a in data.get("assets", [])]
        return {
            "tag": data.get("tagName"),
            "url": data.get("url"),
            "assets": assets,
            "count": len(assets)
        }
    except Exception:
        return None

def check_tap_formula():
    raw = run_cmd(["gh", "api", f"repos/{REPO_TAP}/contents/Formula/mesh.rb"])
    if not raw:
        return False
    try:
        data = json.loads(raw)
        return "download_url" in data or "name" in data
    except Exception:
        return False

def check_brew_live():
    # Test if brew can see the formula in the tap
    info = run_cmd(["brew", "info", f"{REPO_TAP}/mesh"])
    if info and "mesh" in info and VERSION in info:
        return True
    return False

def render_status():
    print("\033[1;36m[Agent-Mesh :: Release & Homebrew Pipeline Monitor]\033[0m")
    print(f"  • Target Tag:    \033[1;33m{TAG}\033[0m")
    print(f"  • Source Repo:   https://github.com/{REPO_MAIN}")
    print(f"  • Tap Repo:      https://github.com/{REPO_TAP}")
    print()

    # Stage 1: CI
    ci = check_ci_status()
    ci_icon = "\033[1;33m⏳ RUNNING\033[0m"
    if ci.get("status") == "completed":
        if ci.get("conclusion") == "success":
            ci_icon = "\033[1;32m✔ SUCCESS\033[0m"
        else:
            ci_icon = f"\033[1;31m✖ FAILED ({ci.get('conclusion')})\033[0m"
    print(f"  1. CI/CD Matrix:    {ci_icon} (run #{ci.get('id')})")

    # Stage 2: GitHub Release
    rel = check_github_release()
    if rel and rel.get("count", 0) > 0:
        rel_icon = f"\033[1;32m✔ PUBLISHED\033[0m ({rel['count']} binaries: {', '.join(rel['assets'][:3])}...)"
    else:
        rel_icon = "\033[1;33m⏳ PENDING GORELEASER\033[0m"
    print(f"  2. GitHub Release:  {rel_icon}")

    # Stage 3: Homebrew Tap
    tap_ok = check_tap_formula()
    if tap_ok:
        tap_icon = "\033[1;32m✔ FORMULA SYNCED\033[0m"
    else:
        tap_icon = "\033[1;33m⏳ AWAITING FORMULA\033[0m"
    print(f"  3. Tap Repository:  {tap_icon}")

    # Stage 4: Live on Brew
    brew_ok = check_brew_live()
    if brew_ok:
        brew_icon = "\033[1;32m🚀 LIVE ON BREW! ('brew install mesh')\033[0m"
    else:
        brew_icon = "\033[1;33m⏳ PROPAGATING\033[0m"
    print(f"  4. Homebrew Install: {brew_icon}")
    print()

    return ci, rel, tap_ok, brew_ok

def main():
    if PEEK:
        render_status()
        return

    print("\033[1;32mWatching release pipeline until Agent-Mesh is live on Homebrew...\033[0m")
    last_stage = 0

    while True:
        os.system("clear")
        ci, rel, tap_ok, brew_ok = render_status()

        if ci.get("conclusion") == "success" and last_stage < 1:
            notify("Agent-Mesh CI Passed", "GitHub Actions test matrix passed 100%!")
            last_stage = 1

        if rel and rel.get("count", 0) > 0 and last_stage < 2:
            notify("Agent-Mesh Release Built", f"v0.1.0 release published with {rel['count']} binaries!")
            last_stage = 2

        if brew_ok:
            notify("Agent-Mesh is LIVE!", "Run 'brew install mesh' now!")
            print("\033[1;32m🎉 Pipeline Complete! Agent-Mesh is live on Homebrew.\033[0m")
            print("To install:")
            print(f"  \033[1;36mbrew tap {REPO_TAP}\033[0m")
            print("  \033[1;36mbrew install mesh\033[0m\n")
            break

        time.sleep(8)

if __name__ == "__main__":
    main()
