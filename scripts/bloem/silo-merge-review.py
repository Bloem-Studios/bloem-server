#!/usr/bin/env python3
"""List what a Silo batch touches that Bloem has to look at while merging it.

    python3 scripts/bloem/silo-merge-review.py --base origin/main --silo upstream/main

Commands assume the repository root is the cwd. The batch is every file that
changed between the merge base of BASE and SILO and SILO itself (renames count
as a deletion plus an addition, so both paths appear). The output is Markdown
with five sections, each a sorted list of paths, some followed by " — " and a
note, or `- none`:

- Seams this batch touches: changed files listed in contracts/seams.txt at BASE.
- Bloem files beside changed Silo code, one line per file:
  - Companions: for a changed <dir>/<stem>[.<ext>] (the stem ends at the first
    dot and drops a _test suffix), the Bloem files in <dir> named
    bloem_<stem>, <stem>_bloem or <stem>.bloem, alone or followed by "_" or
    "." and anything: bloem_auth_ext.go, AdminLayout.bloem.test.tsx,
    Makefile.bloem.
  - Mirrors ("mirrors `path`"): a Bloem file elsewhere whose last two path
    components equal a changed file's, as Bloem's scenario catalogs extend
    Silo's. Overlays have their own section.
  - Callers ("uses `name`"): a Bloem Go file in the directory of a changed Go
    file whose code, comments and literals aside, names a func, method, type,
    const or var whose declaration or body the batch changed, added or
    removed. Each changed line counts for the top-level declaration holding it
    in the old or the new file. Go's visibility rules narrow the match: a name
    from Silo's test files counts only in test files, a method only after a
    ".", a package-level name after a "." only in a test file (pkg.Name from
    an external test package), and a method of a type Silo declares in a test
    file (a test fake) only where the file names that type. Any other method
    counts by name, since an interface call never names the type behind it.
- Overlays to bring in line: the docs/bloem/overlays/ edition of a changed Silo
  doc (docs/<p> or a root X.md), when BASE has one.
- Bloem pages that mention changed files: Bloem-owned Markdown that names a
  changed path, or a changed .go file's directory followed by "/" or ending
  where a path cannot continue (`internal/scanner`), in its text or as a
  relative link target. A directory counts when it is named on its own or with
  an existing Silo file directly in it; a deeper path names another directory,
  and a Bloem file in it is Bloem's code. Frozen records are skipped: anything
  under docs/superpowers/ or .superpowers/ (historical working records) and
  any page whose name carries a date (receipts and merge records).
- Migrations in this batch: files the batch adds under migrations/sql/ as
  plain paths, and the ones it modifies, deletes or renames with a note:
  goose does not re-run an applied migration, and keys one by its version
  prefix.

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
TEST_SUFFIX = re.compile(r"_test$")
MIGRATION_NOTES = {
    "M": "modified: goose does not re-run an applied migration",
    "T": "type changed: goose does not re-run an applied migration",
    "D": "deleted",
}

# A mention starts where a path cannot continue leftward, or right after a
# "./" or "../" link prefix, so `web/README.md` is not a mention of README.md.
LEFT = r"(?:(?<![\w.\-/])|(?<=\./))"
# A file mention ends where the path cannot continue, so `Makefile.bloem` is
# not a mention of Makefile but "see Makefile." is.
FILE_END = r"(?![\w\-/]|\.\w)"
# What follows a directory mention and its "/": nothing, or one file name
# directly in it. Without the "/", the name must end like a file mention.
CHILD = r"([\w.\-]*)" + FILE_END
LINK = re.compile(r"\]\(\s*<?([^)\s>]+)")

# Go top-level declarations as gofmt lays them out: a declaration starts at
# column 0, a grouped entry one tab in, and a line holding only closing
# brackets at column 0 ends it (`) error {` after a long signature does not).
GO_DECL = re.compile(r"(?:func\s*(\([^)]*\))?\s*|type\s+|var\s+|const\s+)(\w+(?:\s*,\s*\w+)*)")
GO_GROUP = re.compile(r"(?:type|var|const)\s*\(\s*(?://.*)?$")
GO_RECEIVER = re.compile(r"\(\s*(?:\w+\s+)?\*?\s*(\w+)")
GO_ENTRY = re.compile(r"\t(\w+(?:\s*,\s*\w+)*)(?![\w.(])")
GO_CLOSER = re.compile(r"[)}]+\s*(?://.*)?$")
# Comments and string and rune literals, which hold words but no references.
GO_NOISE = re.compile(r"//[^\n]*|/\*.*?\*/|\"(?:[^\"\\\n]|\\.)*\"|`[^`]*`|'(?:[^'\\\n]|\\.)*'", re.S)
HUNK = re.compile(rb"@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@")
# Names no other file can refer to.
UNREFERABLE = {"_", "init", "main"}


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


def migration_changes(repo, merge_base, silo):
    """Map each migration the batch touches to its notes: none when it is added.

    Renames are detected here, unlike for the rest of the batch: goose keys a
    migration by its version prefix, so a renamed file can run again.
    """
    out = git(repo, "diff", "--name-status", "-z", "-M", "--no-relative", "--no-ext-diff",
              merge_base, silo, "--", f":(top){MIGRATIONS}").split(b"\0")
    fields = [field.decode() for field in out if field]
    notes, i = {}, 0
    while i < len(fields):
        status = fields[i]
        if status[0] in "RC":
            old, path = fields[i + 1], fields[i + 2]
            verb = "renamed" if status[0] == "R" else "copied"
            note = f"{verb} from `{old}`" + ("" if status[1:] == "100" else " and modified")
            i += 3
        else:
            path = fields[i + 1]
            note = None if status == "A" else MIGRATION_NOTES.get(status[0], f"status {status}")
            i += 2
        notes[path] = [note] if note else []
    return notes


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


def stem(path):
    """A file name up to its first extension, without a _test suffix.

    Leading dots belong to the stem, so .env.example has the stem .env.
    """
    name = posixpath.basename(path)
    lead = len(name) - len(name.lstrip("."))
    return TEST_SUFFIX.sub("", name[:lead] + name[lead:].split(".", 1)[0])


def companions(owned, changed):
    """Bloem-owned companions of changed files, in the same directory.

    For Silo's <stem>[.<ext>], they are bloem_<stem>, <stem>_bloem and
    <stem>.bloem, each alone or followed by "_" or "." and anything.
    """
    stems = {}
    for path in changed:
        if stem(path):
            stems.setdefault(posixpath.dirname(path), set()).add(stem(path))
    patterns = {}
    for here, names in stems.items():
        alts = alternation(names)
        patterns[here] = re.compile(rf"(?:bloem_(?:{alts})|(?:{alts})[_.]bloem)(?:[_.].*)?")
    return {
        path for path in owned
        if posixpath.dirname(path) in patterns
        and patterns[posixpath.dirname(path)].fullmatch(posixpath.basename(path))
    }


def tail(path):
    """A path's last two components."""
    return "/".join(path.rsplit("/", 2)[-2:])


