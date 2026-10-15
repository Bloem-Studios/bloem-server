"""Regressions for the native API documentation coverage report."""

import importlib.util
from pathlib import Path
import unittest


class CatalogueTests(unittest.TestCase):
    def setUp(self):
        path = Path(__file__).with_name("bloem-api-catalogue.py")
        self.assertTrue(path.exists(), "API catalogue generator is missing")
        spec = importlib.util.spec_from_file_location("catalogue", path)
        self.module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(self.module)

    def render(self, paths, routes):
        return self.module.render(
            {"paths": {"/api/v2/account/me": {"get": {"operationId": "me"}}}},
            {"paths": paths},
            {"routes": routes},
        )

    def test_method_only_dispatch_does_not_advertise_every_http_verb(self):
        routes = [
            {"path": "/api/bloem/v1/admin/jobs", "method": verb,
             "method_origin": "handle_all",
             "handler_expr": "methodOnly(http.MethodPost, handler.Create)",
             "source_file": "internal/api/router_bloem.go"}
            for verb in ("POST", "GET", "DELETE", "CONNECT")
        ]
        report = self.render({}, routes)
        self.assertEqual(report.count("`POST /api/bloem/v1/admin/jobs`"), 1)
        self.assertNotIn("`DELETE /api/bloem/v1/admin/jobs`", report)
        self.assertNotIn("`GET /api/bloem/v1/admin/jobs`", report)

    def test_documented_path_does_not_hide_an_undocumented_method(self):
        path = "/api/bloem/v1/groups/"
        report = self.render(
            {path: {"get": {"operationId": "listGroups", "summary": "List groups."}}},
            [{"path": path, "method": "GET"}, {"path": path, "method": "POST"}],
        )
        self.assertIn("`listGroups`", report)
        missing = report.split("## Inventory entries without typed OpenAPI", 1)[1]
        self.assertIn("`POST /api/bloem/v1/groups/`", missing)
        self.assertNotIn("`GET /api/bloem/v1/groups/`", missing)

    def test_unknown_dispatcher_and_wildcard_remain_explicitly_unresolved(self):
        report = self.render({}, [
            {"path": "/api/bloem/v1/admin/*", "method": "DELETE",
             "method_origin": "handle_all", "handler_expr": "nestedRouter"},
            {"path": "/api/bloem/v1/admin/*", "method": "GET",
             "method_origin": "handle_all", "handler_expr": "nestedRouter"},
            {"path": "/api/bloem/v1/admin/proxy", "method": "POST",
             "method_origin": "handle_all", "handler_expr": "dynamicHandler"},
        ])
        self.assertEqual(report.count("`DISPATCH /api/bloem/v1/admin/*`"), 1)
        self.assertIn("Not a callable-operation promise", report)
        self.assertIn("`DISPATCH /api/bloem/v1/admin/proxy`", report)
        self.assertNotIn("`POST /api/bloem/v1/admin/proxy`", report)

    def test_report_is_sorted_escaped_and_reproducible(self):
        paths = {
            "/api/bloem/v1/z": {"get": {"operationId": "z", "summary": "A | B\nC"}},
            "/api/bloem/v1/a": {"get": {"operationId": "a"}},
        }
        result = self.render(paths, [])
        self.assertEqual(result, self.render(dict(reversed(list(paths.items()))), []))
        self.assertLess(result.index("`GET /api/bloem/v1/a`"), result.index("`GET /api/bloem/v1/z`"))
        self.assertIn("A \\| B C", result)
        self.assertIn("not a complete router inventory", result)


if __name__ == "__main__":
    unittest.main()
