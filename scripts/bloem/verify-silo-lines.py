#!/usr/bin/env python3
"""Fail when Bloem overrides a line of Silo's that it did not override before.

Silo always wins: Silo's lines stay exactly as Silo wrote them, and Bloem's
behavior goes on its own lines beside them or in its own files. The seam
ledger (contracts/seams.txt, scripts/verify-seams.sh) only lists which Silo
files Bloem touches. This check looks inside them: it records every Silo line
Bloem changes or removes, in contracts/silo-line-changes.json, and fails when
that set grows or changes.

That catches the two ways Bloem drifts from Silo:

- a change or a merge resolution that replaces one of Silo's lines with a
  Bloem one, or deletes it;
- a Silo change to a line Bloem had already replaced. The merge takes Silo's
  new line into the comparison, the old Bloem line still overrides it, and
  the overridden text no longer matches the record, so Bloem cannot keep its
  version by accident.

The record may only shrink. When a change restores Silo's lines, the check
fails until the record is rewritten without them (make silo-lines-ledger), so
the record always says exactly what Bloem still overrides. Rewriting it to add
lines needs a reviewed reason; the review shows the added Silo lines in the
record's diff.

Silo's version of each file is the Silo commit Bloem last integrated: the
merge base of upstream/main and HEAD, or of upstream/main and MERGE_HEAD
during an uncommitted merge, so the incoming Silo version counts as Silo's.
The working tree is compared, so the check answers for the change about to be
committed.

Generated files (the record marks them "generated") are listed but their lines
are not compared: they follow from the code, and checking the code checks them.

A Bloem line can also leave Silo's lines intact and still keep Silo's code from
running: an added return, continue, break or throw inside a Silo function. When
Silo later changes that function, the merge is clean and Bloem's line still
skips the change. So the record also holds, for every Silo function with such a
line, a fingerprint of the function as Silo wrote it ("hooks"), and the check
fails when Silo's function changes, so someone checks that Bloem's line still
fits before the record is rewritten. A Silo function is found by the context
git prints in each hunk header; lines inside a function Bloem adds whole are
Bloem's own and do not count.

Commands assume the repository root is the cwd and upstream/main is fetched.
"""
import argparse
import collections
import hashlib
import json
import os
import re
import subprocess
import sys

BASE_REF = os.environ.get("SILO_LINES_BASE_REF", "upstream/main")
LEDGER = os.environ.get("SILO_LINES_LEDGER", "contracts/silo-line-changes.json")


def git(*args, binary=False):
    out = subprocess.run(
        ["git", "-c", "core.quotepath=false", *args], check=True, capture_output=True
    ).stdout
    return out if binary else out.decode("utf-8", "replace")


def silo_commit():
    """The Silo commit Bloem last integrated, or the incoming one mid-merge."""
    git("rev-parse", "--verify", "--quiet", BASE_REF + "^{commit}")
    try:
        git("rev-parse", "--verify", "--quiet", "MERGE_HEAD")
        head = "MERGE_HEAD"
    except subprocess.CalledProcessError:
        head = "HEAD"
    return git("merge-base", BASE_REF, head).strip()


def parse_removed(diff_text):
    """Map each file in a -U0 diff to the lines the new side removed.

    Only "-" lines count: a changed line is one removed Silo line plus one
    added line, and a line Bloem only adds removes nothing of Silo's.
    """
    removed = collections.defaultdict(list)
    path = None
    for line in diff_text.splitlines():
        if line.startswith("diff --git "):
            path = None
            continue
        if line.startswith("--- "):
            path = line[6:] if line.startswith("--- a/") else None
            continue
        if line.startswith("+++ ") or line.startswith("@@"):
            continue
        if path is not None and line.startswith("-"):
            removed[path].append(line[1:].rstrip())
    return removed


