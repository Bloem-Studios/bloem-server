#!/usr/bin/env python3
"""List what a Silo batch touches that Bloem has to look at while merging it.

    python3 scripts/bloem/silo-merge-review.py --base origin/main --silo upstream/main

Commands assume the repository root is the cwd. The batch is every file that
changed between the merge base of BASE and SILO and SILO itself (renames count
as a deletion plus an addition, so both paths appear). The output is Markdown
with five sections, each a sorted list of paths or `- none`:

- Seams this batch touches: changed files listed in contracts/seams.txt at BASE.
- Bloem files beside changed Silo code: the Bloem-owned companions of a
  changed .go file: for Silo's <dir>/<stem>.go (or <stem>_test.go), the files
  <dir>/bloem_<stem>.go and <dir>/<stem>_bloem[_<topic>].go, tests included.
  Every other Bloem file in a package is left to the compiler and the gate;
  listing whole packages buried the companions under hundreds of files.
- Overlays to bring in line: the docs/bloem/overlays/ edition of a changed Silo
  doc (docs/<p> or a root X.md), when BASE has one.
- Bloem pages that mention changed files: Bloem-owned Markdown that names a
  changed path, or a changed .go file's directory followed by "/", in its text
  or as a relative link target. A directory counts when it is named on its own
  or with an existing Silo file directly in it; a deeper path names another
  directory, and a Bloem file in it is Bloem's code. Frozen records are
  skipped: anything under docs/superpowers/ or .superpowers/ (historical
  working records) and any page whose name carries a date (receipts and merge
  records).
- Migrations in this batch: files the batch adds under migrations/sql/.

A file is Bloem-owned when it is in BASE's tree and in neither the merge
base's tree nor SILO's tree; the merge base is a Silo commit, so its files are
Silo's even when Silo deletes them in this batch.
"""

import argparse
import posixpath
import re
import subprocess
import sys
from pathlib import Path

KEYS = ("seams", "beside", "overlays", "pages", "migrations")
HEADINGS = {
    "seams": "## Seams this batch touches",
    "beside": "## Bloem files beside changed Silo code",
    "overlays": "## Overlays to bring in line",
    "pages": "## Bloem pages that mention changed files",
    "migrations": "## Migrations in this batch",
}

SEAMS_FILE = "contracts/seams.txt"
OVERLAYS = "docs/bloem/overlays/"
MIGRATIONS = "migrations/sql/"
FROZEN_PAGES = re.compile(r"^(docs/superpowers/|\.superpowers/)|\d{4}-\d{2}-\d{2}[^/]*$")
GO_STEM = re.compile(r"(_test)?\.go$")

# A mention starts where a path cannot continue leftward, or right after a
# "./" or "../" link prefix, so `web/README.md` is not a mention of README.md.
LEFT = r"(?:(?<![\w.\-/])|(?<=\./))"
# A file mention ends where the path cannot continue, so `Makefile.bloem` is
# not a mention of Makefile but "see Makefile." is.
FILE_END = r"(?![\w\-/]|\.\w)"
# What follows a directory mention: nothing, or one file name directly in it.
CHILD = r"([\w.\-]*)" + FILE_END
LINK = re.compile(r"\]\(\s*<?([^)\s>]+)")


class ReviewError(Exception):
    """The review cannot be computed from these commits."""


def git(repo, *args, stdin=None):
    proc = subprocess.run(["git", "-C", str(repo), *args], input=stdin, capture_output=True)
    if proc.returncode != 0:
        detail = proc.stderr.decode(errors="replace").strip()
        raise ReviewError(f"git {' '.join(args)}: {detail}")
    return proc.stdout


def commit(repo, ref):
    return git(repo, "rev-parse", "--verify", "--end-of-options", f"{ref}^{{commit}}").decode().strip()


def tree(repo, rev):
    """Map every path in rev's tree to its (type, object id)."""
    entries = {}
    for record in git(repo, "ls-tree", "-r", "-z", "--full-tree", rev).split(b"\0"):
        if not record:
            continue
        meta, path = record.split(b"\t", 1)
        _, kind, oid = meta.split(b" ")
        entries[path.decode()] = (kind.decode(), oid.decode())
    return entries


def batch_changes(repo, merge_base, silo):
    """Return {path: status letter} for the batch, with renames split in two."""
    out = git(repo, "diff", "--name-status", "-z", "--no-renames", "--no-relative",
              "--no-ext-diff", merge_base, silo).split(b"\0")
    fields = [field.decode() for field in out if field]
    return {fields[i + 1]: fields[i][0] for i in range(0, len(fields), 2)}


def blobs(repo, oids):
    """Return the text of each blob, read in one git cat-file call."""
    if not oids:
        return {}
    data = git(repo, "cat-file", "--batch", stdin="".join(f"{oid}\n" for oid in oids).encode())
    texts, pos = {}, 0
    for oid in oids:
        header_end = data.index(b"\n", pos)
        _, _, size = data[pos:header_end].split(b" ")
        start = header_end + 1
        texts[oid] = data[start:start + int(size)].decode(errors="replace")
        pos = start + int(size) + 1
    return texts


def seams(text):
    """The first token of each non-comment line of contracts/seams.txt."""
    listed = set()
    for line in text.split("\n"):
        line = line.strip()
        if line and not line.startswith("#"):
            listed.add(line.split()[0])
    return listed