def mirrors(owned, changed, exclude):
    """Map each Bloem file ending in a changed file's last two components to those files."""
    tails = {}
    for path in changed:
        if "/" in path:
            tails.setdefault(tail(path), set()).add(path)
    return {
        path: tails[tail(path)] for path in owned
        if "/" in path and path not in exclude and tail(path) in tails
    }


def go_hunks(repo, merge_base, silo):
    """Map each Go file the batch changes to its (old, new) changed line numbers."""
    out = git(repo, "-c", "core.quotePath=false", "diff", "-U0", "--inter-hunk-context=0",
              "--no-renames", "--no-relative", "--no-ext-diff", "--no-textconv", "--no-color",
              "--src-prefix=a/", "--dst-prefix=b/", merge_base, silo, "--", ":(top)*.go")
    hunks, paths, header = {}, {}, False
    for line in out.split(b"\n"):
        if line.startswith(b"diff --git "):
            paths, header = {}, True
        elif header and line[:4] in (b"--- ", b"+++ "):
            name = line[4:].decode(errors="replace")
            if name != "/dev/null":
                paths[line[:1]] = name[2:]
        elif match := HUNK.match(line):
            header = False
            path = paths.get(b"+") or paths.get(b"-")
            if path:
                old, new = hunks.setdefault(path, (set(), set()))
                a, b, c, d = (int(g) if g else 1 for g in match.groups())
                old.update(range(a, a + b))
                new.update(range(c, c + d))
    return hunks


