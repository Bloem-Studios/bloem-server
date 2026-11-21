#!/usr/bin/env python3
"""Tests for the Silo client compatibility check's comparison rules.

Run: python3 scripts/bloem/silo-client-compat/compat_test.py
"""

import json
import sys
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parent
sys.path.insert(0, str(HERE))

import compare  # noqa: E402
import compat  # noqa: E402
import extract_calls  # noqa: E402
import notices  # noqa: E402


def resp(status=200, body=None, headers=None, kind=None):
    h = {"content-type": "application/json"} if body is not None else {}
    h.update(headers or {})
    return {"status": status, "headers": h, "json": body, "kind": kind or ("json" if body is not None else "empty")}


def kinds(diffs, failing=True):
    return sorted((d.kind, d.field) for d in diffs if d.failing == failing)


class CompareTest(unittest.TestCase):
    def test_values_ids_and_timestamps_are_never_compared(self):
        silo = resp(body={"id": "1", "created_at": "2026-01-01T00:00:00Z", "items": [{"id": "a"}]})
        bloem = resp(body={"id": "77", "created_at": "2027-02-02T00:00:00Z", "items": [{"id": "b"}, {"id": "c"}]})
        self.assertEqual(compare.compare("GET", "/api/v2/x", silo, bloem), [])

    def test_additive_bloem_fields_are_allowed(self):
        silo = resp(body={"name": "x"})
        bloem = resp(body={"name": "x", "organization_id": "o1", "extra": {"a": 1}})
        self.assertEqual(compare.compare("GET", "/api/v2/x", silo, bloem), [])

    def test_missing_field_retyped_field_and_emptied_list_fail(self):
        silo = resp(body={"name": "x", "count": 2, "items": [{"id": "a", "title": "t"}], "nested": {"k": True}})
        bloem = resp(body={"count": "2", "items": [], "nested": {}})
        self.assertEqual(kinds(compare.compare("GET", "/api/v2/x", silo, bloem)), [
            ("field_missing", "name"), ("field_missing", "nested.k"), ("field_type", "count"), ("list_empty", "items[]"),
        ])

    def test_null_where_silo_sets_a_value_fails_only_when_a_client_requires_it(self):
        silo = resp(body={"title": "x", "overview": "y"})
        bloem = resp(body={"title": None, "overview": None})
        decoded = {"title": {"required_by": ["silo-apple"]}, "overview": {"required_by": []}}
        diffs = compare.compare("GET", "/api/v2/x", silo, bloem, decoded)
        self.assertEqual(kinds(diffs), [("field_null", "title")])
        self.assertEqual(kinds(diffs, failing=False), [("field_null", "overview")])

    def test_status_problem_type_and_headers(self):
        silo = resp(404, {"type": "https://siloserver.org/problems/not_found", "status": 404})
        bloem = resp(403, {"type": "https://siloserver.org/problems/permission_denied", "status": 403})
        self.assertEqual([d.kind for d in compare.compare("GET", "/api/v2/x", silo, bloem)], ["status", "problem_type"])
        silo = resp(200, {"a": 1}, {"etag": '"1"'})
        bloem = resp(200, {"a": 1})
        self.assertEqual(kinds(compare.compare("GET", "/api/v2/x", silo, bloem)), [("header_missing", "etag")])

    def test_id_keyed_objects_are_one_wildcard(self):
        silo = resp(body={"progress": {"101": {"pos": 1}, "102": {"pos": 2}}})
        bloem = resp(body={"progress": {"5": {"pos": 3}}})
        self.assertEqual(compare.compare("GET", "/api/v2/x", silo, bloem), [])

    def test_value_rules_catch_answers_clients_refuse(self):
        silo = resp(body={"api_major": 2})
        bloem = resp(body={"api_major": 3})
        self.assertEqual(kinds(compare.compare("GET", "/api/v2/system/info", silo, bloem)), [("value", "api_major")])

    def test_a_one_sided_rate_limit_is_a_note(self):
        silo = resp(404, {"type": "https://siloserver.org/problems/not_found"})
        bloem = resp(429, {"type": "https://siloserver.org/problems/rate_limited"}, {"retry-after": "5"})
        diffs = compare.compare("POST", "/api/v2/x", silo, bloem)
        self.assertEqual(kinds(diffs), [])
        self.assertEqual(kinds(diffs, failing=False), [("rate_limited", "")])

    def test_non_json_bodies_compare_by_kind(self):
        silo = resp(200, None, {"content-type": "image/jpeg"}, kind="binary")
        bloem = resp(200, None, {"content-type": "image/jpeg"}, kind="binary")
        self.assertEqual(compare.compare("GET", "/api/v2/x", silo, bloem), [])