HOOK_SUFFIXES = (".go", ".ts", ".tsx", ".js", ".mjs", ".rego")
# A line that can skip the rest of the Silo function it sits in.
CONTROL = re.compile(r"\b(?:return|continue|break|throw)\b")
# An added line that starts a declaration of Bloem's own at column 0: the
# hunk's later lines are that declaration's, not a hook in Silo's function.
OWN_DECLARATION = re.compile(
    r"^(?:func |function |async function |export (?:default )?(?:async )?function |export (?:const|let|class) |const |let |class )"
)
COMMENT = re.compile(r"^\s*(?://|#|\*|/\*)")


def parse_hooks(diff_text):
    """Map each file to the Silo functions in which Bloem added a line that can
    skip Silo's code, keyed by the hunk header's function context."""
    hooks = collections.defaultdict(set)
    path = header = None
    own = False
    for line in diff_text.splitlines():
        if line.startswith("diff --git "):
            path = None
            continue
        if line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else None
            continue
        if line.startswith("--- "):
            continue
        if line.startswith("@@"):
            parts = line.split("@@", 2)
            header = parts[2].strip() if len(parts) > 2 else ""
            own = False
            continue
        if path is None or not path.endswith(HOOK_SUFFIXES) or not line.startswith("+"):
            continue
        text = line[1:]
        if OWN_DECLARATION.match(text):
            own = True
            continue
        if own or not header or COMMENT.match(text):
            continue
        if CONTROL.search(text.split("//", 1)[0]):
            hooks[path].add(header)
    return hooks


def silo_function(lines, header):
    """The text of the Silo declaration a hunk header names: from its line to
    the next line that closes a top-level block. None when it is not found."""
    for i, line in enumerate(lines):
        if line.startswith(header):
            end = i + 1
            while end < len(lines) and not lines[end].startswith(("}", ")")):
                end += 1
            return "\n".join(lines[i : end + 1])
    return None


def fingerprint(text):
    return "missing" if text is None else hashlib.sha256(text.encode("utf-8")).hexdigest()[:16]


def current_hooks(silo, diff_text):
    hooks = {}
    for path, headers in parse_hooks(diff_text).items():
        try:
            lines = git("show", f"{silo}:{path}").split("\n")
        except subprocess.CalledProcessError:
            continue
        hooks[path] = {header: fingerprint(silo_function(lines, header)) for header in sorted(headers)}
    return hooks


def current_state(silo):
    """Every Silo file the working tree changes, deletes or replaces, with the
    Silo lines it overrides."""
    changed = [p for p in git("diff", "--name-only", "--no-renames", "--diff-filter=MDT", silo).split("\n") if p]
    if not changed:
        return {}, {}
    numstat = git("diff", "--numstat", "--no-renames", silo, "--", *changed)
    binary = {line.split("\t", 2)[2] for line in numstat.splitlines() if line.startswith("-\t-\t")}
    present = set(git("ls-files", "-z", "--", *changed).split("\0"))
    diff = git("diff", "-U0", "--no-renames", "--no-color", "--no-ext-diff", silo, "--", *changed)
    removed = parse_removed(diff)
    state = {}
    for path in changed:
        if path not in present or not os.path.lexists(path):
            state[path] = {"kind": "deleted", "silo_blob": git("rev-parse", f"{silo}:{path}").strip()}
        elif path in binary:
            state[path] = {"kind": "binary", "silo_blob": git("rev-parse", f"{silo}:{path}").strip()}
        elif removed.get(path):
            state[path] = {"kind": "lines", "lines": sorted(removed[path])}
    return state, current_hooks(silo, diff)


def load_ledger():
    if not os.path.exists(LEDGER):
        return {}, {}
    with open(LEDGER, encoding="utf-8") as f:
        record = json.load(f)
    return record["files"], record.get("hooks", {})


def compare_hooks(ledger, state):
    """Problems with the recorded hooks, in compare's shape."""
    problems = []
    for path in sorted(set(ledger) | set(state)):
        old, new = ledger.get(path, {}), state.get(path, {})
        for header in sorted(set(old) | set(new)):
            if header not in old:
                problems.append((path, f"a new Bloem line can skip Silo's code in: {header}", []))
            elif header not in new:
                problems.append((path, f"no Bloem line skips Silo's code in {header} any more: shrink the record", []))
            elif old[header] != new[header]:
                problems.append((path, f"Silo changed {header}, where a Bloem line skips Silo's code: check that "
                                       "Bloem's line still fits Silo's new code", []))
    return problems


