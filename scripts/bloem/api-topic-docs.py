#!/usr/bin/env python3
"""Write docs/bloem/api/: the native /api/bloem/v1 API, one page per topic.

The pages are generated from contracts/api/bloem/v1/openapi.json (make
bloem-openapi), so they list every typed operation with its credentials,
parameters, request and response fields and error codes. Edit the handler's
OpenAPI declaration, not the pages. Each operation goes to the first topic in
TOPICS whose patterns match its path; a new path that matches none fails the run.

Usage (from the repository root):
  python3 scripts/bloem/api-topic-docs.py           # write the pages
  python3 scripts/bloem/api-topic-docs.py --check   # fail if a page is out of date
"""
import json
import os
import re
import sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SPEC = os.path.join(ROOT, "contracts", "api", "bloem", "v1", "openapi.json")
OUT = os.path.join(ROOT, "docs", "bloem", "api")
PREFIX = "/api/bloem/v1"
METHODS = ["get", "head", "post", "put", "patch", "delete"]
SHARED = {"BloemNativeError"}

# slug, title, explainer page, technical pages, path patterns (relative to PREFIX), intro.
TOPICS = [
    ("live-tv", "Live TV", "bloem-live-tv.md",
     [("Live TV client access", "live-tv-client-access.md"), ("Xtream live TV providers", "xtream-live-tv.md"),
      ("Live TV tuner discovery", "../livetv-tuner-discovery.md"),
      ("Media ownership across API replicas", "bloem-cluster-media.md")],
     [r"^/livetv(/|$)"],
     "Channels, the programme guide, live sessions and their delivery, recordings and recording rules, and "
     "the tuner, provider and guide administration behind them."),
    ("storage", "Storage sources", "bloem-storage-sources.md",
     [("Native storage", "bloem-native-storage.md"), ("Native video playback", "bloem-native-video-playback.md"),
      ("Native audiobook presentations", "bloem-native-audio-playback.md")],
     [r"^/admin/(platform|organization)/(native-storage|library-locations)(/|$)"],
     "Storage plugins, sources, credential rotation, bucket and folder browsing, and library locations, in the "
     "platform scope and in an organization's own scope."),
    ("requests", "Requests", "bloem-requests.md",
     [("Bloem request policy", "bloem-request-policy.md"), ("Audiobook requests", "bloem-audiobook-requests.md"),
      ("Ebook requests", "bloem-ebook-request-foundation.md")],
     [r"^/(book|ebook)-requests(/|$)",
      r"^/admin/organization/(audiobook-request-integrations|book-requests|ebook-request-integrations|"
      r"ebook-request-policy|ebook-requests)(/|$)"],
     "Bloem's audiobook and ebook request lanes: filing and reading requests, the organization queues, and "
     "the acquisition connections. Movie and series requests are Silo's, under /api/v2."),
    ("plans", "Plans and preview", "bloem-plans.md",
     [("Entitlement templates guide", "../wiki/admin/entitlement-templates.md"),
      ("Entitlement template operations", "../operations/entitlement-templates.md"),
      ("Bulk policy cohorts", "../operations/bulk-policy-cohorts.md"),
      ("Browse-only content policy", "bloem-browse-only-content-policy.md")],
     [r"^/admin/platform/(entitlement-templates|users|preview)(/|$)",
      r"^/admin/platform/accounts/(entitlement-bulk|entitlement-snapshots)(/|$)",
      r"^/admin/platform/accounts/[^/]+/entitlement(/|$)",
      r"^/admin/organization/entitlement-cohorts(/|$)", r"^/preview(/|$)"],
     "Entitlement templates (plans) and their revisions, applying them to accounts one at a time or in "
     "reviewed bulk jobs, and the storefront preview: its administration and the guest sessions."),
    ("messages", "Messages and promotions", "bloem-messages-and-promotions.md",
     [("Server admin announcements", "admin-announcements.md"), ("Playback overlays", "playback-overlays.md"),
      ("Seasonal scheduling", "seasonal-scheduling.md"), ("Presentation evaluators", "bloem-presentation-evaluators.md"),
      ("Campaign and seasonal asset storage", "bloem-campaign-asset-storage.md")],
     [r"^/notifications(/|$)", r"^/ambience(/|$)", r"^/promotions(/|$)",
      r"^/admin/platform/(ambience|promotions|upgrade-prompt|announcements)(/|$)"],
     "The native inbox, announcements, seasonal packs and their public artwork, the campaign cards a "
     "viewer sees, campaign authoring and the upgrade prompt."),
    ("apps", "Apps and clients", "bloem-apps.md",
     [("Bloem native client surface", "bloem-client-surface.md"),
      ("SDK compatibility and contract generation", "bloem-sdk-compatibility.md"),
      ("Protocol compatibility", "bloem-compatibility-removal.md")],
     [r"^/(capabilities|server|watch|sync|persons|catalog|profile-credentials)(/|$)"],
     "What Bloem's own apps read beyond Silo's API: capabilities, server identity, the composed Watch "
     "documents, progress sync, people, item collections and profile sign-in credentials."),
    ("organizations", "Organizations and administration", "bloem-organizations.md",
     [("Multitenant administration", "multitenant-administration.md"),
      ("Resource tenancy foundation", "resource-tenancy-foundation.md"),
      ("OPA tenant authorization", "opa-tenant-authorization.md"), ("Lifecycle safety", "bloem-lifecycle-safety.md")],
     [r"^/admin/session(/|$)", r"^/organizations(/|$)", r"^/admin/organization/",
      r"^/admin/platform/(organizations|tenants|accounts|library-figures|new-arrivals)(/|$)",
      r"^/admin/search/"],
     "Administrative contexts, organization administration (people, groups, invitations, libraries, grants, "
     "audit and policy decisions), the platform's organization lifecycle and reports, the storefront's reseller "
     "organizations (tenants) and their members, and an account's profiles, devices and sign-in sessions."),
]