def overlay_for(path):
    if path.startswith("docs/") and not path.startswith("docs/bloem/"):
        return OVERLAYS + path[len("docs/"):]
    if "/" not in path and path.endswith(".md"):
        return OVERLAYS + path
    return None


def go_stem(path):
    return GO_STEM.sub("", posixpath.basename(path))


def companions(owned, changed):
    """Bloem-owned bloem_<stem>.go and <stem>_bloem[_*].go beside changed Go files."""
    stems = {}
    for path in changed:
        if path.endswith(".go"):
            stems.setdefault(posixpath.dirname(path), set()).add(go_stem(path))
    found = set()
    for path in owned:
        here = posixpath.dirname(path)
        if not path.endswith(".go") or here not in stems:
            continue
        name = go_stem(path)
        for stem in stems[here]:
            if name in (f"bloem_{stem}", f"{stem}_bloem") or name.startswith(f"{stem}_bloem_"):
                found.add(path)
    return found


def alternation(paths):
    return "|".join(re.escape(p) for p in sorted(paths, key=len, reverse=True))


class Mentions:
    """Finds changed files and changed Go directories named in a page."""

    def __init__(self, files, dirs, silo_files):
        self.files, self.dirs, self.silo_files = files, dirs, silo_files
        self.file_re = re.compile(LEFT + f"(?:{alternation(files)}){FILE_END}") if files else None
        self.dir_re = re.compile(LEFT + f"({alternation(dirs)})" + CHILD) if dirs else None

    def names_dir(self, directory, child):
        """A directory counts named alone or with a Silo file directly in it.

        A subdirectory, a Bloem file, a glob such as bloem_* or a Go symbol
        after the directory does not count.
        """
        child = child.rstrip(".")
        return not child or directory + child in self.silo_files

    def in_text(self, text):
        if self.file_re and self.file_re.search(text):
            return True
        if self.dir_re:
            return any(self.names_dir(m.group(1), m.group(2)) for m in self.dir_re.finditer(text))
        return False

    def in_links(self, page, text):
        """Whether a relative link in page resolves to a changed file or directory."""
        here = posixpath.dirname(page)
        for target in LINK.findall(text):
            target = target.split("#", 1)[0].split("?", 1)[0]
            if not target or "://" in target or target.startswith(("/", "mailto:")):
                continue
            resolved = posixpath.normpath(posixpath.join(here, target))
            if resolved in self.files or resolved + "/" in self.dirs:
                return True
            parent = posixpath.dirname(resolved) + "/"
            if parent in self.dirs and self.names_dir(parent, posixpath.basename(resolved)):
                return True
        return False

    def __call__(self, page, text):
        return self.in_text(text) or self.in_links(page, text)


def review(repo: Path, base: str, silo: str) -> dict:
    base_rev, silo_rev = commit(repo, base), commit(repo, silo)
    merge_base = git(repo, "merge-base", base_rev, silo_rev).decode().strip()
    changes = batch_changes(repo, merge_base, silo_rev)
    changed = set(changes)

    base_tree = tree(repo, base_rev)
    silo_paths = set(tree(repo, silo_rev))
    merge_base_paths = set(tree(repo, merge_base))
    owned = {p for p in base_tree if p not in silo_paths and p not in merge_base_paths}

    if SEAMS_FILE not in base_tree:
        raise ReviewError(f"{SEAMS_FILE} is missing at {base}")
    seam_oid = base_tree[SEAMS_FILE][1]
    seam_paths = seams(blobs(repo, [seam_oid])[seam_oid])

    beside = companions(owned, changed)

    overlays = {o for o in map(overlay_for, changed) if o and o in base_tree}

    go_dirs = {posixpath.dirname(p) + "/" for p in changed if p.endswith(".go") and "/" in p}
    mentions = Mentions(changed, go_dirs, set(base_tree) - owned)
    candidates = sorted(
        p for p in owned
        if p.endswith(".md") and base_tree[p][0] == "blob" and not FROZEN_PAGES.search(p)
    )
    pages = set()
    if changed:
        texts = blobs(repo, sorted({base_tree[p][1] for p in candidates}))
        pages = {page for page in candidates if mentions(page, texts[base_tree[page][1]])}

    migrations = {p for p, status in changes.items() if status == "A" and p.startswith(MIGRATIONS)}

    return {
        "seams": sorted(changed & seam_paths),
        "beside": sorted(beside),
        "overlays": sorted(overlays),
        "pages": sorted(pages),
        "migrations": sorted(migrations),
    }


def render(result: dict) -> str:
    sections = []
    for key in KEYS:
        items = result.get(key) or []
        lines = [f"- `{path}`" for path in items] or ["- none"]
        sections.append("\n".join([HEADINGS[key], *lines]) + "\n")
    return "\n".join(sections)


def main(argv=None) -> int:
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--base", required=True, help="Bloem commit the batch merges into")
    parser.add_argument("--silo", required=True, help="Silo commit at the tip of the batch")
    parser.add_argument("--repo", default=".", help="repository (default: the cwd)")
    args = parser.parse_args(argv)
    try:
        result = review(Path(args.repo), args.base, args.silo)
    except ReviewError as err:
        print(f"silo-merge-review: {err}", file=sys.stderr)
        return 2
    sys.stdout.write(render(result))
    return 0


if __name__ == "__main__":
    sys.exit(main())
