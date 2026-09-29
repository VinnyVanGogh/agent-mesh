#!/usr/bin/env python3
"""Regenerate internal/telemetry/ratecard/ratecard.json from a models.dev snapshot.

Usage: curl -sS https://models.dev/api.json -o api.json && scripts/gen_ratecard.py api.json

Runs at maintainer time only. StayPoint never touches the network at runtime.
Rates are emitted as integer micro-dollars per million tokens ($/Mtok * 1e6).
"""
import json
import sys
from decimal import Decimal

# First-party price lists only; resellers add markup and duplicate entries.
PROVIDERS = ["anthropic", "openai", "google", "xai", "deepseek", "mistral"]


def micros(v):
    return int((Decimal(str(v)) * 1_000_000).to_integral_value())


def rates(cost, base=None):
    out = {}
    for key in ("input", "output", "cache_read", "cache_write"):
        if key in cost:
            out[key] = micros(cost[key])
        elif base is not None and key in base:
            out[key] = base[key]
    return out


def main(path):
    api = json.load(open(path))
    models = {}
    for pid in PROVIDERS:
        for mid, m in sorted(api.get(pid, {}).get("models", {}).items()):
            c = m.get("cost")
            if not c or "input" not in c or "output" not in c:
                continue
            e = rates(c)
            # models.dev carries no 1h cache-write rate; Anthropic bills it at 2x input.
            if pid == "anthropic" and "cache_write" in e:
                e["cache_write_1h"] = e["input"] * 2
            tiers = []
            for t in c.get("tiers", []):
                if t.get("tier", {}).get("type") != "context":
                    continue
                te = rates(t, e)
                if "cache_write_1h" in e:
                    te["cache_write_1h"] = te["input"] * 2
                te["context_over"] = t["tier"]["size"]
                tiers.append(te)
            tiers.sort(key=lambda t: t["context_over"])
            if tiers:
                e["tiers"] = tiers
            e["provider"] = pid
            models[mid] = e
    card = {
        "source": "https://models.dev/api.json",
        "license": "MIT (c) 2025 models.dev",
        "unit": "micro-USD per 1M tokens",
        "models": models,
    }
    with open("internal/telemetry/ratecard/ratecard.json", "w") as f:
        json.dump(card, f, indent=1, sort_keys=True)
        f.write("\n")
    print(len(models), "models")


main(sys.argv[1])