def load():
    with open(SPEC) as fh:
        return json.load(fh)


def cell(s):
    return re.sub(r"\s+", " ", str(s or "")).replace("|", "\\|").strip()


def slug(heading):
    s = heading.strip().lower()
    s = re.sub(r"[^\w\- ]", "", s)
    return s.replace(" ", "-")


def ref_name(ref):
    return ref.rsplit("/", 1)[-1]


def schema_link(name):
    if name in SHARED:
        return f"[`{name}`](README.md#{slug(name)})"
    return f"[`{name}`](#{slug(name)})"


def type_of(s):
    if not isinstance(s, dict) or not s:
        return "any"
    if "$ref" in s:
        return schema_link(ref_name(s["$ref"]))
    for key in ("oneOf", "anyOf"):
        if key in s:
            return " or ".join(type_of(x) for x in s[key])
    if "allOf" in s:
        return " and ".join(type_of(x) for x in s["allOf"])
    t = s.get("type")
    if isinstance(t, list):
        base = " or ".join(x for x in t if x != "null")
        out = f"{base} or null" if "null" in t else base
    else:
        out = t or "object"
    if out == "array" or (isinstance(t, list) and "array" in t):
        out = out.replace("array", f"array of {type_of(s.get('items', {}))}")
    if s.get("format"):
        out += f" ({s['format']})"
    if "enum" in s:
        vals = ", ".join(f"`{v}`" for v in s["enum"] if v is not None)
        out += f": {vals}"
    if out == "object" and isinstance(s.get("additionalProperties"), dict):
        out = f"object of {type_of(s['additionalProperties'])}"
    return out


def refs_in(s, found):
    if isinstance(s, dict):
        if "$ref" in s:
            found.add(ref_name(s["$ref"]))
        for v in s.values():
            refs_in(v, found)
    elif isinstance(s, list):
        for v in s:
            refs_in(v, found)


def closure(spec, names):
    schemas = spec["components"]["schemas"]
    todo, seen = list(names), set()
    while todo:
        n = todo.pop()
        if n in seen or n not in schemas:
            continue
        seen.add(n)
        more = set()
        refs_in(schemas[n], more)
        todo.extend(more - seen)
    return seen


def fields_table(schema):
    props = schema.get("properties") or {}
    if not props:
        return None
    req = set(schema.get("required") or [])
    rows = ["| Field | Type | Required | Description |", "| --- | --- | --- | --- |"]
    for name, p in props.items():
        desc = p.get("description", "") if isinstance(p, dict) else ""
        rows.append(f"| `{name}` | {cell(type_of(p))} | {'yes' if name in req else ''} | {cell(desc)} |")
    return "\n".join(rows)


