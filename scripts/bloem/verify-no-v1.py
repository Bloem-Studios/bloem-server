#!/usr/bin/env python3
"""Fail when a line Bloem adds names Silo's retired /api/v1 or Bloem's retired
/api/bloem/v1.

Silo answers every /api/v1 route with 410 Gone (only /api/v1/health and
/api/v1/ready stay), and Bloem's own API is /api/bloem/v2, so a Bloem caller,
test or doc that still spells either path is a bug.

Only Bloem's lines are checked: the diff from the last merged Silo commit (the
merge base with upstream/main, as scripts/verify-seams.sh uses), so Silo's own
files and lines stay as Silo wrote them. A URL with a host
(https://openrouter.ai/api/v1, an external service's own v1) is not Silo's and
passes. Paths in contracts/bloem-v1-allow.txt pass: dated records, the
migrations that rewrite stored paths, and their tests. A single line that must
spell a retired path, such as a test asserting it is refused, carries the
comment "v1-guard: allow".

Commands assume the repository root is the cwd and upstream/main is fetched.
"""
import fnmatch
import os
import re
import subprocess
import sys

BASE_REF = os.environ.get("V1_GUARD_BASE_REF", "upstream/main")
ALLOW = os.environ.get("V1_GUARD_ALLOW", "contracts/bloem-v1-allow.txt")

# A retired path, not preceded by a host or another path segment.
RETIRED = re.compile(r"(?<![A-Za-z0-9._~%/:@-])/api/(?:bloem/)?v1(?![A-Za-z0-9_.-])")
# A line that must spell a retired path, such as a test asserting it is
# refused, carries this marker in a comment.
MARKER = "v1-guard: allow"
KEPT = re.compile(r"/api/v1/(?:health|ready)(?![A-Za-z0-9_-])")


def names_retired_api(text):
    """Whether one line names a retired path, outside a marked line."""
    if MARKER in text:
        return False
    # A regular expression spells the path with escaped slashes.
    return bool(RETIRED.search(KEPT.sub("", text.replace("\\/", "/"))))


def git(*args):
    return subprocess.run(["git", "-c", "core.quotepath=false", *args], check=True, capture_output=True, text=True).stdout


def main():
    try:
        git("rev-parse", "--verify", "--quiet", BASE_REF + "^{commit}")
    except subprocess.CalledProcessError:
        print(f"::error::{BASE_REF} is not available; run: git fetch upstream main", file=sys.stderr)
        return 2
    base = git("merge-base", BASE_REF, "HEAD").strip()
    patterns = []
    with open(ALLOW, encoding="utf-8") as f:
        for line in f:
            line = line.split("#", 1)[0].strip()
            if line:
                patterns.append(line)
    diff = subprocess.run(
        ["git", "-c", "core.quotepath=false", "diff", "-U0", "--no-renames", "--no-color", "--no-ext-diff", base, "--", "."],
        check=True, capture_output=True,
    ).stdout.decode("utf-8", "replace")
    found = []
    path = None
    for line in diff.splitlines():
        if line.startswith("+++ "):
            path = line[6:] if line.startswith("+++ b/") else None
            continue
        if not line.startswith("+") or path is None:
            continue
        if names_retired_api(line[1:]) and not any(fnmatch.fnmatchcase(path, p) for p in patterns):
            found.append(f"{path}: {line[1:].strip()[:200]}")
    if found:
        print("\n".join(found))
        print(f"::error::{len(found)} Bloem line(s) name a retired API (/api/v1 or /api/bloem/v1). Use Silo's /api/v2 or "
              f"Bloem's /api/bloem/v2; a dated record or a path-rewriting migration goes in {ALLOW} with its reason.",
              file=sys.stderr)
        return 1
    print(f"no Bloem line names /api/v1 or /api/bloem/v1 (base {base[:9]})")
    return 0


if __name__ == "__main__":
    sys.exit(main())
