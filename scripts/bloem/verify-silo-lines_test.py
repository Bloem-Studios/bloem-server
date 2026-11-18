#!/usr/bin/env python3
"""Regression cases for scripts/bloem/verify-silo-lines.py."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("guard", pathlib.Path(__file__).with_name("verify-silo-lines.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)

DIFF = """diff --git a/internal/x.go b/internal/x.go
index 1..2 100644
--- a/internal/x.go
+++ b/internal/x.go
@@ -10 +10 @@ func f() {
-\tif err := preflightPlaybackFile(ctx, file); err != nil {
+\tif err := h.nativeVideoPreflight(r, file); err != nil {
@@ -20,0 +21 @@ func g() {
+\tbloemHook(ctx)
diff --git a/internal/only_added.go b/internal/only_added.go
--- a/internal/only_added.go
+++ b/internal/only_added.go
@@ -5,0 +6,2 @@
+\tif bloemSkip() {
+\t\treturn
"""


class ParseTest(unittest.TestCase):
    def test_counts_only_removed_silo_lines(self):
        removed = guard.parse_removed(DIFF)
        self.assertEqual(removed["internal/x.go"], ["\tif err := preflightPlaybackFile(ctx, file); err != nil {"])
        self.assertNotIn("internal/only_added.go", removed)


class CompareTest(unittest.TestCase):
    def lines(self, *values):
        return {"kind": "lines", "lines": sorted(values)}

    def test_current_record_passes(self):
        state = {"a.go": self.lines("silo one", "silo two")}
        self.assertEqual(guard.compare(dict(state), state), [])

    def test_new_override_fails(self):
        problems = guard.compare({}, {"a.go": self.lines("silo one")})
        self.assertEqual(len(problems), 1)
        self.assertIn("now overrides", problems[0][1])

    def test_silo_changing_an_overridden_line_fails(self):
        # Bloem kept its line through a merge that changed Silo's: the
        # overridden text is now Silo's new line.
        problems = guard.compare({"a.go": self.lines("silo old")}, {"a.go": self.lines("silo new")})
        self.assertEqual(problems[0][2], ["silo new"])

    def test_restored_lines_ask_to_shrink_the_record(self):
        problems = guard.compare({"a.go": self.lines("one", "two")}, {"a.go": self.lines("one")})
        self.assertIn("shrink the record", problems[0][1])

    def test_file_back_to_silo_asks_to_drop_the_entry(self):
        problems = guard.compare({"a.go": self.lines("one")}, {})
        self.assertIn("no longer overrides", problems[0][1])

    def test_generated_files_skip_line_comparison(self):
        self.assertEqual(guard.compare({"gen.json": {"kind": "generated"}}, {"gen.json": self.lines("anything")}), [])

    def test_silo_change_to_a_deleted_file_fails(self):
        old = {"kind": "deleted", "silo_blob": "aaa"}
        new = {"kind": "deleted", "silo_blob": "bbb"}
        self.assertIn("Silo changed", guard.compare({"f.png": old}, {"f.png": new})[0][1])


if __name__ == "__main__":
    unittest.main()
