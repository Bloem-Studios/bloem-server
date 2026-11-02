#!/usr/bin/env python3
"""Tests for scripts/bloem/silo-merge-review.py.

Run: python3 scripts/bloem/silo-merge-review_test.py

Each test builds its own temporary git repository: a Silo commit (the merge
base), a "bloem" branch that adds Bloem's files on top of it, and a "silo"
branch carrying the batch.
"""

import importlib.util
import os
import subprocess
import tempfile
import unittest
from pathlib import Path

SEAMS = "# Silo-owned files Bloem may edit.\nMakefile  # Bloem gates\ninternal/y/b.go\n"


def load_review():
    path = Path(__file__).with_name("silo-merge-review.py")
    spec = importlib.util.spec_from_file_location("silo_merge_review", path)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def git(repo, *args):
    env = dict(os.environ, GIT_CONFIG_GLOBAL=os.devnull, GIT_CONFIG_NOSYSTEM="1")
    subprocess.run(
        ["git", "-C", str(repo), "-c", "user.name=t", "-c", "user.email=t@example.com", *args],
        check=True,
        capture_output=True,
        env=env,
    )


def apply(repo, files, message):
    """Write (or, for a None value, delete) files and commit them."""
    for path, text in files.items():
        target = repo / path
        if text is None:
            git(repo, "rm", "-q", path)
            continue
        target.parent.mkdir(parents=True, exist_ok=True)
        target.write_text(text)
        git(repo, "add", path)
    git(repo, "commit", "-q", "--allow-empty", "-m", message)


def make_repo(test, merge_base, bloem, silo, seams=SEAMS):
    """Return a repository whose "bloem" and "silo" branches fork at merge_base."""
    tmp = tempfile.TemporaryDirectory()
    test.addCleanup(tmp.cleanup)
    repo = Path(tmp.name)
    git(repo, "init", "-q", "-b", "main")
    apply(repo, merge_base, "silo: merge base")
    git(repo, "branch", "silo")
    git(repo, "checkout", "-q", "-b", "bloem")
    if seams is not None:
        bloem = {"contracts/seams.txt": seams, **bloem}
    apply(repo, bloem, "bloem: own files")
    git(repo, "checkout", "-q", "silo")
    apply(repo, silo, "silo: batch")
    git(repo, "checkout", "-q", "bloem")
    return repo


class ReviewTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.mod = load_review()

    def review(self, merge_base, bloem, silo):
        return self.mod.review(make_repo(self, merge_base, bloem, silo), "bloem", "silo")

    def test_seam_touched_by_batch_is_listed(self):
        r = self.review(
            {"Makefile": "all:\n", "internal/y/b.go": "package y\n"},
            {"Makefile": "all:\n\tbloem\n"},
            {"Makefile": "all:\n\tsilo\n"},
        )
        self.assertEqual(r["seams"], ["Makefile"])

    def test_bloem_file_beside_changed_go_file(self):
        r = self.review(
            {"internal/x/a.go": "package x\n", "internal/y/b.go": "package y\n"},
            {"internal/x/bloem_a.go": "package x\n", "internal/y/bloem_b.go": "package y\n",
             "internal/x/sub/bloem_c.go": "package sub\n"},
            {"internal/x/a.go": "package x // changed\n"},
        )
        self.assertEqual(r["beside"], ["internal/x/bloem_a.go"])

    def test_changed_silo_doc_maps_to_overlay(self):
        r = self.review(
            {"docs/architecture/api-contract.md": "a\n", "DEVELOPMENT.md": "a\n",
             "docs/admin-api.md": "a\n"},
            {"docs/bloem/overlays/architecture/api-contract.md": "o\n",
             "docs/bloem/overlays/DEVELOPMENT.md": "o\n"},
            {"docs/architecture/api-contract.md": "b\n", "DEVELOPMENT.md": "b\n",
             "docs/admin-api.md": "b\n"},
        )
        self.assertEqual(r["overlays"], ["docs/bloem/overlays/DEVELOPMENT.md",
                                         "docs/bloem/overlays/architecture/api-contract.md"])

    def test_bloem_page_mentioning_changed_path(self):
        r = self.review(
            {"internal/x/a.go": "package x\n"},
            {"docs/bloem/x.md": "The hook lives in `internal/x/`.\n",
             "docs/bloem/y.md": "Nothing relevant.\n",
             "internal/x/bloem_notes.go": "package x // internal/x/\n"},
            {"internal/x/a.go": "package x // changed\n"},
        )
        self.assertEqual(r["pages"], ["docs/bloem/x.md"])

    def test_new_migration_listed(self):
        r = self.review(
            {"migrations/sql/20260101000000_init.sql": "-- init\n"},
            {"migrations/sql/20261001000000_bloem.sql": "-- bloem\n"},
            {"migrations/sql/20261102000000_add_thing.sql": "-- new\n",
             "migrations/sql/20260101000000_init.sql": "-- init, edited\n"},
        )
        self.assertEqual(r["migrations"], ["migrations/sql/20261102000000_add_thing.sql"])

    def test_empty_sections_print_none(self):
        empty = {key: [] for key in ("seams", "beside", "overlays", "pages", "migrations")}
        self.assertIn("## Migrations in this batch\n- none", self.mod.render(empty))

    def test_render_prints_the_five_headings_in_order(self):
        text = self.mod.render({"seams": ["Makefile"], "beside": [], "overlays": [],
                                "pages": ["docs/bloem/x.md", "FORK.md"], "migrations": []})
        headings = [line for line in text.split("\n") if line.startswith("#")]
        self.assertEqual(headings, [
            "## Seams this batch touches",
            "## Bloem files beside changed Silo code",
            "## Overlays to bring in line",
            "## Bloem pages that mention changed files",
            "## Migrations in this batch",
        ])
        self.assertIn("## Seams this batch touches\n- `Makefile`\n", text)
        self.assertIn("- `docs/bloem/x.md`\n- `FORK.md`\n", text)

    def test_file_silo_deletes_is_not_bloem_owned(self):
        # The merge base is a Silo commit: a file Silo deletes in this batch is
        # in base and not in silo, but it is still Silo's file.
        r = self.review(
            {"internal/x/a.go": "package x\n", "internal/x/old.go": "package x\n",
             "docs/old.md": "mentions `internal/x/`\n"},
            {"internal/x/bloem_a.go": "package x\n"},
            {"internal/x/a.go": "package x // changed\n", "internal/x/old.go": None,
             "docs/old.md": None},
        )
        self.assertEqual(r["beside"], ["internal/x/bloem_a.go"])
        self.assertEqual(r["pages"], [])

    def test_rename_lists_old_and_new_paths(self):
        body = "package z\n\nfunc Long() int { return 1 }\n" * 20
        r = self.review(
            {"internal/z/old_name.go": body},
            {"internal/z/bloem_old_name.go": "package z\n",
             "internal/w/bloem_new_name_test.go": "package w\n",
             "docs/bloem/z.md": "See [old](../../internal/z/old_name.go).\n"},
            {"internal/z/old_name.go": None, "internal/w/new_name.go": body},
        )
        self.assertEqual(r["beside"], ["internal/w/bloem_new_name_test.go",
                                       "internal/z/bloem_old_name.go"])
        self.assertEqual(r["pages"], ["docs/bloem/z.md"])

    def test_pages_skip_frozen_records(self):
        # Dated receipts and records, and the historical working records under
        # docs/superpowers/ and .superpowers/, are never edited by a batch.
        mention = "Touches `internal/x/a.go`.\n"
        r = self.review(
            {"internal/x/a.go": "package x\n"},
            {"docs/operations/2026-11-01-silo-sync-deployment.md": mention,
             "docs/superpowers/plans/2026-11-02-plan.md": mention,
             "docs/superpowers/notes/log.md": mention,
             ".superpowers/sdd/2026-08-13-x/task-2-report.md": mention,
             "docs/architecture/2026-09-08-upstream-reconciliation.md": mention,
             "docs/bloem/upstream-merge-2026-10-10.md": mention,
             "docs/operations/bulk-policy-runbook.md": mention},
            {"internal/x/a.go": "package x // changed\n"},
        )
        self.assertEqual(r["pages"], ["docs/operations/bulk-policy-runbook.md"])

    def test_page_matching_respects_path_boundaries(self):
        r = self.review(
            {"Makefile": "all:\n", "README.md": "r\n", "internal/x/a.go": "package x\n",
             "internal/x/b.go": "package x\n", "internal/y/b.go": "package y\n"},
            {"docs/bloem/longer.md": "Bloem targets live in `Makefile.bloem`.\n",
             "docs/bloem/nested.md": "See `web/README.md` and `cmd/internal/x/`.\n",
             "docs/bloem/plain.md": "Run `make` (see Makefile).\n",
             "docs/bloem/linked.md": "See [b](../../internal/x/b.go#L3).\n",
             "docs/bloem/file.md": "Code under internal/x/a.go.\n"},
            {"Makefile": "all:\n\tsilo\n", "README.md": "r2\n",
             "internal/x/a.go": "package x // changed\n"},
        )
        # linked.md names a file Silo did not change, in a directory it did.
        self.assertEqual(r["pages"], ["docs/bloem/file.md", "docs/bloem/linked.md",
                                      "docs/bloem/plain.md"])

    def test_directory_mention_is_the_directory_or_a_silo_file_in_it(self):
        # A deeper path names another directory, and a Bloem file in the
        # directory is Bloem's code, not Silo's.
        r = self.review(
            {"internal/x/a.go": "package x\n", "internal/x/b.go": "package x\n",
             "internal/x/sub/c.go": "package sub\n"},
            {"internal/x/bloem_a.go": "package x\n",
             "docs/bloem/bare.md": "Package `internal/x/` does X.\n",
             "docs/bloem/glob.md": "Files `internal/x/*.go`.\n",
             "docs/bloem/silo_child.md": "See `internal/x/b.go`.\n",
             "docs/bloem/silo_child_link.md": "See [b](../../internal/x/b.go).\n",
             "docs/bloem/deeper.md": "See `internal/x/sub/c.go`.\n",
             "docs/bloem/deeper_link.md": "See [c](../../internal/x/sub/c.go).\n",
             "docs/bloem/bloem_child.md": "Bloem adds internal/x/bloem_a.go.\n",
             "docs/bloem/bloem_link.md": "See [ours](../../internal/x/bloem_a.go#L1).\n",
             "docs/bloem/subdir.md": "Helpers live in `internal/x/sub` today.\n",
             "docs/bloem/bloem_glob.md": "Bloem's `internal/x/bloem_*` files.\n",
             "docs/bloem/symbol.md": "The field internal/x/b.Thing.Name is set.\n"},
            {"internal/x/a.go": "package x // changed\n"},
        )
        self.assertEqual(r["pages"], ["docs/bloem/bare.md", "docs/bloem/glob.md",
                                      "docs/bloem/silo_child.md",
                                      "docs/bloem/silo_child_link.md"])

    def test_beside_lists_companions_not_the_whole_package(self):
        # Bloem names the file holding its additions to Silo's <stem>.go
        # bloem_<stem>.go or <stem>_bloem[_<topic>].go.
        r = self.review(
            {"internal/api/router.go": "package api\n",
             "internal/api/handlers/items_test.go": "package handlers\n",
             "internal/api/handlers/admin.go": "package handlers\n"},
            {"internal/api/router_bloem.go": "package api\n",
             "internal/api/router_bloem_livetv_test.go": "package api\n",
             "internal/api/bloem_router.go": "package api\n",
             "internal/api/bloem_audiobook_router.go": "package api\n",
             "internal/api/router_compression_test.go": "package api\n",
             "internal/api/handlers/bloem_items.go": "package handlers\n",
             "internal/api/handlers/bloem_items_test.go": "package handlers\n",
             "internal/api/handlers/bloem_admin_people.go": "package handlers\n",
             "internal/api/handlers/admin_tenants.go": "package handlers\n"},
            {"internal/api/router.go": "package api // changed\n",
             "internal/api/handlers/items_test.go": "package handlers // changed\n"},
        )
        self.assertEqual(r["beside"], [
            "internal/api/bloem_router.go",
            "internal/api/handlers/bloem_items.go",
            "internal/api/handlers/bloem_items_test.go",
            "internal/api/router_bloem.go",
            "internal/api/router_bloem_livetv_test.go",
        ])

    def test_page_linking_changed_silo_doc_by_relative_path(self):
        r = self.review(
            {"docs/architecture/api-contract.md": "a\n", "docs/admin-api.md": "a\n"},
            {"docs/bloem/x.md": "See [the contract](../architecture/api-contract.md#rules).\n",
             "docs/bloem/y.md": "See [admin](../../admin-api.md).\n"},
            {"docs/architecture/api-contract.md": "b\n", "docs/admin-api.md": "b\n"},
        )
        self.assertEqual(r["pages"], ["docs/bloem/x.md"])

    def test_missing_seams_file_is_an_error(self):
        repo = make_repo(self, {"Makefile": "all:\n"}, {}, {"Makefile": "all:\n\tsilo\n"},
                         seams=None)
        with self.assertRaises(self.mod.ReviewError):
            self.mod.review(repo, "bloem", "silo")

    def test_nothing_new_from_silo_lists_none(self):
        repo = make_repo(self, {"Makefile": "all:\n"}, {}, {})
        git(repo, "merge", "-q", "--no-edit", "silo")
        r = self.mod.review(repo, "bloem", "silo")
        self.assertEqual(r, {key: [] for key in ("seams", "beside", "overlays", "pages",
                                                 "migrations")})


if __name__ == "__main__":
    unittest.main()
