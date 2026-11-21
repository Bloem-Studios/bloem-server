"""Compare one Silo answer with Bloem's answer to the same request.

Values are never compared, only the shape a client decodes: status, problem
type, the headers clients rely on, and every JSON field's presence and type.
Bloem may add fields; it may not drop, retype or empty one Silo sends. The
rules below decide what is volatile, so nothing is ignored by hand:

- ids, timestamps, tokens, URLs and counts differ by value only, and values
  are not compared (except the few in VALUE_RULES that clients check);
- an object whose keys are ids (digits, UUIDs, hashes) is one wildcard key;
- a field null on one side and set on the other is a divergence only when a
  Silo client requires it (calls.json); otherwise it is a note;
- list lengths are not compared, but a list Silo fills and Bloem leaves empty
  is a divergence;
- a 429 on one side only is a note: rate-limit windows depend on timing.
"""

import re
from typing import NamedTuple

# Headers a Silo client reads. Presence must match; values are volatile,
# except the media type of Content-Type.
CLIENT_HEADERS = (
    "etag", "location", "retry-after", "content-range", "accept-ranges", "www-authenticate",
    "cache-control", "deprecation", "sunset", "x-silo-api-version", "sec-websocket-protocol",
)

# Values Silo clients compare against constants before they accept a server.
VALUE_RULES = {
    ("GET", "/api/v2/system/info"): {"api_major": "equal"},
    ("GET", "/api/v2/settings/contract"): {"revision": "at_least"},
}

DYNAMIC_KEY = re.compile(
    r"^(?:\d+|[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}|[0-9a-f]{16,}|[A-Za-z0-9_-]{22,})$"
)


class Diff(NamedTuple):
    kind: str
    field: str
    detail: str
    failing: bool


def json_type(value):
    if value is None:
        return "null"
    if isinstance(value, bool):
        return "boolean"
    if isinstance(value, (int, float)):
        return "number"
    if isinstance(value, str):
        return "string"
    if isinstance(value, list):
        return "array"
    return "object"


def shape(value, path="", out=None):
    """{field path: set of JSON types} over the whole document; lists merge their elements."""
    out = {} if out is None else out
    out.setdefault(path, set()).add(json_type(value))
    if isinstance(value, dict):
        keys = list(value)
        dynamic = keys and all(DYNAMIC_KEY.match(k) for k in keys) and len(keys) > 0
        for k in keys:
            name = "{*}" if dynamic else k
            shape(value[k], f"{path}.{name}" if path else name, out)
    elif isinstance(value, list):
        if value:
            for item in value:
                shape(item, f"{path}[]", out)
        else:
            out.setdefault(f"{path}[]", set())
    return out


def media_type(headers):
    return (headers.get("content-type") or "").split(";")[0].strip().lower()


def problem_type(resp):
    """The problem type (RFC 9457), or the error code of Silo's v1 envelope."""
    body = resp.get("json")
    if isinstance(body, dict) and isinstance(body.get("type"), str):
        return body["type"]
    if isinstance(body, dict) and isinstance(body.get("error"), str):
        return "error:" + body["error"]
    return None


def parent_list_empty(path, empties):
    return any(path.startswith(e) and path != e for e in empties)


