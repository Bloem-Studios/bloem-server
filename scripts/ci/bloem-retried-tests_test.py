#!/usr/bin/env python3
"""Tests for scripts/ci/bloem-retried-tests.py.

Run: python3 scripts/ci/bloem-retried-tests_test.py
"""

import importlib.util
import json
import re
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]
MODULE = "github.com/Example/server"


def load():
    path = Path(__file__).with_name("bloem-retried-tests.py")
    spec = importlib.util.spec_from_file_location("retried_tests", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def write(root, path, text):
    target = root / path
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text(text)


def go_test(*names):
    return "package x\n\nimport \"testing\"\n\n" + "".join(
        f"func {name}(t *testing.T) {{}}\n" for name in names
    )


class FakeRunner:
    """Answers each go test run with the next exit code."""

    def __init__(self, codes):
        self.codes = list(codes)
        self.calls = []

    def __call__(self, argv):
        self.calls.append(argv)
        return self.codes.pop(0)


class RetriedTestsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.mod = load()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        write(self.root, "go.mod", f"module {MODULE}\n\ngo 1.26\n")
        write(self.root, "scripts/ci/bloem-retried-tests.txt", "# why\n\n./internal/a TestSlow\n")
        write(self.root, "internal/a/a_test.go", go_test("TestSlow", "TestSlowLater"))
        write(self.root, "internal/b/b_test.go", go_test("TestOther"))
        write(self.root, "scripts/ci/db-pins.txt", "# pins\n./internal/b TestOther\n")
        write(self.root, "scripts/ci/db-contracts.txt", "# contracts\n./internal/b TestContract\n")

    def tearDown(self):
        self.tmp.cleanup()

    def test_pattern_names_only_the_listed_test(self):
        pattern = self.mod.skip_pattern(self.root)
        self.assertEqual(pattern, "^(TestSlow)$")
        self.assertTrue(re.search(pattern, "TestSlow"))
        self.assertFalse(re.search(pattern, "TestSlowLater"))

    def test_a_listed_name_also_defined_elsewhere_fails(self):
        # -skip matches names, not packages: the other test would run nowhere.
        write(self.root, "internal/b/b_test.go", go_test("TestOther", "TestSlow"))
        with self.assertRaisesRegex(self.mod.ListError, "TestSlow is also a test in ./internal/b"):
            self.mod.skip_pattern(self.root)

    def test_a_listed_test_that_no_longer_exists_fails(self):
        write(self.root, "internal/a/a_test.go", go_test("TestSlowLater"))
        with self.assertRaisesRegex(self.mod.ListError, "no longer defined"):
            self.mod.skip_pattern(self.root)

    def test_an_entry_that_is_a_pattern_fails(self):
        write(self.root, "scripts/ci/bloem-retried-tests.txt", "./internal/a TestSlow.*\n")
        with self.assertRaisesRegex(self.mod.ListError, "one test name"):
            self.mod.skip_pattern(self.root)

    def test_a_pass_on_the_first_run_needs_no_retry(self):
        runner = FakeRunner([0])
        self.assertTrue(self.mod.run_retried(self.root, [f"{MODULE}/internal/a"], runner))
        self.assertEqual(len(runner.calls), 1)
        argv = runner.calls[0]
        self.assertEqual(argv[:2], ["go", "test"])
        self.assertIn("-count=1", argv)
        self.assertEqual(argv[argv.index("-run") + 1], "^TestSlow$")
        self.assertEqual(argv[-1], f"{MODULE}/internal/a")

    def test_a_failure_is_retried_and_a_later_pass_passes(self):
        runner = FakeRunner([1, 1, 0])
        self.assertTrue(self.mod.run_retried(self.root, [f"{MODULE}/internal/a"], runner))
        self.assertEqual(len(runner.calls), 3)

    def test_three_failures_fail_the_run(self):
        runner = FakeRunner([1, 1, 1, 0])
        self.assertFalse(self.mod.run_retried(self.root, [f"{MODULE}/internal/a"], runner))
        self.assertEqual(len(runner.calls), 3)

    def test_a_shard_without_the_package_runs_nothing(self):
        runner = FakeRunner([])
        self.assertTrue(self.mod.run_retried(self.root, [f"{MODULE}/internal/b"], runner))
        self.assertEqual(runner.calls, [])

    def test_a_shard_leaves_a_database_list_test_to_the_pins_job(self):
        write(self.root, "scripts/ci/bloem-retried-tests.txt", "./internal/b TestOther\n")
        runner = FakeRunner([])
        self.assertTrue(self.mod.run_retried(self.root, [f"{MODULE}/internal/b"], runner))
        self.assertEqual(runner.calls, [])

    def db_pins(self, codes):
        write(self.root, "scripts/ci/bloem-retried-tests.txt", "# why\n./internal/b TestOther\n./internal/a TestSlow\n")
        runner = FakeRunner(codes)
        work = self.root / "work"
        work.mkdir()
        return self.mod.run_db_pins(self.root, work, runner), runner, work

    def test_db_pins_runs_the_lists_without_the_retried_test_then_the_test_alone(self):
        ok, runner, work = self.db_pins([0, 0])
        self.assertTrue(ok)
        self.assertEqual(len(runner.calls), 2)
        full, alone = runner.calls
        self.assertEqual(full, ["make", "test-db-pins", f"DB_PINS={work / 'db-pins.txt'}",
                                f"DB_CONTRACTS={work / 'db-contracts.txt'}"])
        self.assertEqual((work / "db-pins.txt").read_text(), "# pins\n")
        self.assertEqual((work / "db-contracts.txt").read_text(), "# contracts\n./internal/b TestContract\n")
        # The same runner as the pins, so a skip still fails; the shard's TestSlow is not its business.
        self.assertEqual(alone, ["make", "test-db-contracts", f"DB_CONTRACTS={work / 'retried-TestOther.txt'}"])
        self.assertEqual((work / "retried-TestOther.txt").read_text(), "./internal/b TestOther\n")

    def test_db_pins_retries_a_failed_test_and_a_later_pass_passes(self):
        ok, runner, _ = self.db_pins([0, 1, 1, 0])
        self.assertTrue(ok)
        self.assertEqual(len(runner.calls), 4)

    def test_db_pins_fails_when_the_test_fails_every_run(self):
        ok, runner, _ = self.db_pins([0, 1, 1, 1])
        self.assertFalse(ok)
        self.assertEqual(len(runner.calls), 4)

    def test_db_pins_fails_when_the_other_pins_fail_and_still_runs_the_retried_test(self):
        ok, runner, _ = self.db_pins([2, 0])
        self.assertFalse(ok)
        self.assertEqual(len(runner.calls), 2)

    def test_the_real_list_is_valid(self):
        self.assertTrue(self.mod.skip_pattern(ROOT).startswith("^("))

    def test_the_real_database_entries_are_in_a_database_list(self):
        listed = self.mod.db_listed(ROOT)
        for entry in [("./internal/chapterthumbs", "TestTerminatedChapterWorkerCannotReplaceCurrentBlobDB"),
                      ("./internal/chapterthumbs", "TestChapterImageReuseWaitsForCollectorDB")]:
            self.assertIn(entry, listed)
            self.assertIn(entry, self.mod.checked_entries(ROOT))


WEB_FILE = "src/a/Flaky.test.tsx"
FLAKY = ("Flaky host", "shows the pick (under load)")
OTHER = ("Flaky host", "replaces the pick")


def vitest_report(root, *tests, file_status=None, message=""):
    """A Vitest JSON report: tests are (file, titles, status)."""
    files = {}
    for file, titles, status in tests:
        files.setdefault(file, []).append({"ancestorTitles": list(titles[:-1]), "title": titles[-1],
                                           "fullName": " ".join(titles), "status": status})
    results = []
    for file, assertions in files.items():
        failed = any(a["status"] == "failed" for a in assertions)
        results.append({"name": str(root / "web" / file), "assertionResults": assertions,
                        "status": file_status or ("failed" if failed else "passed"), "message": message})
    return {"success": False, "testResults": results}


class WebRunner:
    """Answers the shard run and each run alone with scripted (exit code, report)."""

    def __init__(self, shard, alone=()):
        self.shard, self.alone = shard, list(alone)
        self.shard_calls, self.alone_calls = [], []

    def run_shard(self, argv):
        self.shard_calls.append(argv)
        code, report = self.shard
        self._write(argv[-1], report)
        return code

    def run_alone(self, argv):
        self.alone_calls.append(argv)
        code, report = self.alone.pop(0)
        self._write(argv[-1], report)
        return code

    @staticmethod
    def _write(arg, report):
        path = next(part.split("=", 1)[1] for part in arg.split() if part.startswith("--outputFile.json="))
        if report is not None:
            Path(path).write_text(json.dumps(report))


class RetriedWebTestsTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.mod = load()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        write(self.root, "web/" + WEB_FILE,
              'describe("Flaky host", () => {\n  it("shows the pick (under load)", () => {});\n'
              '  it("replaces the pick", () => {});\n});\n')
        write(self.root, "scripts/ci/bloem-retried-web-tests.txt",
              "# why\n\n" + " > ".join((WEB_FILE, *FLAKY)) + "\n")
        self.work = self.root / "work"
        self.work.mkdir()

    def tearDown(self):
        self.tmp.cleanup()

    def run_web(self, runner):
        return self.mod.run_web(self.root, ["--shard=2/4", "--maxWorkers=4"], self.work,
                                shard_runner=runner.run_shard, test_runner=runner.run_alone)

    def report(self, *tests, **kw):
        return vitest_report(self.root, *tests, **kw)

    def test_a_passing_shard_runs_once_with_a_json_report(self):
        runner = WebRunner((0, None))
        self.assertTrue(self.run_web(runner))
        (argv,) = runner.shard_calls
        self.assertEqual(argv[:2], ["make", "test-web"])
        self.assertTrue(argv[2].startswith("WEBTEST_ARGS=--shard=2/4 --maxWorkers=4 --reporter=default "
                                           "--reporter=json --outputFile.json="))
        self.assertEqual(runner.alone_calls, [])

    def test_a_listed_failure_runs_alone_and_passes(self):
        passed = self.report((WEB_FILE, FLAKY, "passed"), (WEB_FILE, OTHER, "skipped"))
        runner = WebRunner((1, self.report((WEB_FILE, FLAKY, "failed"), (WEB_FILE, OTHER, "passed"))),
                           [(0, passed)])
        self.assertTrue(self.run_web(runner))
        (argv,) = runner.alone_calls
        self.assertEqual(argv[:5], ["pnpm", "exec", "vitest", "run", WEB_FILE])
        self.assertEqual(argv[argv.index("-t") + 1], r"^Flaky host shows the pick \(under load\)$")

    def test_a_listed_failure_gets_two_more_runs_at_most(self):
        failed = self.report((WEB_FILE, FLAKY, "failed"))
        runner = WebRunner((1, failed), [(1, failed), (1, failed), (0, None)])
        self.assertFalse(self.run_web(runner))
        self.assertEqual(len(runner.alone_calls), 2)

    def test_a_run_alone_that_selected_nothing_does_not_pass(self):
        runner = WebRunner((1, self.report((WEB_FILE, FLAKY, "failed"))),
                           [(0, self.report((WEB_FILE, FLAKY, "skipped"))), (0, {"testResults": []})])
        self.assertFalse(self.run_web(runner))

    def test_an_unlisted_failure_fails_the_shard_with_no_retry(self):
        runner = WebRunner((1, self.report((WEB_FILE, FLAKY, "failed"), (WEB_FILE, OTHER, "failed"))))
        self.assertFalse(self.run_web(runner))
        self.assertEqual(runner.alone_calls, [])

    def test_a_file_that_failed_outside_its_tests_is_not_retried(self):
        runner = WebRunner((1, self.report((WEB_FILE, FLAKY, "failed"), file_status="failed",
                                           message="")))
        broken = self.report((WEB_FILE, OTHER, "skipped"), file_status="failed", message="SyntaxError")
        broken["testResults"][0]["assertionResults"] = []
        runner.shard = (1, broken)
        self.assertFalse(self.run_web(runner))
        self.assertEqual(runner.alone_calls, [])

    def test_a_failed_shard_without_a_report_is_not_retried(self):
        runner = WebRunner((1, None))
        self.assertFalse(self.run_web(runner))
        self.assertEqual(runner.alone_calls, [])

    def test_a_failed_shard_with_no_failed_test_is_not_retried(self):
        runner = WebRunner((1, self.report((WEB_FILE, FLAKY, "passed"))))
        self.assertFalse(self.run_web(runner))

    def test_web_list_entries_must_still_name_their_test(self):
        write(self.root, "scripts/ci/bloem-retried-web-tests.txt", f"{WEB_FILE} > Flaky host > gone\n")
        with self.assertRaisesRegex(self.mod.ListError, "no longer names 'gone'"):
            self.mod.web_entries(self.root)
        write(self.root, "scripts/ci/bloem-retried-web-tests.txt", "src/a/Missing.test.tsx > x > y\n")
        with self.assertRaisesRegex(self.mod.ListError, "does not exist"):
            self.mod.web_entries(self.root)
        write(self.root, "scripts/ci/bloem-retried-web-tests.txt", "Flaky host > shows the pick\n")
        with self.assertRaisesRegex(self.mod.ListError, "expected"):
            self.mod.web_entries(self.root)

    def test_an_empty_web_list_retries_nothing(self):
        write(self.root, "scripts/ci/bloem-retried-web-tests.txt", "# comments only\n")
        self.assertEqual(self.mod.web_entries(self.root), [])

    def test_the_real_web_list_is_valid(self):
        self.assertIsInstance(self.mod.web_entries(ROOT), list)


if __name__ == "__main__":
    unittest.main()
