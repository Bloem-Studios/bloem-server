#!/usr/bin/env python3
"""Run the tests in scripts/ci/bloem-retried-tests.txt with up to two retries.

    retried=$(python3 scripts/ci/bloem-retried-tests.py skip)
    GOFLAGS="$GOFLAGS -skip=$skip|$retried" make test-go GO_TEST_PACKAGES="$packages"
    python3 scripts/ci/bloem-retried-tests.py run $packages

    python3 scripts/ci/bloem-retried-tests.py db-pins

Commands assume the repository root is the cwd.

A few Silo tests fail only under the load of Bloem CI's Go test shards, or of
its Go database pins job, and pass alone. For each listed test the shards skip
its name in `make test-go`, and `run` then runs it on its own,
`go test -count=1 -run '^Name$'` in its package, when that package is in the
shard's list. A listed test that is also in a database list
(scripts/ci/db-pins.txt or scripts/ci/db-contracts.txt) belongs to the pins
job instead: `db-pins` runs `make test-db-pins` on copies of both lists
without it, then runs it on its own through the same runner
(`make test-db-contracts` with a one-test list), so a skip still fails it. A
failed run is retried twice; the step fails when all three runs fail. The list
names one test per line, never a pattern, and each entry carries its reason
and evidence.

`skip` prints the -skip pattern. `go test -skip` matches test names, not
packages, so a listed name that is also a test in another package is refused
(that test would then run nowhere), as is a listed test that no longer exists.
"""

import os
import re
import subprocess
import sys
import tempfile
from pathlib import Path

LIST = "scripts/ci/bloem-retried-tests.txt"
DB_LISTS = ("scripts/ci/db-pins.txt", "scripts/ci/db-contracts.txt")
ATTEMPTS = 3  # one run and two retries
TEST_FUNC = re.compile(r"^func (Test[A-Za-z0-9_]*)\(t \*testing\.T\)", re.M)
TEST_NAME = re.compile(r"^Test[A-Za-z0-9_]*$")
SKIP_DIRS = {".git", "node_modules", "web", "vendor", ".superpowers"}


class ListError(Exception):
    pass


def listed_tests(root):
    """(package dir, test name) for every entry, in list order."""
    entries = []
    for line in (root / LIST).read_text().splitlines():
        fields = line.split("#", 1)[0].split()
        if not fields:
            continue
        if len(fields) != 2 or not TEST_NAME.match(fields[1]):
            raise ListError(f"{LIST}: expected '<package dir> <TestName>' with one test name, got {line!r}")
        entries.append((fields[0].rstrip("/"), fields[1]))
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


def checked_entries(root):
    root = Path(root)
    entries = listed_tests(root)
    if not entries:
        raise ListError(f"{LIST} names no tests")
    defined = defined_tests(root)
    problems = []
    for package, name in entries:
        if package not in defined.get(name, set()):
            problems.append(f"{name} is listed for {package} but no longer defined there")
        elsewhere = sorted(defined.get(name, set()) - {package})
        if elsewhere:
            problems.append(f"{name} is also a test in {', '.join(elsewhere)}, which the list does not name")
    if problems:
        raise ListError("; ".join(problems))
    return entries


def db_listed(root):
    """(package dir, test name) for every test in the database lists."""
    found = set()
    for name in DB_LISTS:
        for line in (Path(root) / name).read_text().splitlines():
            fields = line.split("#", 1)[0].split()
            if len(fields) == 2:
                found.add((fields[0].rstrip("/"), fields[1]))
    return found


def skip_pattern(root):
    return "^(" + "|".join(sorted({name for _, name in checked_entries(root)})) + ")$"


def module_path(root):
    for line in (Path(root) / "go.mod").read_text().splitlines():
        if line.startswith("module "):
            return line.split()[1]
    raise ListError("go.mod names no module")


def go_test(argv):
    env = dict(os.environ)
    # The shard's -skip would skip the very test this runs.
    env["GOFLAGS"] = " ".join(f for f in env.get("GOFLAGS", "").split() if not f.startswith("-skip="))
    return subprocess.run(argv, env=env).returncode


def run_command(argv):
    return subprocess.run(argv).returncode


def retry(package, name, argv, runner):
    """Run argv up to ATTEMPTS times until it passes; True when one run passed."""
    for attempt in range(1, ATTEMPTS + 1):
        print(f"bloem-retried-tests: {name} in {package}, run {attempt} of {ATTEMPTS}", flush=True)
        if runner(argv) == 0:
            break
    else:
        print(f"bloem-retried-tests: {name} in {package} failed all {ATTEMPTS} runs", flush=True)
        return False
    if attempt > 1:
        # A GitHub annotation, so a flake that passed on a retry shows on the run's summary.
        prefix = "::warning::" if os.environ.get("GITHUB_ACTIONS") == "true" else ""
        print(f"{prefix}bloem-retried-tests: {name} in {package} passed on run {attempt}", flush=True)
    return True


def run_retried(root, packages, runner=go_test):
    """Run each listed test whose package is in packages; True when all pass.

    Tests in a database list are left to `db-pins`: the shards skip those anyway.
    """
    module = module_path(root)
    shard = set(packages)
    in_db_lists = db_listed(root)
    ok = True
    for package, name in checked_entries(root):
        import_path = module + package[1:]
        if import_path not in shard or (package, name) in in_db_lists:
            continue
        argv = ["go", "test", "-timeout=20m", "-count=1", "-run", f"^{name}$", import_path]
        ok = retry(package, name, argv, runner) and ok
    return ok


def without(text, drop):
    """The list text without the lines that name an entry in drop; comments stay."""
    kept = []
    for line in text.splitlines(keepends=True):
        fields = line.split("#", 1)[0].split()
        if len(fields) == 2 and (fields[0].rstrip("/"), fields[1]) in drop:
            continue
        kept.append(line)
    return "".join(kept)


def run_db_pins(root, workdir, runner=run_command):
    """The Go database pins job: every pin, with the listed ones retried; True when all pass.

    The full run uses copies of the database lists without the retried tests,
    and each retried test then runs alone through the same runner and make
    variables, so a missing or skipped test still fails.
    """
    root, workdir = Path(root), Path(workdir)
    in_db_lists = db_listed(root)
    retried = [entry for entry in checked_entries(root) if entry in in_db_lists]
    drop = set(retried)
    copies = []
    for name in DB_LISTS:
        copy = workdir / Path(name).name
        copy.write_text(without((root / name).read_text(), drop))
        copies.append(copy)
    ok = runner(["make", "test-db-pins", f"DB_PINS={copies[0]}", f"DB_CONTRACTS={copies[1]}"]) == 0
    for package, name in retried:
        one = workdir / f"retried-{name}.txt"
        one.write_text(f"{package} {name}\n")
        ok = retry(package, name, ["make", "test-db-contracts", f"DB_CONTRACTS={one}"], runner) and ok
    return ok


def main(argv):
    try:
        if argv[1:] == ["skip"]:
            print(skip_pattern(Path.cwd()))
            return 0
        if len(argv) >= 2 and argv[1] == "run":
            return 0 if run_retried(Path.cwd(), argv[2:]) else 1
        if argv[1:] == ["db-pins"]:
            with tempfile.TemporaryDirectory(prefix="bloem-db-pins-") as workdir:
                return 0 if run_db_pins(Path.cwd(), workdir) else 1
    except ListError as err:
        sys.exit(f"bloem-retried-tests: {err}")
    sys.exit("usage: bloem-retried-tests.py skip | run PACKAGE... | db-pins")


if __name__ == "__main__":
    sys.exit(main(sys.argv))
