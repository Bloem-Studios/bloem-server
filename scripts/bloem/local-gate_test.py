#!/usr/bin/env python3
"""Tests for scripts/bloem/local-gate.py.

Run: python3 scripts/bloem/local-gate_test.py

The drift test reads .github/workflows/bloem-ci.yml and fails when Bloem CI
runs a make target or a go test the local gate does not.
"""

import importlib.util
import re
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
WORKFLOW = ROOT / ".github" / "workflows" / "bloem-ci.yml"

# Commands Bloem CI runs that the local gate deliberately does not, with the
# reason. Keep this empty unless a CI command is pure runner plumbing.
CI_ONLY = {}


def load_gate():
    path = Path(__file__).with_name("local-gate.py")
    spec = importlib.util.spec_from_file_location("local_gate", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def run_blocks(text):
    """Return the body of every `run:` step, folded and block scalars alike."""
    lines = text.split("\n")
    blocks = []
    i = 0
    while i < len(lines):
        match = re.match(r"^(\s*)(?:-\s+)?run:\s*(.*)$", lines[i])
        if not match:
            i += 1
            continue
        indent, rest = len(match.group(1)), match.group(2).strip()
        if rest in ("|", ">", "|-", ">-"):
            body = []
            i += 1
            while i < len(lines) and (
                not lines[i].strip() or len(lines[i]) - len(lines[i].lstrip()) > indent
            ):
                body.append(lines[i])
                i += 1
            blocks.append("\n".join(body))
            continue
        blocks.append(rest)
        i += 1
    return blocks


def commands(texts):
    """Make targets and go test invocations, normalized for comparison.

    A go test is keyed by its packages, its -run pattern and -race. A sharded
    CI pattern ("$tests") counts as the whole package, which is what the gate
    runs.
    """
    found = set()
    for text in texts:
        text = re.sub(r"\\\n\s*", " ", text)
        for target in re.findall(r"(?<![\w-])make\s+([a-z][a-z0-9-]*)", text):
            found.add(("make", target))
        for line in text.split("\n"):
            if "go test" not in line:
                continue
            line = line[line.index("go test"):]
            tokens = re.findall(r"'[^']*'|\"[^\"]*\"|\S+", line)
            packages = tuple(sorted(t for t in tokens if t.startswith("./")))
            pattern = None
            if "-run" in tokens:
                pattern = tokens[tokens.index("-run") + 1].strip("'\"")
                if pattern.startswith("$"):
                    pattern = None
            found.add(("go test", packages, pattern, "-race" in tokens))
    return found


class LocalGateTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.gate = load_gate()

    def test_phases_in_order(self):
        seen = list(dict.fromkeys(step.phase for step in self.gate.STEPS))
        self.assertEqual(
            seen, ["build", "verify", "lint", "web", "go", "db", "tenancy"]
        )

    def test_every_ci_make_target_and_go_test_is_in_the_gate(self):
        ci = commands(run_blocks(WORKFLOW.read_text()))
        gate = commands(step.cmd for step in self.gate.STEPS)
        missing = sorted(ci - gate - set(CI_ONLY), key=repr)
        self.assertEqual(
            missing,
            [],
            "Bloem CI runs these but the local gate does not; add them to "
            "scripts/bloem/local-gate.py (CI_ONLY lists deliberate exceptions)",
        )

    def test_postgres_flags_match_ci(self):
        argv = " ".join(self.gate.postgres_command("x", 5432))
        self.assertIn("pgvector/pgvector:pg18", argv)
        self.assertIn("max_connections=300", argv)
        self.assertIn("max_locks_per_transaction=512", argv)

    def test_go_suite_is_dealt_to_four_shards_each_on_a_fresh_database(self):
        shards = [s for s in self.gate.STEPS if s.phase == "go"]
        self.assertEqual(len(shards), 4)
        fresh = {n for s in shards for n in s.needs if n.startswith("fresh_db:")}
        self.assertEqual(len(fresh), 4)

    def test_go_shards_leave_database_list_tests_to_the_pins_job(self):
        # Silo's database-list tests run in the db phase on a fresh database;
        # run beside every other package they see each other's writes.
        # The pattern is computed on its own, so a broken list fails the step
        # instead of leaving -skip empty.
        for step in (s for s in self.gate.STEPS if s.phase == "go"):
            self.assertTrue(step.cmd.startswith("skip=$(python3 scripts/ci/bloem-db-list-skip.py) && "), step.cmd)
            self.assertIn('-skip=$skip"', step.cmd)
        block = next(b for b in run_blocks(WORKFLOW.read_text()) if "make test-go" in b)
        self.assertIn("\nskip=$(python3 scripts/ci/bloem-db-list-skip.py)\n", "\n" + "\n".join(
            line.strip() for line in block.split("\n")) + "\n")
        test_go = next(line for line in block.split("\n") if "make test-go" in line)
        self.assertIn('GOFLAGS="$GOFLAGS -skip=$skip" make test-go', test_go)

    def test_redis_steps_declare_their_disposable_server(self):
        # The browse Redis tests flush their server and accept only CI's fixed
        # address or one the harness declares; the gate's Redis has a free port.
        redis_steps = [s for s in self.gate.STEPS if "@REDIS@" in s.cmd]
        self.assertTrue(redis_steps)
        for step in redis_steps:
            self.assertIn("SILO_TEST_REDIS_DISPOSABLE_ADDR=@REDIS_ADDR@", step.cmd)
        exports = self.gate.services_exports("123-abcd", 40001, 40002)
        self.assertIn("export SILO_TEST_REDIS_DISPOSABLE_ADDR=127.0.0.1:40002", exports)

    def test_gofmt_is_the_go_mod_toolchains(self):
        # The gofmt on PATH belongs to the machine's Go, which can format
        # differently from the toolchain go.mod names (and CI uses).
        step = next(s for s in self.gate.STEPS if s.name == "gofmt")
        self.assertIn('"$(go env GOROOT)/bin/gofmt" -l .', step.cmd)

    def test_step_names_are_unique(self):
        names = [f"{s.phase}/{s.name}" for s in self.gate.STEPS]
        self.assertEqual(len(names), len(set(names)))

    def test_services_exports_are_shell_assignments(self):
        text = self.gate.services_exports("123-abcd", 40001, 40002)
        for line in text.strip().split("\n"):
            self.assertRegex(line, r"^export [A-Z_]+=\S+$")
        self.assertIn("export GATE_SERVICES_ID=123-abcd", text)
        self.assertIn("127.0.0.1:40001/bloem_test?", text)
        self.assertIn("export SILO_TEST_REDIS_URL=redis://127.0.0.1:40002/0", text)

    def test_list_lines_are_phase_slash_step_tab_command(self):
        for line in self.gate.format_list(self.gate.STEPS).strip().split("\n"):
            self.assertRegex(line, r"^[a-z]+/[a-z0-9-]+\t\S")


if __name__ == "__main__":
    unittest.main()
