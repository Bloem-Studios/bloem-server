#!/usr/bin/env bash
# Bloem: CI's "Lint changed lines" gate.
#
# A finding is reported only when its line both
#   (1) changed in this push (relative to BASE_SHA), and
#   (2) differs from upstream Silo's copy of the file (or the file is Bloem's).
# (1) is the usual changed-lines rule. (2) keeps Silo's own lint debt out of
# Bloem's gate: an upstream sync merge brings in lines identical to Silo, and
# restoring a Silo file to upstream's text (seam reduction) must not count as
# a Bloem change either. Bloem's own code, in any file, is still linted.
#
# Usage: BASE_SHA=<sha> scripts/bloem-lint-changed-ci.sh
set -euo pipefail

base_sha=${BASE_SHA:-}
if [ -z "$base_sha" ] || [[ "$base_sha" =~ ^0+$ ]] || ! git cat-file -e "${base_sha}^{commit}" 2>/dev/null; then
	base_sha="$(git rev-parse HEAD^)"
fi

upstream_ref=${UPSTREAM_REF:-refs/remotes/upstream/main}
if ! git rev-parse -q --verify "$upstream_ref" >/dev/null; then
	# Silo is public: no credential is needed.
	git fetch --no-tags --quiet https://github.com/Silo-Server/silo-server.git "+refs/heads/main:${upstream_ref}"
fi

report=$(mktemp)
trap 'rm -f "$report"' EXIT
# Exit 1 means "issues found" and the filter below decides which count.
# Anything else (timeout, crash, bad config) is a failed gate, not a pass.
rc=0
golangci-lint run --timeout=30m --new-from-rev="$base_sha" --output.json.path="$report" ./... >/dev/null || rc=$?
if [ "$rc" -ne 0 ] && [ "$rc" -ne 1 ]; then
	echo "golangci-lint failed with exit code $rc" >&2
	exit "$rc"
fi

python3 - "$report" "$upstream_ref" <<'PY'
import json, re, subprocess, sys

report, upstream = sys.argv[1], sys.argv[2]
raw = open(report).read()
if not raw.strip():
    sys.exit("golangci-lint produced no report")
issues = json.loads(raw).get("Issues") or []

# Lines of the working tree that differ from upstream, per file. A file absent
# upstream diffs as wholly added, so every line of Bloem's own files counts.
diff = subprocess.run(["git", "diff", "-U0", "--no-color", upstream, "--", "*.go"],
                      capture_output=True, text=True, check=True).stdout
bloem = {}
path = None
for line in diff.splitlines():
    if line.startswith("+++ "):
        path = None if line == "+++ /dev/null" else line[6:]
    elif line.startswith("@@") and path:
        m = re.search(r"\+(\d+)(?:,(\d+))?", line)
        start, count = int(m.group(1)), int(m.group(2) or "1")
        bloem.setdefault(path, set()).update(range(start, start + count))

kept = [i for i in issues if i["Pos"]["Line"] in bloem.get(i["Pos"]["Filename"], ())]
for i in kept:
    p = i["Pos"]
    print(f'{p["Filename"]}:{p["Line"]}:{p.get("Column", 0)}: {i["Text"]} ({i["FromLinter"]})')
print(f"{len(kept)} lint issue(s) on Bloem's changed lines ({len(issues) - len(kept)} on lines identical to Silo ignored)", file=sys.stderr)
sys.exit(1 if kept else 0)
PY