def declared(names, receiver=None):
    """(name, receiver type or None) pairs for a declaration's names."""
    if receiver:
        typed = GO_RECEIVER.match(receiver)
        receiver = typed.group(1) if typed else receiver
    return tuple((name.strip(), receiver) for name in names.split(","))


def go_declarations(text, raw_strings):
    """The (name, receiver) pairs the top-level declaration of each line declares.

    With raw_strings, lines inside a backquoted string are never read as
    declarations. Callers take the union of both readings, so a stray
    backquote cannot hide a declaration and a `)` in embedded SQL cannot end one.
    """
    names, current, group, raw = [], (), False, False
    for line in text.split("\n"):
        was_raw = raw
        if raw_strings and (raw or not line.lstrip().startswith("//")) and line.count("`") % 2:
            raw = not raw
        if was_raw:
            names.append(current)
        elif line[:1] in ("", " ", "\t"):
            if group and (entry := GO_ENTRY.match(line)):
                current = declared(entry.group(1))
            names.append(current)
        elif GO_CLOSER.match(line):
            names.append(current)
            current, group = (), False
        else:
            if GO_GROUP.match(line):
                current, group = (), True
            elif decl := GO_DECL.match(line):
                current, group = declared(decl.group(2), decl.group(1)), False
            elif line.startswith(("//", "/*", "import", "package")):
                current = ()
            names.append(current)
    return names


def changed_names(text, lines):
    """The declarations the given 1-based line numbers of Go source belong to."""
    per_line = list(zip(go_declarations(text, True), go_declarations(text, False)))
    found = set()
    for number in lines:
        if 0 < number <= len(per_line):
            found.update(*per_line[number - 1])
    return found


def reference_pattern(declarations, test):
    """A pattern for references to declarations, as Go's visibility rules allow them.

    declarations holds (name, receiver, declared_in_test) triples. Only a test
    file sees names from Silo's test files; a method is reached through a
    selector; a package-level name is written after a dot only from an
    external test package (pkg.Name).
    """
    visible = {(name, receiver) for name, receiver, in_test in declarations if test or not in_test}
    methods = {name for name, receiver in visible if receiver}
    package = {name for name, receiver in visible if not receiver}
    parts = []
    if methods:
        parts.append(rf"\.\s*(?P<method>{alternation(methods)})(?!\w)")
    if package:
        left = r"(?<!\w)" if test else r"(?<![\w.])"
        parts.append(rf"{left}(?P<package>{alternation(package)})(?!\w)")
    return re.compile("|".join(parts)) if parts else None


def callers(go_files, changed):
    """Map each Bloem Go file to the changed names it uses in its own directory.

    go_files maps a Bloem-owned Go file to its text; changed maps a directory
    to the (name, receiver, declared_in_test) triples Silo's batch declared,
    changed or removed there. A method of a type Silo declares in a test file
    (a test fake) counts only where the file's code names that type; any other
    method counts by name, as an interface call never names the type behind it.
    """
    patterns, found = {}, {}
    for path, text in go_files.items():
        here, test = posixpath.dirname(path), path.endswith("_test.go")
        if (here, test) not in patterns:
            patterns[here, test] = reference_pattern(changed.get(here, ()), test)
        if not patterns[here, test]:
            continue
        code = GO_NOISE.sub(" ", text)
        names = set()
        for match in patterns[here, test].finditer(code):
            name = match.group(match.lastgroup)
            if match.lastgroup == "package" or any(
                    n == name and receiver and (not in_test or (test and receiver in code))
                    for n, receiver, in_test in changed[here]):
                names.add(name)
        if names:
            found[path] = names
    return found