def body_schema(content):
    for ctype, media in (content or {}).items():
        return ctype, media.get("schema", {})
    return None, None


def operations(spec):
    for path, item in spec["paths"].items():
        for m in METHODS:
            if m in item:
                yield m.upper(), path, item[m], item.get("parameters", [])


def topic_of(path):
    short = path[len(PREFIX):] if path.startswith(PREFIX) else path
    for t in TOPICS:
        if any(re.search(p, short) for p in t[4]):
            return t[0]
    sys.exit(f"{path}: matches no topic; add it to TOPICS")


def security_names(op):
    sec = op.get("security")
    if sec is None:
        return None
    names = []
    for alt in sec:
        names.append(" and ".join(f"`{k}`" for k in alt) or "none")
    return names


def render_op(spec, method, path, op, shared_params, used):
    lines = [f"### `{method} {path}`", ""]
    if op.get("summary"):
        lines += [op["summary"].strip(), ""]
    lines.append(f"Operation ID: `{op.get('operationId', '')}`.")
    sec = security_names(op)
    if sec is None or sec == ["none"] or not sec:
        lines.append("Credentials: none (public).")
    else:
        lines.append("Credentials: " + "; or ".join(sec) + ".")
    lines.append("")
    if op.get("description"):
        lines += [op["description"].strip(), ""]
    params = list(shared_params) + list(op.get("parameters", []))
    if params:
        lines += ["| Parameter | In | Type | Required | Description |", "| --- | --- | --- | --- | --- |"]
        for p in params:
            if "$ref" in p:
                p = spec["components"].get("parameters", {}).get(ref_name(p["$ref"]), {})
            lines.append(f"| `{p.get('name')}` | {p.get('in')} | {cell(type_of(p.get('schema', {})))} | "
                         f"{'yes' if p.get('required') else ''} | {cell(p.get('description', ''))} |")
        lines.append("")
    rb = op.get("requestBody")
    if rb:
        ctype, schema = body_schema(rb.get("content"))
        refs_in(schema, used)
        req = "required" if rb.get("required") else "optional"
        lines.append(f"Request body ({req}, `{ctype}`): {type_of(schema)}.")
        if rb.get("description"):
            lines.append(cell(rb["description"]))
        lines.append("")
    resp = op.get("responses", {})
    if resp:
        lines += ["| Status | Body | Meaning |", "| --- | --- | --- |"]
        for code, r in resp.items():
            ctype, schema = body_schema(r.get("content"))
            refs_in(schema, used)
            body = type_of(schema) if ctype else ""
            if ctype and ctype != "application/json":
                body = f"`{ctype}`" + (f" {body}" if schema else "")
            lines.append(f"| {code} | {cell(body)} | {cell(r.get('description', ''))} |")
        lines.append("")
    return lines


def render_topic(spec, topic, ops):
    slug_, title, explainer, tech, _, intro = topic
    used = set()
    body = []
    for method, path, op, shared in ops:
        body += render_op(spec, method, path, op, shared, used)
    schemas = sorted(closure(spec, used) - SHARED)
    head = [
        f"# {title} API",
        "",
        "> **Generated** by `scripts/bloem/api-topic-docs.py` from",
        "> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json).",
        "> Do not edit by hand: change the handler's OpenAPI declaration, run `make bloem-openapi`, then",
        "> the script. Shared conventions, credentials and the error body are on the [index](README.md).",
        "",
        intro,
        "",
        f"For the plain-language picture, read the [{title} explainer](../../architecture/{explainer}). "
        "The invariants are in:",
        "",
    ]
    head += [f"- [{name}]({os.path.normpath(os.path.join('../../architecture', path))})" for name, path in tech]
    head += ["", f"## Operations ({len(ops)})", "", "| Method and path | Summary |", "| --- | --- |"]
    for method, path, op, _ in ops:
        anchor = slug(f"{method} {path}")
        head.append(f"| [`{method} {path}`](#{anchor}) | {cell(op.get('summary', ''))} |")
    head += ["", "## Reference", ""]
    tail = ["## Schemas", ""]
    for name in schemas:
        s = spec["components"]["schemas"][name]
        tail += [f"### {name}", ""]
        if s.get("description"):
            tail += [cell(s["description"]), ""]
        table = fields_table(s)
        tail += [table if table else f"Type: {type_of({k: v for k, v in s.items() if k != 'description'})}.", ""]
    return "\n".join(head + body + tail).rstrip() + "\n"