class SampledListTest(unittest.TestCase):
    """The admin app log is a window onto each server's own newest lines."""

    LOGS = ("GET", "/api/v2/admin/logs/app")

    @staticmethod
    def line(component, message, attrs, **extra):
        return {"id": "1", "timestamp": "2026-11-21T15:00:34.242Z", "level": "info", "component": component,
                "message": message, "client_ip": "127.0.0.1/32", "node_id": "n", "attrs": attrs, **extra}

    def test_a_window_of_other_lines_is_a_note(self):
        # The 2026-11-21 merge bot run: Silo's newest five lines were apiv2
        # request lines, Bloem's drain had not flushed its own yet and its
        # newest were the v1 tombstone's request lines.
        silo = resp(body={"items": [self.line("apiv2", "apiv2 request", {
            "api_major": 2, "auth_class": "session", "operation_id": "listAdminAccessGroups", "status_class": "2xx"},
            request_id="r1", user_id="1")]})
        bloem = resp(body={"items": [self.line("api", "api request", {"path": "/api/v1/watch/history", "status": 410},
                                               request_id="r2")]})
        diffs = compare.compare(*self.LOGS, silo, bloem)
        self.assertEqual(kinds(diffs), [])
        self.assertEqual(kinds(diffs, failing=False), [("field_missing", "items[].user_id")])

    def test_the_log_item_schema_is_still_compared(self):
        silo = resp(body={"items": [self.line("apiv2", "apiv2 request", {"api_major": 2}, user_id="1")]})
        missing = self.line("api", "api request", {})
        del missing["message"]
        retyped = self.line("api", "api request", {}, user_id=1)
        retyped["level"] = 3
        self.assertEqual(kinds(compare.compare(*self.LOGS, silo, resp(body={"items": [missing]}))),
                         [("field_missing", "items[].message")])
        self.assertEqual(kinds(compare.compare(*self.LOGS, silo, resp(body={"items": [retyped]}))),
                         [("field_type", "items[].level"), ("field_type", "items[].user_id")])
        self.assertEqual(kinds(compare.compare(*self.LOGS, silo, resp(body={"items": []}))),
                         [("list_empty", "items[]")])
        # Anywhere else the same answers are divergences.
        self.assertIn(("field_missing", "items[].user_id"),
                      kinds(compare.compare("GET", "/api/v2/admin/logs/audit", silo,
                                            resp(body={"items": [self.line("api", "api request", {})]}))))

    def test_required_fields_are_silos_schema(self):
        spec = json.loads((HERE.parents[2] / "contracts/api/v2/openapi.json").read_text())
        schema = spec["components"]["schemas"]["AdminOperationalLog"]
        rule = compare.SAMPLED_LISTS[self.LOGS]
        self.assertEqual(sorted(rule["required"]), sorted(schema["required"]))
        self.assertTrue(set(rule["free_form"]) <= set(schema["properties"]))
        self.assertGreater(len(rule["reason"]), 10)


class AllowlistTest(unittest.TestCase):
    def test_entries_match_by_op_kind_persona_and_field_glob(self):
        allow = compare.Allowlist([{"op": "GET /api/v2/x", "kind": "field_missing", "field": "a.*", "reason": "r" * 12}])
        d = compare.Diff("field_missing", "a.b", "", True)
        self.assertIsNotNone(allow.match("GET /api/v2/x", "user", d))
        self.assertIsNone(allow.match("GET /api/v2/y", "user", d))
        self.assertEqual(allow.unused(), [])

    def test_allowlist_may_only_shrink_and_needs_reasons(self):
        old = [{"op": "GET /api/v2/x", "kind": "status", "reason": "a good reason"}]
        self.assertEqual(compare.validate_allowlist([], old), [])
        self.assertEqual(compare.validate_allowlist(old, old), [])
        grown = old + [{"op": "GET /api/v2/y", "kind": "status", "reason": "another reason"}]
        self.assertTrue(any("may only shrink" in p for p in compare.validate_allowlist(grown, old)))
        swapped = [{"op": "GET /api/v2/y", "kind": "status", "reason": "another reason"}]
        self.assertTrue(any("may only shrink" in p for p in compare.validate_allowlist(swapped, old)))
        self.assertTrue(compare.validate_allowlist([{"op": "GET /api/v2/x", "kind": "status"}], None))

    def test_checked_in_allowlist_is_valid(self):
        entries = json.loads((HERE / "allowlist.json").read_text())
        self.assertEqual(compare.validate_allowlist(entries, None), [])