def compare(method, path, silo, bloem, decoded=None):
    """Differences between Silo's response and Bloem's, worst first."""
    decoded = decoded or {}
    diffs = []
    if 429 in (silo["status"], bloem["status"]) and silo["status"] != bloem["status"]:
        # A rate limit's window depends on when earlier requests landed, which
        # differs between two servers; the refusal shape itself is compared
        # wherever both refuse.
        return [Diff("rate_limited", "", f"Silo {silo['status']}, Bloem {bloem['status']}; limiter timing", False)]
    if silo["status"] != bloem["status"]:
        diffs.append(Diff("status", "", f"Silo {silo['status']}, Bloem {bloem['status']}", True))
    s_mt, b_mt = media_type(silo["headers"]), media_type(bloem["headers"])
    if s_mt != b_mt and silo["status"] == bloem["status"]:
        diffs.append(Diff("content_type", "", f"Silo {s_mt or '-'}, Bloem {b_mt or '-'}", True))
    s_pt, b_pt = problem_type(silo), problem_type(bloem)
    if silo["status"] >= 400 or bloem["status"] >= 400:
        if s_pt != b_pt:
            diffs.append(Diff("problem_type", "type", f"Silo {s_pt}, Bloem {b_pt}", True))
    for h in CLIENT_HEADERS:
        if h in silo["headers"] and h not in bloem["headers"]:
            diffs.append(Diff("header_missing", h, f"Silo sends {h}, Bloem does not", True))
    if silo["status"] != bloem["status"] or silo.get("json") is None:
        if silo.get("json") is None and bloem.get("json") is None and silo["status"] == bloem["status"]:
            if silo.get("kind") != bloem.get("kind"):
                diffs.append(Diff("body_kind", "", f"Silo {silo.get('kind')}, Bloem {bloem.get('kind')}", True))
        return diffs
    if bloem.get("json") is None:
        diffs.append(Diff("body_kind", "", f"Silo json, Bloem {bloem.get('kind')}", True))
        return diffs
    s_shape, b_shape = shape(silo["json"]), shape(bloem["json"])
    empties = []
    for field in sorted(s_shape):
        s_types = s_shape[field] - {"null"}
        b_types = b_shape.get(field)
        label = field or "(body)"
        need = decoded.get(field.replace("{*}", "*"), {}) or decoded.get(field, {})
        required = bool(need.get("required_by"))
        who = f" (required by {', '.join(need['required_by'])})" if required else ""
        if parent_list_empty(field, empties):
            continue
        if b_types is None:
            if field.endswith("[]"):
                continue
            diffs.append(Diff("field_missing", label, f"Silo sends it, Bloem does not{who}", True))
            continue
        if field.endswith("[]"):
            if s_shape[field] and not b_types:
                empties.append(field)
                diffs.append(Diff("list_empty", label, f"Silo's list has items, Bloem's is empty{who}", True))
            continue
        b_real = b_types - {"null"}
        if s_types and b_real and not (s_types & b_real):
            diffs.append(Diff("field_type", label, f"Silo {'/'.join(sorted(s_types))}, Bloem {'/'.join(sorted(b_real))}{who}", True))
        elif s_types and not b_real:
            diffs.append(Diff("field_null", label, f"Silo sets it, Bloem sends null{who}", required))
    for field, rule in VALUE_RULES.get((method, path), {}).items():
        s_val, b_val = silo["json"].get(field), bloem["json"].get(field) if isinstance(bloem["json"], dict) else None
        if rule == "equal" and s_val != b_val:
            diffs.append(Diff("value", field, f"Silo {s_val!r}, Bloem {b_val!r}; clients check it", True))
        if rule == "at_least" and isinstance(s_val, (int, float)) and not (isinstance(b_val, (int, float)) and b_val >= s_val):
            diffs.append(Diff("value", field, f"Silo {s_val!r}, Bloem {b_val!r}; clients need at least Silo's", True))
    return diffs


class Allowlist:
    """Divergences Bloem accepts, each with a reason. The list may only shrink."""

    def __init__(self, entries):
        self.entries = entries
        self.used = set()

    def match(self, op, persona, diff):
        for i, e in enumerate(self.entries):
            if e["op"] != op or e["kind"] != diff.kind:
                continue
            if e.get("persona", "*") not in ("*", persona):
                continue
            pattern = e.get("field", "*")
            if pattern != "*" and not re.fullmatch(re.escape(pattern).replace(r"\*", ".*"), diff.field):
                continue
            self.used.add(i)
            return e
        return None

    def unused(self):
        return [e for i, e in enumerate(self.entries) if i not in self.used]


def validate_allowlist(entries, previous=None):
    """Problems with the allowlist file: missing reasons, unknown kinds, growth."""
    kinds = {"status", "content_type", "problem_type", "header_missing", "body_kind", "field_missing",
             "list_empty", "field_type", "field_null", "value", "error"}
    problems = []
    for e in entries:
        if not isinstance(e.get("reason"), str) or len(e["reason"].strip()) < 10:
            problems.append(f"entry {e.get('op')} {e.get('kind')}: needs a reason")
        if e.get("kind") not in kinds:
            problems.append(f"entry {e.get('op')}: unknown kind {e.get('kind')!r}")
        if not re.match(r"^(GET|POST|PUT|PATCH|DELETE|HEAD) /api/", e.get("op", "")):
            problems.append(f"entry {e.get('op')!r}: op must be 'METHOD /api/...'")
    if previous is not None and len(entries) > len(previous):
        problems.append(f"the allowlist grew from {len(previous)} to {len(entries)} entries; it may only shrink")
    if previous is not None:
        old = {(e.get("op"), e.get("kind"), e.get("persona", "*"), e.get("field", "*")) for e in previous}
        for e in entries:
            key = (e.get("op"), e.get("kind"), e.get("persona", "*"), e.get("field", "*"))
            if key not in old:
                problems.append(f"new allowlist entry {key}: the list may only shrink")
    return problems
