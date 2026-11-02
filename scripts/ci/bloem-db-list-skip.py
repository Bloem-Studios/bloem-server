#!/usr/bin/env python3
"""Print a go test -skip pattern naming every test in Silo's database lists.

    GOFLAGS="$GOFLAGS -skip=$(python3 scripts/ci/bloem-db-list-skip.py)" make test-go ...

Commands assume the repository root is the cwd.

Silo's own `make test-go` has no database, so its DB-backed tests skip there.
Silo runs the ones it relies on through scripts/ci/dbpins, from
scripts/ci/db-pins.txt and scripts/ci/db-contracts.txt, on a fresh database
(`make test-db-pins`). Some of them assume nothing else writes to that
database while they run: the marker population sync, for one, walks every
eligible file in it.

Bloem's Go test shards give `make test-go` one shared database, so every
DB-backed package runs at once. Bloem CI's Go database pins job already runs
both lists the way Silo does, so the shards leave those tests to it rather than
run them a second time beside every other package's writes.

`go test -skip` matches test names, not packages. The pattern is refused when a
listed name is also a test in a package the lists do not name (that test would
then run nowhere), or when a listed test no longer exists.
"""

import re
import sys
from pathlib import Path

LISTS = ("scripts/ci/db-pins.txt", "scripts/ci/db-contracts.txt")
TEST_FUNC = re.compile(r"^func (Test[A-Za-z0-9_]*)\(t \*testing\.T\)", re.M)
SKIP_DIRS = {".git", "node_modules", "web", "vendor", ".superpowers"}


class ListError(Exception):
    pass


def listed_tests(root):
    """(package dir, test name) for every entry in the database lists."""
    entries = set()
    for name in LISTS:
        for line in (root / name).read_text().splitlines():
            fields = line.split("#", 1)[0].split()
            if not fields:
                continue
            if len(fields) != 2:
                raise ListError(f"{name}: expected '<package dir> <TestName>', got {line!r}")
            entries.add((fields[0].rstrip("/"), fields[1]))
    return entries


def defined_tests(root):
    """Map each top-level test name to the package dirs that define it."""
    found = {}
    stack = [root]
    while stack:
        directory = stack.pop()
        for path in directory.iterdir():
            if path.is_dir():
                if path.name not in SKIP_DIRS:
                    stack.append(path)
            elif path.name.endswith("_test.go"):
                package = "./" + path.parent.relative_to(root).as_posix()
                for name in TEST_FUNC.findall(path.read_text(errors="replace")):
                    found.setdefault(name, set()).add(package)
    return found


def build_pattern(root):
    root = Path(root)
    entries = listed_tests(root)
    defined = defined_tests(root)
    listed_dirs = {}
    for package, name in entries:
        listed_dirs.setdefault(name, set()).add(package)
    problems = []
    for package, name in sorted(entries):
        if package not in defined.get(name, set()):
            problems.append(f"{name} is listed for {package} but no longer defined there")
    for name, packages in sorted(listed_dirs.items()):
        elsewhere = sorted(defined.get(name, set()) - packages)
        if elsewhere:
            problems.append(f"{name} is also a test in {', '.join(elsewhere)}, which the lists do not name")
    if problems:
        raise ListError("; ".join(problems))
    if not listed_dirs:
        raise ListError("the database lists name no tests")
    return "^(" + "|".join(sorted(listed_dirs)) + ")$"


def main():
    try:
        print(build_pattern(Path.cwd()))
    except ListError as err:
        sys.exit(f"bloem-db-list-skip: {err}")


if __name__ == "__main__":
    main()