class ReplayPlanTest(unittest.TestCase):
    CTX = {"movie": "m1", "series": "s1", "movies_library": "L1", "admin_profile": "pa", "user_profile": "pu",
           "pin_profile": "pk", "user_account": "u2", "movie_file": "f1"}

    def test_path_ids_come_from_the_seeded_data_by_route_and_name(self):
        r = compat.resolve_path
        self.assertEqual(r("/api/v2/catalog/items/{id}", self.CTX, "user"), "/api/v2/catalog/items/m1")
        self.assertEqual(r("/api/v2/profiles/{id}/verify-pin", self.CTX, "pin"), "/api/v2/profiles/pk/verify-pin")
        self.assertEqual(r("/api/v2/catalog/series/{id}/seasons/{num}", self.CTX, "admin"),
                         "/api/v2/catalog/series/s1/seasons/1")
        self.assertEqual(r("/api/v2/subtitles/{media_file_id}", self.CTX, "admin"), "/api/v2/subtitles/f1")
        # A route id the seed has no value for is the same invented id on both servers.
        self.assertEqual(r("/api/v2/collections/{id}", self.CTX, "admin"), "/api/v2/collections/compat-missing")

    def test_calls_file_names_its_sources_and_has_operations(self):
        calls = json.loads((compat.CALLS).read_text())
        self.assertEqual(set(calls["sources"]), {"silo-android", "silo-apple", "silo-roku"})
        self.assertGreater(len(calls["operations"]), 100)
        for op in calls["operations"]:
            self.assertRegex(op["path"], r"^/api/v[12]/")
            self.assertIn(op["method"], extract_calls.METHODS)
        text = json.dumps(calls)
        self.assertNotRegex(text, r"\"/(?:home|Users|var|tmp)/|https?://(?!siloserver\.org|example\.)[a-z0-9.-]+\.[a-z]{2,}")


class ExtractTest(unittest.TestCase):
    def test_path_chains_resolve_interpolation_and_concatenation(self):
        f = extract_calls.path_chains
        self.assertEqual(f('client.get("/api/v2/catalog/items/${id.encodeURLPathPart()}/versions")', "kotlin")[0][0],
                         "/api/v2/catalog/items/{}/versions")
        self.assertEqual(f('let path = "/api/v2/profiles/\\(try seg(id))/verify-pin"', "swift")[0][0],
                         "/api/v2/profiles/{}/verify-pin")
        self.assertEqual(f('path = "/api/v2/watch/" + catalog.request.segment(itemId) + "/trickplay"', "brightscript")[0][0],
                         "/api/v2/watch/{}/trickplay")

    def test_routes_match_templates_by_segment(self):
        routes = {"/api/v2/watch/{id}": {"GET"}, "/api/v2/watch/{id}/trickplay": {"GET"}, "/api/v2/watchlist/{item_id}": {"PUT"}}
        self.assertEqual(extract_calls.match_route("/api/v2/watch/{}/trickplay", routes), ["/api/v2/watch/{id}/trickplay"])
        self.assertEqual(extract_calls.match_route("/api/v2/watchlist/{}", routes), ["/api/v2/watchlist/{item_id}"])



class NoticeTest(unittest.TestCase):
    def test_a_reading_that_differs_from_silo_is_a_divergence(self):
        silo = {"GET /api/v2/notifications": 0, "GET /api/v2/notifications/unread-count": 0,
                "GET /api/v2/notifications/{id} for the notice": (404, "not_found")}
        bloem = {"GET /api/v2/notifications": 1, "GET /api/v2/notifications/unread-count": 0,
                 "GET /api/v2/notifications/{id} for the notice": (200, None)}
        got = [(d.kind, d.field, d.failing) for d in notices.diffs(silo, bloem)]
        self.assertEqual(got, [("bloem notice", "GET /api/v2/notifications", True),
                               ("bloem notice", "GET /api/v2/notifications/{id} for the notice", True)])

    def test_matching_readings_pass(self):
        same = {"GET /api/v2/notifications": 2, "events socket notifications snapshot": 0}
        self.assertEqual(notices.diffs(same, dict(same)), [])

if __name__ == "__main__":
    unittest.main()
