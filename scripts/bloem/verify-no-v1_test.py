#!/usr/bin/env python3
"""Regression cases for scripts/bloem/verify-no-v1.py's line matcher."""
import importlib.util
import pathlib
import unittest

spec = importlib.util.spec_from_file_location("guard", pathlib.Path(__file__).with_name("verify-no-v1.py"))
guard = importlib.util.module_from_spec(spec)
spec.loader.exec_module(guard)


class RetiredLineTest(unittest.TestCase):
    def test_flags_retired_paths(self):
        for line in [
            'fetch("/api/v1/auth/me")',
            "GET /api/bloem/v1/watch/home",
            "url: '/api/v1/stream/abc?st=x'",
            "const re = /^\\/api\\/v1\\/ambience\\/assets\\//",
            "base = `/api/bloem/v1`",
            "/api/v1",
        ]:
            self.assertTrue(guard.names_retired_api(line), line)

    def test_passes_current_external_and_marked_lines(self):
        for line in [
            'fetch("/api/v2/auth/me")',
            "GET /api/bloem/v2/watch/home",
            'ASRBaseURL: "https://openrouter.ai/api/v1"',
            "readiness: /api/v1/ready and /api/v1/health",
            "/api/v10/thing",
            'refused := "/api/v1/x" // v1-guard: allow (asserts v1 is refused)',
        ]:
            self.assertFalse(guard.names_retired_api(line), line)


if __name__ == "__main__":
    unittest.main()
