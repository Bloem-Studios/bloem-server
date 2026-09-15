#!/usr/bin/env python3
"""Re-merge the migration ledger against the current route inventory.

Adding a route to the router changes contracts/api/v2/route-inventory.json in
two ways the ledger cannot absorb by hand: the new row has no ledger entry, and
every later row shifts, which the gate reports as an order violation. It also
renumbers the inventory's middleware-chain table, so the `middleware_chain`
index copied onto hundreds of unrelated entries goes stale at once. None of
that is a decision; all of it is bookkeeping.

This runs the bookkeeping. It calls build_ledger.py with a consumer map rebuilt
from the mechanical call sites already committed in the ledger, so the merge
refreshes the inventory-copied fields and the row order, seeds entries for new
routes, and changes no consumer evidence and no curated decision. Then it runs
assign_sections.py so the new rows land in their delivery unit.

What it deliberately does not do: refresh consumer evidence. That needs the
silo-apple and silo-android trees at a named commit, and it is a separate,
reviewed operation -- run extract_consumers.py, match_consumers.py and
build_ledger.py by hand, as scripts/apiv2-ledger/README.md describes, when the
question is "who calls this route", not "what did the router just change".

Usage (the repository root is the cwd):
  scripts/apiv2-ledger/refresh_ledger.py [--check]

--check reports whether the committed ledger is already what this produces and
leaves the file alone.
"""

import json
import os
import subprocess
import sys
import tempfile

HERE = os.path.dirname(os.path.abspath(__file__))
LEDGER = "contracts/api/v2/migration.json"
INVENTORY = "contracts/api/v2/route-inventory.json"


def main(argv):
    check = "--check" in argv[1:]
    with open(LEDGER, encoding="utf-8") as f:
        before = f.read()
    doc = json.loads(before)

    # The mechanical sites in the committed ledger are exactly what
    # match_consumers.py last produced, so feeding them back in reproduces the
    # same evidence rather than erasing it.
    cmap = {}
    for entry in doc["entries"]:
        key = f"{entry['listener']} {entry['method']} {entry['path']}"
        sites = [
            {"repo": s["repo"], "file": s["file"], "line": s["line"], "types": s["types"]}
            for s in entry["consumer_call_sites"]
            if s["match"] == "mechanical"
        ]
        if sites:
            cmap[key] = sites

    trees = doc["source_trees"]
    with tempfile.NamedTemporaryFile("w", suffix=".json", delete=False, encoding="utf-8") as tmp:
        json.dump(cmap, tmp)
        cmap_path = tmp.name
    try:
        subprocess.run(
            [sys.executable, os.path.join(HERE, "build_ledger.py"), INVENTORY, cmap_path,
             LEDGER, trees["silo-apple"], trees["silo-android"]],
            check=True,
        )
        subprocess.run(
            [sys.executable, os.path.join(HERE, "assign_sections.py"), LEDGER],
            check=True,
        )
    finally:
        os.unlink(cmap_path)

    with open(LEDGER, encoding="utf-8") as f:
        after = f.read()
    if check:
        with open(LEDGER, "w", encoding="utf-8") as f:
            f.write(before)
        if after != before:
            print(f"{LEDGER} is stale; run make migration-ledger", file=sys.stderr)
            return 1
        print(f"{LEDGER}: current")
        return 0
    if after == before:
        print(f"{LEDGER}: unchanged")
    else:
        print(f"{LEDGER}: rewritten in inventory order; review the seeded rows before committing")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
