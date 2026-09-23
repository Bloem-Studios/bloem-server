#!/usr/bin/env bash
# Bloem: CI's "Lint changed lines" gate.
#
# An ordinary commit is linted on the lines it changed relative to BASE_SHA.
# An upstream sync merge is different: relative to its first parent, every
# line Silo brought in counts as "changed", so the gate would fail on Silo's
# own lint debt (code Bloem must not edit; see contracts/seams.txt). For a
# merge commit only the lines that differ from BOTH parents are Bloem's own
# work (the conflict resolutions), so findings are reported on those alone.
#
# Usage: BASE_SHA=<sha> scripts/bloem-lint-changed-ci.sh
set -euo pipefail

base_sha=${BASE_SHA:-}
if [ -z "$base_sha" ] || [[ "$base_sha" =~ ^0+$ ]] || ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
	base_sha="$(git rev-parse HEAD^)"
fi

if ! git rev-parse -q --verify HEAD^2 >/dev/null; then
	exec golangci-lint run --new-from-merge-base="$base_sha" ./...
fi

report=$(mktemp)
trap 'rm -f "$report"' EXIT
# Exit 1 means "issues found" and the filter below decides which count.
# Anything else (timeout, crash, bad config) is a failed gate, not a pass.
rc=0
golangci-lint run --new-from-rev=HEAD^1 --output.json.path="$report" ./... >/dev/null || rc=$?
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ]; then
	echo "golangci-lint failed with exit code $rc" >&2
	exit "$rc"
fi

python3 - "$report" <<'PY'
import json, re, subprocess, sys

# Lines of HEAD that differ from the second (upstream) parent.
diff = subprocess.run(["git", "diff", "-U0", "--no-color", "HEAD^2", "HEAD", "--", "*.go"],
                      capture_output=True, text=True, check=True).stdout
own = {}
path = None
for line in diff.splitlines():
    if line.startswith("+++ "):
        path = None if line == "+++ /dev/null" else line[6:]
    elif line.startswith("@@") and path:
        m = re.search(r"\+(\d+)(?:,(\d+))?", line)
        start, count = int(m.group(1)), int(m.group(2) or "1")
        own.setdefault(path, set()).update(range(start, start + count))

raw = open(sys.argv[1]).read()
if not raw.strip():
    sys.exit("golangci-lint produced no report")
data = json.loads(raw)
issues = [i for i in (data.get("Issues") or [])
          if i["Pos"]["Line"] in own.get(i["Pos"]["Filename"], ())]
for i in issues:
    p = i["Pos"]
    print(f'{p["Filename"]}:{p["Line"]}:{p.get("Column", 0)}: {i["Text"]} ({i["FromLinter"]})')
print(f"{len(issues)} lint issue(s) on lines this merge's resolution changed", file=sys.stderr)
sys.exit(1 if issues else 0)
PY