def compare(ledger, state):
    """Problems, as (path, message, detail lines). Empty means current."""
    problems = []
    for path in sorted(set(ledger) | set(state)):
        old, new = ledger.get(path), state.get(path)
        if old is None:
            what = "lines" if new["kind"] == "lines" else f"file ({new['kind']})"
            problems.append((path, f"Bloem now overrides Silo's {what} here", new.get("lines", [])))
            continue
        if new is None:
            problems.append((path, "Bloem no longer overrides anything of Silo's here: remove it from the record", []))
            continue
        if old["kind"] == "generated":
            continue
        if old["kind"] != new["kind"]:
            problems.append((path, f"was {old['kind']}, now {new['kind']}", new.get("lines", [])))
            continue
        if new["kind"] in ("deleted", "binary"):
            if old.get("silo_blob") != new.get("silo_blob"):
                problems.append((path, "Silo changed this file, which Bloem deletes or replaces", []))
            continue
        before = collections.Counter(old["lines"])
        after = collections.Counter(new["lines"])
        added = sorted((after - before).elements())
        restored = sorted((before - after).elements())
        if added:
            problems.append((path, f"{len(added)} more Silo line(s) overridden", added))
        elif restored:
            problems.append((path, f"{len(restored)} Silo line(s) restored: shrink the record", restored))
    return problems


def write_ledger(ledger, state, hooks, silo):
    files = {}
    for path, entry in state.items():
        if ledger.get(path, {}).get("kind") == "generated":
            files[path] = {"kind": "generated"}
        else:
            files[path] = entry
    with open(LEDGER, "w", encoding="utf-8") as f:
        json.dump({"silo": silo, "files": dict(sorted(files.items())), "hooks": hooks}, f, indent=1,
                  ensure_ascii=False, sort_keys=True)
        f.write("\n")


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n", 1)[0])
    parser.add_argument("--write", action="store_true", help="rewrite the record from the working tree")
    args = parser.parse_args(argv)
    try:
        silo = silo_commit()
    except subprocess.CalledProcessError:
        print(f"::error::{BASE_REF} is not available; run: git fetch upstream main", file=sys.stderr)
        return 2
    ledger, ledger_hooks = load_ledger()
    state, hooks = current_state(silo)
    if args.write:
        write_ledger(ledger, state, hooks, silo)
        total = sum(len(e.get("lines", [])) for p, e in state.items() if ledger.get(p, {}).get("kind") != "generated")
        print(f"wrote {LEDGER}: {len(state)} Silo files, {total} overridden Silo lines, "
              f"{sum(len(h) for h in hooks.values())} hooked Silo functions (silo {silo[:9]})")
        return 0
    problems = compare(ledger, state) + compare_hooks(ledger_hooks, hooks)
    if problems:
        for path, message, detail in problems:
            print(f"{path}: {message}")
            for line in detail[:20]:
                print(f"    - {line[:200]}")
            if len(detail) > 20:
                print(f"    ... and {len(detail) - 20} more")
        print(
            f"::error::{len(problems)} problem(s) against {LEDGER}. Silo always wins: restore Silo's lines word for "
            "word and put Bloem's behavior on its own lines beside them. Where Silo changed a function a Bloem line "
            "skips, check Bloem's line against Silo's new code. Then run make silo-lines-ledger; adding to the "
            "record needs a reviewed reason.",
            file=sys.stderr,
        )
        return 1
    total = sum(len(e.get("lines", [])) for e in ledger.values())
    print(f"Bloem overrides only the recorded Silo lines: {len(ledger)} files, {total} lines, "
          f"{sum(len(h) for h in ledger_hooks.values())} hooked Silo functions (silo {silo[:9]})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