def render_index(spec, counts):
    schemes = spec["components"]["securitySchemes"]
    lines = [
        "# Bloem native API by topic",
        "",
        "> **Generated** by `scripts/bloem/api-topic-docs.py` from",
        "> [`contracts/api/bloem/v1/openapi.json`](../../../contracts/api/bloem/v1/openapi.json); do not edit",
        "> by hand. Bloem's routes on Silo's `/api/v1`, which no OpenAPI document describes, are on the",
        "> hand-written [/api/v1 routes](v1-routes.md) page.",
        "",
        "Every typed operation under `/api/bloem/v1`, grouped by the topics of Bloem's explainer pages. The",
        "[wire reference](../../bloem-api-reference.md) adds behavior that does not fit a schema, and the",
        "[API catalogue](../api-catalogue.md) checks the contract against the mounted routes.",
        "",
        "| Topic | Operations | Explainer |",
        "| --- | ---: | --- |",
    ]
    for t in TOPICS:
        lines.append(f"| [{t[1]}]({t[0]}.md) | {counts[t[0]]} | [{t[2]}](../../architecture/{t[2]}) |")
    lines.append(f"| [/api/v1 routes](v1-routes.md) | hand-written | |")
    lines += [
        "",
        "## Conventions",
        "",
        "- **Base path.** Every path on these pages is absolute, starting at `/api/bloem/v1`, on the server's",
        "  main address.",
        "- **Profile headers.** Viewer operations act for one household profile: `X-Profile-Id` names it, and",
        "  `X-Profile-Token` carries the proof for a PIN-locked profile. API keys skip PIN verification.",
        "- **Administrative contexts.** Organization and platform administration use a separate token from",
        "  `POST /api/bloem/v1/admin/session` (see [organizations](organizations.md)); an ordinary account",
        "  token does not replace it, and the two contexts are never mixed.",
        "- **Revisions.** Many mutations take the revision the caller last read (for example",
        "  `expected_revision`) and answer `409` when it moved; each operation says so. Read again and review",
        "  before retrying.",
        "",
        "## Credentials",
        "",
        "| Scheme | Description |",
        "| --- | --- |",
    ]
    for name, s in schemes.items():
        lines.append(f"| `{name}` | {cell(s.get('description', ''))} |")
    lines += ["", "## Errors", ""]
    for name in sorted(SHARED):
        s = spec["components"]["schemas"].get(name)
        if not s:
            continue
        lines += [f"### {name}", ""]
        if s.get("description"):
            lines += [cell(s["description"]), ""]
        lines += [fields_table(s) or "", ""]
    lines += ["Each operation lists its own error codes under the status that carries them.", "",
              "## Regenerating", "", "```sh", "make bloem-openapi",
              "python3 scripts/bloem/api-topic-docs.py", "```", "",
              "`make verify-bloem-api-catalogue`, which CI runs, fails when a page is out of date.", ""]
    return "\n".join(lines)


def main(argv):
    check = "--check" in argv
    spec = load()
    by_topic = {t[0]: [] for t in TOPICS}
    for method, path, op, shared in operations(spec):
        by_topic[topic_of(path)].append((method, path, op, shared))
    pages = {f"{t[0]}.md": render_topic(spec, t, by_topic[t[0]]) for t in TOPICS}
    pages["README.md"] = render_index(spec, {k: len(v) for k, v in by_topic.items()})
    stale = []
    os.makedirs(OUT, exist_ok=True)
    for name, text in pages.items():
        path = os.path.join(OUT, name)
        if check:
            current = open(path).read() if os.path.exists(path) else None
            if current != text:
                stale.append(os.path.relpath(path, ROOT))
            continue
        with open(path, "w") as fh:
            fh.write(text)
        print(os.path.relpath(path, ROOT))
    if stale:
        sys.exit("out of date, run python3 scripts/bloem/api-topic-docs.py:\n  " + "\n  ".join(stale))


if __name__ == "__main__":
    main(sys.argv[1:])
