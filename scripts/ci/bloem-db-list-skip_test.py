#!/usr/bin/env python3
"""Tests for scripts/ci/bloem-db-list-skip.py.

Run: python3 scripts/ci/bloem-db-list-skip_test.py
"""

import importlib.util
import re
import tempfile
import unittest
from pathlib import Path

ROOT = Path(__file__).resolve().parents[2]


def load():
    path = Path(__file__).with_name("bloem-db-list-skip.py")
    spec = importlib.util.spec_from_file_location("db_list_skip", path)
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


class DBListSkipTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.mod = load()

    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.root = Path(self.tmp.name)
        write(self.root, "scripts/ci/db-pins.txt", "# pins\n\n./internal/a TestPinned\n")
        write(self.root, "scripts/ci/db-contracts.txt", "./internal/b TestContract\n")
        write(self.root, "internal/a/a_test.go", go_test("TestPinned", "TestPinnedLater"))
        write(self.root, "internal/b/b_test.go", go_test("TestContract"))

    def tearDown(self):
        self.tmp.cleanup()

    def test_pattern_names_every_listed_test_and_nothing_else(self):
        pattern = self.mod.build_pattern(self.root)
        self.assertEqual(pattern, "^(TestContract|TestPinned)$")
        self.assertTrue(re.search(pattern, "TestPinned"))
        self.assertFalse(re.search(pattern, "TestPinnedLater"))

    def test_a_listed_name_defined_in_an_unlisted_package_fails(self):
        # go test -skip matches names, not packages: the unlisted test would
        # then run nowhere.
        write(self.root, "internal/c/c_test.go", go_test("TestPinned"))
        with self.assertRaisesRegex(self.mod.ListError, r"TestPinned.*\./internal/c"):
            self.mod.build_pattern(self.root)

    def test_a_listed_test_that_does_not_exist_fails(self):
        write(self.root, "scripts/ci/db-contracts.txt", "./internal/b TestGone\n")
        with self.assertRaisesRegex(self.mod.ListError, r"TestGone"):
            self.mod.build_pattern(self.root)

    def test_repository_lists_build_a_pattern(self):
        pattern = self.mod.build_pattern(ROOT)
        self.assertTrue(re.search(pattern, "TestPopulationSyncResumesAfterQuotaAcrossRuns"))


if __name__ == "__main__":
    unittest.main()