def annotated(notes):
    """Sorted items: a path alone, or (path, note) when it carries notes."""
    return [
        (path, "; ".join(notes[path])) if notes[path] else path
        for path in sorted(notes)
    ]


def alternation(paths):
    return "|".join(re.escape(p) for p in sorted(paths, key=len, reverse=True))


class Mentions:
    """Finds changed files and changed Go directories named in a page."""

    def __init__(self, files, dirs, silo_files):
        self.files, self.dirs, self.silo_files = files, dirs, silo_files
        self.file_re = re.compile(LEFT + f"(?:{alternation(files)}){FILE_END}") if files else None
        bare = alternation(d.rstrip("/") for d in dirs)
        self.dir_re = re.compile(LEFT + f"({bare})(?:/{CHILD}|{FILE_END})") if dirs else None

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
            return any(self.names_dir(m.group(1) + "/", m.group(2) or "")
                       for m in self.dir_re.finditer(text))
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
    silo_tree = tree(repo, silo_rev)
    merge_base_tree = tree(repo, merge_base)
    owned = {p for p in base_tree if p not in silo_tree and p not in merge_base_tree}

    if SEAMS_FILE not in base_tree:
        raise ReviewError(f"{SEAMS_FILE} is missing at {base}")
    seam_oid = base_tree[SEAMS_FILE][1]
    seam_paths = seams(blobs(repo, [seam_oid])[seam_oid])

    overlays = {o for o in map(overlay_for, changed) if o and o in base_tree}

    hunks = go_hunks(repo, merge_base, silo_rev)
    hunk_dirs = {posixpath.dirname(path) for path in hunks}
    go_files = {
        p for p in owned
        if p.endswith(".go") and base_tree[p][0] == "blob" and posixpath.dirname(p) in hunk_dirs
    }
    candidates = sorted(
        p for p in owned
        if p.endswith(".md") and base_tree[p][0] == "blob" and not FROZEN_PAGES.search(p)
    ) if changed else []
    sides = [(path, side_tree, lines) for path, (old, new) in hunks.items()
             for side_tree, lines in ((merge_base_tree, old), (silo_tree, new))
             if lines and side_tree.get(path, ("",))[0] == "blob"]
    texts = blobs(repo, sorted({base_tree[p][1] for p in [*go_files, *candidates]}
                               | {side_tree[path][1] for path, side_tree, _ in sides}))

    names = {}
    for path, side_tree, lines in sides:
        in_test = path.endswith("_test.go")
        names.setdefault(posixpath.dirname(path), set()).update(
            (name, receiver, in_test)
            for name, receiver in changed_names(texts[side_tree[path][1]], lines)
            if name not in UNREFERABLE)

    beside = {path: [] for path in companions(owned, changed)}
    for path, silo_paths in mirrors(owned, changed, overlays).items():
        beside.setdefault(path, []).append(
            "mirrors " + ", ".join(f"`{p}`" for p in sorted(silo_paths)))
    used = callers({p: texts[base_tree[p][1]] for p in go_files}, names)
    for path, found in used.items():
        beside.setdefault(path, []).append("uses " + ", ".join(f"`{n}`" for n in sorted(found)))

    go_dirs = {posixpath.dirname(p) + "/" for p in changed if p.endswith(".go") and "/" in p}
    mentions = Mentions(changed, go_dirs, set(base_tree) - owned)
    pages = {page for page in candidates if mentions(page, texts[base_tree[page][1]])}

    migrations = migration_changes(repo, merge_base, silo_rev)

    return {
        "seams": sorted(changed & seam_paths),
        "beside": annotated(beside),
        "overlays": sorted(overlays),
        "pages": sorted(pages),
        "migrations": annotated(migrations),
    }


def render(result: dict) -> str:
    sections = []
    for key in KEYS:
        items = result.get(key) or []
        lines = [
            f"- `{item[0]}` — {item[1]}" if isinstance(item, tuple) else f"- `{item}`"
            for item in items
        ] or ["- none"]
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
