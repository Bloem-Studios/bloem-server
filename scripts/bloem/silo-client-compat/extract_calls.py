#!/usr/bin/env python3
"""Build contracts/client/silo-clients/calls.json from Silo's own client apps.

    python3 scripts/bloem/silo-client-compat/extract_calls.py \
        --android DIR --apple DIR --roku DIR [--openapi FILE] [--inventory FILE] [--out FILE]

Commands assume the repository root is the cwd. Each DIR is a checkout of
Silo-Server/silo-android, silo-apple or silo-roku. The script reads their
shipped sources (tests, docs and developer spikes are skipped), finds every
`/api/v1` and `/api/v2` path they request, matches it to an operation in
Silo's OpenAPI document (`/api/v2`) or route inventory (`/api/v1`), and
records per operation: the methods, the query parameters, request headers and
body fields the client sends, and the response fields it decodes, marking a
field required when the client's decoder fails without it.

It reads source text with patterns, not a compiler, so the result is a close
approximation: every entry names the source lines it came from, and
`overrides.json` next to this script corrects what the patterns get wrong.
The harness (compat.py) uses the decoded fields to rank a difference; it
compares the whole response either way, so a missed field still shows up.
"""

import argparse
import json
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path

ROOT = Path(__file__).resolve().parents[3]
HERE = Path(__file__).resolve().parent

SKIP_PARTS = {
    "test", "tests", "Tests", "androidUnitTest", "commonTest", "androidTest", "androidInstrumentedTest",
    "jvmTest", "iosTest", "testFixtures", "docs", "scripts", "ui-validation", "baselineprofile",
    "baselineprofile-tv", "fastlane", "build", "dev", "Preview Content", "node_modules", "mockups",
}
SKIP_SUFFIXES = ("Tests", "UITests", "Test", "Previews")

EXTS = {".kt": "kotlin", ".swift": "swift", ".bs": "brightscript", ".brs": "brightscript"}
METHODS = ("GET", "POST", "PUT", "PATCH", "DELETE", "HEAD")


def shipped_sources(root):
    for path in sorted(root.rglob("*")):
        if path.suffix not in EXTS or not path.is_file():
            continue
        rel = path.relative_to(root)
        if any(p in SKIP_PARTS or p.endswith(SKIP_SUFFIXES) for p in rel.parts[:-1]):
            continue
        if rel.stem.endswith(("Test", "Tests", "Fixtures", "Preview", "Previews", "Mock", "Mocks", "Fake")):
            continue
        yield path, rel


# ---------------------------------------------------------------- path literals

STRING = re.compile(r'"((?:[^"\\]|\\.)*)"')


def is_comment(line, lang):
    s = line.lstrip()
    if lang == "brightscript":
        return s.startswith("'") or s.lower().startswith("rem ")
    return s.startswith(("//", "*", "/*"))


def literal_text(raw, lang):
    """A string literal's text with every interpolation replaced by {}."""
    if lang == "kotlin":
        raw = re.sub(r"\$\{(?:[^{}]|\{[^{}]*\})*\}", "{}", raw)
        raw = re.sub(r"\$[A-Za-z_][A-Za-z0-9_]*", "{}", raw)
    elif lang == "swift":
        out, i = [], 0
        while i < len(raw):
            if raw.startswith("\\(", i):
                depth, j = 1, i + 2
                while j < len(raw) and depth:
                    depth += {"(": 1, ")": -1}.get(raw[j], 0)
                    j += 1
                out.append("{}")
                i = j
            else:
                out.append(raw[i])
                i += 1
        raw = "".join(out)
    return raw


def operand_end(line, start):
    """End of the expression that starts at `start`, up to a top-level + , ) ] }."""
    depth, i, quote = 0, start, False
    while i < len(line):
        c = line[i]
        if quote:
            if c == "\\":
                i += 1
            elif c == '"':
                quote = False
        elif c == '"':
            quote = True
        elif c in "([{":
            depth += 1
        elif c in ")]}":
            if depth == 0:
                return i
            depth -= 1
        elif depth == 0 and c in "+,":
            return i
        i += 1
    return i


def path_chains(line, lang):
    """Every request path the line builds, interpolations and `+` operands as {}."""
    found = []
    for m in STRING.finditer(line):
        text = literal_text(m.group(1), lang)
        at = text.find("/api/v")
        if at < 0 or not re.match(r"/api/v[12](/|$)", text[at:]):
            continue
        path = text[at:]
        i = m.end()
        while True:
            rest = line[i:]
            plus = re.match(r"\s*\+\s*", rest)
            if not plus:
                break
            i += plus.end()
            lit = STRING.match(line, i)
            if lit:
                path += literal_text(lit.group(1), lang)
                i = lit.end()
            else:
                end = operand_end(line, i)
                if end == i:
                    break
                path += "{}"
                i = end
        found.append((path, m.start()))
    return found


def split_query(path):
    base, _, query = path.partition("?")
    names = [q.split("=", 1)[0] for q in query.split("&") if q and q.split("=", 1)[0] not in ("", "{}")]
    return base, names


# ---------------------------------------------------------------- route table

def git_show(ref, path):
    return subprocess.run(["git", "show", f"{ref}:{path}"], cwd=ROOT, check=True, capture_output=True, text=True).stdout


def load_routes(silo_ref):
    routes = defaultdict(set)
    spec = json.loads(git_show(silo_ref, "contracts/api/v2/openapi.json"))
    for path, ops in spec["paths"].items():
        for method in ops:
            if method.upper() in METHODS:
                routes[path].add(method.upper())
    inv = json.loads(git_show(silo_ref, "contracts/api/v2/route-inventory.json"))
    if inv:
        for route in inv.get("routes", []):
            path, method = route.get("path", ""), route.get("method", "").upper()
            if path.startswith("/api/v1") and method in METHODS:
                routes[re.sub(r"\{([^}:]+):[^}]*\}", r"{\1}", path)].add(method)
    return routes


def segment_matches(client, template):
    if template.startswith("{") and template.endswith("}"):
        return 1 if client else None
    if client == template:
        return 0
    if "{}" in client:
        pattern = "^" + ".*".join(re.escape(p) for p in client.split("{}")) + "$"
        if re.match(pattern, template):
            return 2
    return None


def match_route(path, routes):
    """Templates the client path can reach, best (fewest guessed literals) first."""
    segs = path.rstrip("/").split("/")
    scored = []
    for template in routes:
        tsegs = template.split("/")
        if len(tsegs) != len(segs):
            continue
        cost = 0
        for c, t in zip(segs, tsegs):
            s = segment_matches(c, t)
            if s is None:
                break
            cost += s * (10 if s == 2 else 1)
        else:
            scored.append((cost, template))
    scored.sort()
    if not scored:
        return []
    best = [t for c, t in scored if c == scored[0][0]]
    # A wildcard that stands for a literal segment ($list, Body.kind) may reach
    # several routes; keep every one the client can name.
    if scored[0][0] >= 10:
        best = [t for c, t in scored if c < 20 + scored[0][0]]
    return best


# ---------------------------------------------------------------- method, query, headers, body

def window(lines, i, before, after):
    return lines[max(0, i - before): i + after + 1]


METHOD_RX = [
    re.compile(r"HttpMethod\.(Get|Post|Put|Delete|Patch|Head)\b"),
    re.compile(r"\bmethod\s*[:=]\s*\.?\"?(GET|POST|PUT|DELETE|PATCH|HEAD|get|post|put|delete|patch)\b"),
    re.compile(r"httpMethod\s*=\s*\"(GET|POST|PUT|DELETE|PATCH)\""),
    re.compile(r"\"(GET|POST|PUT|DELETE|PATCH|HEAD)\"\s*,"),
]


def methods_in(text):
    found = []
    for rx in METHOD_RX:
        found += [m.upper() for m in rx.findall(text)]
    return found


def detect_method(lines, i, col, lang):
    """Methods the call can use, nearest evidence first; [] when nothing says."""
    line = lines[i]
    head = line[:col]
    if lang in ("kotlin", "swift"):
        m = re.search(r"\.(get|post|put|delete|patch|head|prepareGet|preparePost|preparePut|prepareDelete)(?:Unauthenticated)?\(\s*(?:[a-zA-Z]+:\s*[^,]*,\s*)*(?:path:\s*)?$", head)
        if m:
            return [m.group(1).replace("prepare", "").upper()]
    same = methods_in(line)
    if same or lang == "brightscript":
        return same
    for text in window(lines, i, 3, 6):
        found = methods_in(text)
        if found:
            return found
    return []


def detect_query(lines, i, lang):
    names = set()
    for text in window(lines, i, 1, 14):
        if lang == "kotlin":
            names.update(re.findall(r'\bparameter\(\s*"([A-Za-z0-9_\[\].-]+)"', text))
            names.update(re.findall(r'parameters\.(?:append|set)\(\s*"([A-Za-z0-9_.-]+)"', text))
        elif lang == "swift":
            names.update(re.findall(r'URLQueryItem\(name:\s*"([A-Za-z0-9_.-]+)"', text))
            for q in re.findall(r"query:\s*\[([^\]]*)\]", text):
                names.update(re.findall(r'"([a-z][a-z0-9_]*)"\s*:', q))
        else:
            for q in re.findall(r"query:\s*\{([^}]*)\}", text):
                names.update(re.findall(r"([a-z_][a-z0-9_]*)\s*:", q))
    return names


def detect_headers(lines, i, lang):
    names = set()
    for text in window(lines, i, 2, 12):
        names.update(re.findall(r'"((?:X-|Authorization|If-Match|If-None-Match|Range|Accept-Language)[A-Za-z0-9-]*)"', text))
    return {n for n in names if n not in ("X-",)}


def detect_auth(lines, i, lang):
    for text in window(lines, i, 2, 8):
        if lang == "kotlin" and "skipSiloAuth()" in text:
            return "none"
        if lang == "swift" and re.search(r"Unauthenticated\(|authenticated:\s*false", text):
            return "none"
        if lang == "brightscript":
            m = re.search(r'auth:\s*"(\w+)"', text)
            if m:
                return m.group(1)
    return None


# ---------------------------------------------------------------- types

KT_CLASS = re.compile(r"\b(?:data\s+)?class\s+([A-Z][A-Za-z0-9_]*)\s*(?:<[^>(]*>)?\s*(?:@[A-Za-z]+\s*)?(?:constructor\s*)?\(")
SW_TYPE = re.compile(r"\b(struct|class|enum)\s+([A-Z][A-Za-z0-9_]*)\s*(?:<[^>{]*>)?\s*(?::\s*([^{]*))?\{")


def balanced(text, start, open_c, close_c):
    depth, i = 0, start
    while i < len(text):
        c = text[i]
        if c == open_c:
            depth += 1
        elif c == close_c:
            depth -= 1
            if depth == 0:
                return i
        elif c == '"':
            j = i + 1
            while j < len(text) and text[j] != '"':
                j += 2 if text[j] == "\\" else 1
            i = j
        i += 1
    return len(text)


def split_top(text, sep=","):
    parts, depth, cur, i = [], 0, [], 0
    while i < len(text):
        c = text[i]
        if c in "(<[{":
            depth += 1
        elif c in ")>]}":
            depth -= 1
        if c == sep and depth == 0:
            parts.append("".join(cur))
            cur = []
        else:
            cur.append(c)
        i += 1
    parts.append("".join(cur))
    return parts


def strip_comments(text):
    text = re.sub(r"/\*.*?\*/", "", text, flags=re.S)
    return re.sub(r"(?m)//[^\n]*", "", text)


def kotlin_types(files):
    types = {}
    for path, rel in files:
        text = strip_comments(path.read_text(errors="replace"))
        for m in KT_CLASS.finditer(text):
            name = m.group(1)
            close = balanced(text, m.end() - 1, "(", ")")
            params = text[m.end(): close]
            fields = {}
            for p in split_top(params):
                pm = re.search(r'(?:@SerialName\(\s*"([^"]+)"\s*\)\s*)?(?:@[A-Za-z]+(?:\([^)]*\))?\s*)*(?:override\s+)?(?:val|var)\s+([A-Za-z_][A-Za-z0-9_]*)\s*:\s*([^=]+?)\s*(=.*)?$', p.strip(), re.S)
                if not pm:
                    continue
                serial = pm.group(1)
                if not serial:
                    sm = re.search(r'@SerialName\(\s*"([^"]+)"', p)
                    serial = sm.group(1) if sm else pm.group(2)
                if "@Transient" in p:
                    continue
                ktype = " ".join(pm.group(3).split())
                nullable = ktype.endswith("?")
                fields[serial] = {"type": ktype.rstrip("?"), "required": not nullable and pm.group(4) is None}
            if fields and (name not in types or len(fields) > len(types[name]["fields"])):
                types[name] = {"fields": fields, "source": f"{rel}"}
    return types


def snake(name):
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", name)
    s = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1_\2", s)
    return s.lower()


def swift_types(files):
    types = {}
    for path, rel in files:
        text = strip_comments(path.read_text(errors="replace"))
        for m in SW_TYPE.finditer(text):
            kind, name, conformances = m.group(1), m.group(2), m.group(3) or ""
            if kind == "enum":
                continue
            close = balanced(text, m.end() - 1, "{", "}")
            body = text[m.end(): close]
            decodable = re.search(r"\b(Decodable|Codable)\b", conformances) or re.search(r"init\(from decoder", body)
            if not decodable:
                continue
            # Top-level stored properties only: drop nested bodies.
            flat, depth = [], 0
            for c in body:
                if c == "{":
                    depth += 1
                    flat.append("{")
                elif c == "}":
                    depth -= 1
                elif depth == 0:
                    flat.append(c)
            props = {}
            for pm in re.finditer(r"(?m)^\s*(?:(?:public|private|internal|fileprivate|nonisolated|@[A-Za-z]+)\s+)*(let|var)\s+([a-zA-Z_][A-Za-z0-9_]*)\s*:\s*([^=\n{]+?)\s*(=[^\n]*)?(\{)?\s*$", "".join(flat)):
                if pm.group(5) or re.search(r"\bstatic\b", pm.group(0)):
                    continue
                if pm.group(1) == "let" and pm.group(4):
                    continue  # a let with a value is never decoded
                stype = pm.group(3).strip()
                props[pm.group(2)] = {"type": stype.rstrip("?!"), "optional": stype.endswith(("?", "!")) or stype.startswith("Optional<")}
            keys = {}
            ck = re.search(r"enum\s+CodingKeys\s*:[^{]*\{", body)
            if ck:
                ck_body = body[ck.end(): balanced(body, ck.end() - 1, "{", "}")]
                for case in re.findall(r"case\s+([^\n]+)", ck_body):
                    for item in split_top(case):
                        im = re.match(r'\s*`?([A-Za-z_][A-Za-z0-9_]*)`?\s*(?:=\s*"([^"]*)")?', item)
                        if im:
                            keys[im.group(1)] = im.group(2) or im.group(1)
            custom = re.search(r"init\(from decoder:", body)
            fields = {}
            if custom:
                init_body = body[custom.end(): balanced(body, body.find("{", custom.end()), "{", "}")]
                for dm in re.finditer(r"\.(decode|decodeIfPresent|value|optional|decodeLossy\w*|lenient\w*)\(\s*([^,()]*?)(?:\.self)?\s*,?\s*(?:forKey:\s*)?\.([A-Za-z_][A-Za-z0-9_]*)", init_body):
                    prop = dm.group(3)
                    json_key = snake(keys.get(prop, prop))
                    required = dm.group(1) == "decode" and not dm.group(2).endswith("?")
                    ptype = props.get(prop, {}).get("type") or dm.group(2) or "Any"
                    fields[json_key] = {"type": ptype, "required": required}
            else:
                for prop, info in props.items():
                    if ck and prop not in keys:
                        continue
                    fields[snake(keys.get(prop, prop))] = {"type": info["type"], "required": not info["optional"]}
            if fields and (name not in types or len(fields) > len(types[name]["fields"])):
                types[name] = {"fields": fields, "source": f"{rel}"}
    return types


KT_WRAPPED = re.compile(r"^(?:List|Set|Collection|Array|MutableList|Sequence)<(.+)>$")
KT_MAP = re.compile(r"^(?:Map|MutableMap|LinkedHashMap|HashMap)<[^,]+,\s*(.+)>$")
SW_ARRAY = re.compile(r"^\[([^:\]]+)\]$")
SW_DICT = re.compile(r"^\[[^:\]]+:\s*(.+)\]$")
SCALARS = {
    "String": "string", "Int": "number", "Long": "number", "Double": "number", "Float": "number",
    "Boolean": "boolean", "Bool": "boolean", "Int64": "number", "Int32": "number", "UInt": "number",
    "Date": "string", "URL": "string", "UUID": "string", "Instant": "string", "Decimal": "number",
    "JsonElement": "any", "JsonObject": "object", "JsonArray": "array", "AnyCodable": "any", "JSONValue": "any",
}


def json_kind(t, types):
    t = t.strip().rstrip("?!")
    if t in SCALARS:
        return SCALARS[t], None
    for rx, kind in ((KT_WRAPPED, "array"), (SW_ARRAY, "array"), (KT_MAP, "object"), (SW_DICT, "object")):
        m = rx.match(t)
        if m:
            return kind, m.group(1).strip()
    generic = re.match(r"^([A-Za-z0-9_.]+)<(.+)>$", t)
    base = generic.group(1).split(".")[-1] if generic else t.split(".")[-1]
    if base in types:
        return "object", base
    return "unknown", None


def flatten(type_name, types, prefix="", seen=(), depth=0, out=None):
    out = {} if out is None else out
    t = types.get(type_name)
    if not t or depth > 5 or type_name in seen:
        return out
    for key, info in t["fields"].items():
        path = f"{prefix}{key}"
        kind, inner = json_kind(info["type"], types)
        out[path] = {"type": kind, "required": info["required"]}
        while kind == "array" and inner:
            path += "[]"
            kind, inner2 = json_kind(inner, types)
            if kind in ("object",) and inner2 is None and inner in types:
                inner2 = inner
            if kind == "array":
                out[path] = {"type": kind, "required": False}
            inner = inner2
            if kind != "array":
                break
        if kind == "object" and inner:
            flatten(inner, types, path + ("" if path.endswith("[]") else "") + ("." if not path.endswith("[]") else "."), seen + (type_name,), depth + 1, out)
    return out


TYPE_PATTERNS = {
    "kotlin": [
        re.compile(r"\b(?:ownedV2Call|safeApiV2Call|safeApiCall|read|exchange|call|bound|get|post|request)<\s*([A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?)"),
        re.compile(r"\.body<\s*([A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?)\s*>"),
        re.compile(r"decodeFromString\(\s*(?:ListSerializer\()?([A-Z][A-Za-z0-9_.]*)\.serializer\(\)"),
        re.compile(r"decodeFrom(?:String|JsonElement)<\s*([A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?)\s*>"),
    ],
    "swift": [
        re.compile(r"\bdecode\(\s*(\[?[A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?\]?)\.self"),
        re.compile(r"\bas:\s*(\[?[A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?\]?)\.self"),
        re.compile(r"\blet\s+[a-z][A-Za-z0-9_]*\s*:\s*(\[?[A-Z][A-Za-z0-9_.]*(?:<[^=]*>)?\]?)\s*=\s*try\s+await"),
        re.compile(r"(?:requestGet|householdRequest|requestJSON|decodeJSON|getJSON)\s*<\s*([A-Z][A-Za-z0-9_.]*)"),
    ],
}
RETURN_TYPE = {
    "kotlin": re.compile(r"\bfun\s+[^(]*\([^)]*\)?[^:]*:\s*(?:ApiResult|Result)<\s*([A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?)"),
    "swift": re.compile(r"\bfunc\s+[^(]*\(.*\)\s*(?:async\s*)?(?:throws\s*)?->\s*(\[?[A-Z][A-Za-z0-9_.]*(?:<[^>]*>)?\]?)"),
}
NO_BODY = {"Unit", "Void", "HttpResponse", "Data", "Bool", "String", "ByteArray", "Boolean", "Nothing", "URL", "URLRequest", "T", "R"}


def unwrap_generic(t, types):
    """CatalogPage<X> → CatalogPage when known, else X; [X] stays an array."""
    t = t.strip()
    m = re.match(r"^([A-Za-z0-9_.]+)<(.+)>$", t)
    if m:
        outer = m.group(1).split(".")[-1]
        if outer in ("ApiResult", "Result", "Optional", "Response"):
            return unwrap_generic(m.group(2), types)
        if outer in types:
            return outer
        return unwrap_generic(split_top(m.group(2))[0], types)
    return t.split(".")[-1]


def detect_response(lines, i, lang, types):
    if lang not in TYPE_PATTERNS:
        return None
    order = [i] + [i + d for d in range(1, 13)] + [i - d for d in range(1, 30)]
    for k in order:
        if not 0 <= k < len(lines):
            continue
        for rx in TYPE_PATTERNS[lang]:
            m = rx.search(lines[k])
            if m:
                t = unwrap_generic(m.group(1), types)
                if t.strip("[]") in NO_BODY:
                    return "-"
                return t
    for k in range(i, max(-1, i - 60), -1):
        m = RETURN_TYPE[lang].search(lines[k])
        if m:
            t = unwrap_generic(m.group(1), types)
            return "-" if t.strip("[]") in NO_BODY else t
        if re.search(r"\b(fun|func)\s", lines[k]):
            break
    return None


# ---------------------------------------------------------------- driver

def git_head(root):
    try:
        return subprocess.run(["git", "-C", str(root), "rev-parse", "HEAD"], check=True,
                              capture_output=True, text=True).stdout.strip()
    except (subprocess.CalledProcessError, FileNotFoundError):
        return "unknown"


def extract(client, root, routes):
    files = list(shipped_sources(root))
    lang_files = defaultdict(list)
    for path, rel in files:
        lang_files[EXTS[path.suffix]].append((path, rel))
    types = {}
    types.update(kotlin_types(lang_files["kotlin"]))
    types.update(swift_types(lang_files["swift"]))
    calls, unmatched = [], []
    for path, rel in files:
        lang = EXTS[path.suffix]
        lines = path.read_text(errors="replace").split("\n")
        for i, line in enumerate(lines):
            if "/api/v" not in line or is_comment(line, lang):
                continue
            for raw, col in path_chains(line, lang):
                if re.search(r"(?:startsWith|hasPrefix|hasSuffix|endsWith|contains|matches|==|!=)\s*\(?\s*$", line[:col]):
                    continue  # recognizes a path, does not request one
                base, qnames = split_query(raw)
                base = re.sub(r"(\{\})+", "{}", base)
                if re.search(r"[\[\]()$\\^+*]", base):
                    continue  # a pattern that recognizes paths, not a request
                literal = [s for s in base.split("/")[3:] if s and "{}" not in s]
                if not literal:
                    continue  # a generic helper: its callers name the route
                if base.rstrip("/") in ("/api/v1", "/api/v2") or base.endswith("/") and not match_route(base, routes):
                    continue
                templates = match_route(base, routes)
                where = f"{rel}:{i + 1}"
                if not templates and base.startswith("/api/v1/"):
                    # Retired upstream: Silo answers it with its tombstone, and so must Bloem.
                    template = re.sub(r"\{\}", "{id}", base)
                    routes.setdefault(template, set()).update(detect_method(lines, i, col, lang) or ["GET"])
                    templates = [template]
                if not templates:
                    unmatched.append({"client": client, "path": raw, "source": where})
                    continue
                methods = detect_method(lines, i, col, lang)
                response = detect_response(lines, i, lang, types)
                for template in templates:
                    allowed = sorted(routes[template])
                    chosen = [m for m in dict.fromkeys(methods) if m in allowed]
                    inferred = not chosen
                    if not chosen:
                        chosen = ["GET"] if "GET" in allowed and not methods else allowed if not methods else []
                    if not chosen:
                        unmatched.append({"client": client, "path": raw, "source": where,
                                          "note": f"method {','.join(methods)} not in {','.join(allowed)} for {template}"})
                        continue
                    for m in chosen:
                        calls.append({
                        "client": client, "method": m, "path": template, "method_inferred": inferred,
                        "query": sorted(set(qnames) | detect_query(lines, i, lang)),
                        "headers": sorted(detect_headers(lines, i, lang)),
                        "auth": detect_auth(lines, i, lang),
                        "response_type": response if m != "HEAD" else "-", "source": where,
                        })
    return calls, unmatched, types


def merge(all_calls, all_types, overrides):
    by_op = {}
    for call in all_calls:
        key = (call["method"], call["path"])
        op = by_op.setdefault(key, {
            "method": call["method"], "path": call["path"], "clients": {}, "query": set(), "headers": set(),
            "auth": set(), "response_types": {}, "response": {},
        })
        op["clients"].setdefault(call["client"], []).append(call["source"])
        op["query"].update(call["query"])
        op["headers"].update(call["headers"])
        if call["auth"]:
            op["auth"].add(call["auth"])
        rt = call["response_type"]
        if rt and rt != "-":
            types = all_types[call["client"]]
            fields = flatten(rt, types)
            if fields:
                op["response_types"].setdefault(call["client"], set()).add(rt)
                for path, info in fields.items():
                    cur = op["response"].setdefault(path, {"type": info["type"], "required_by": [], "read_by": []})
                    if cur["type"] in ("unknown", "any") and info["type"] not in ("unknown", "any"):
                        cur["type"] = info["type"]
                    bucket = "required_by" if info["required"] else "read_by"
                    if call["client"] not in cur[bucket]:
                        cur[bucket].append(call["client"])
            else:
                op["response_types"].setdefault(call["client"], set()).add(rt + " (unresolved)")
    for key, extra in overrides.get("calls", {}).items():
        method, path = key.split(" ", 1)
        op = by_op.setdefault((method, path), {
            "method": method, "path": path, "clients": {}, "query": set(), "headers": set(), "auth": set(),
            "response_types": {}, "response": {},
        })
        for client, src in extra.get("clients", {}).items():
            op["clients"].setdefault(client, []).extend(src)
        op["query"].update(extra.get("query", []))
        op["headers"].update(extra.get("headers", []))
        for path_, info in extra.get("response", {}).items():
            op["response"][path_] = info
    out = []
    for (method, path), op in sorted(by_op.items(), key=lambda kv: (kv[0][1], kv[0][0])):
        for info in op["response"].values():
            info["required_by"].sort()
            info["read_by"] = sorted(c for c in info["read_by"] if c not in info["required_by"])
        out.append({
            "method": method, "path": path,
            "clients": {c: sorted(set(s)) for c, s in sorted(op["clients"].items())},
            "query": sorted(op["query"]), "headers": sorted(op["headers"]), "auth": sorted(op["auth"]),
            "response_types": {c: sorted(t) for c, t in sorted(op["response_types"].items())},
            "response": dict(sorted(op["response"].items())),
        })
    return out


def render(doc):
    """JSON with one line per operation member and per decoded field, for readable diffs."""
    one = lambda v: json.dumps(v, ensure_ascii=False)  # noqa: E731
    out = ["{"]
    for key in ("schema", "generated_by", "sources", "silo_routes_from"):
        out.append(f" {one(key)}: {one(doc[key])},")
    out.append(' "operations": [')
    for i, op in enumerate(doc["operations"]):
        out.append("  {")
        members = [k for k in op if k != "response"]
        for k in members:
            out.append(f"   {one(k)}: {one(op[k])},")
        fields = list(op["response"].items())
        if fields:
            out.append('   "response": {')
            out += [f"    {one(f)}: {one(v)}" + ("," if j < len(fields) - 1 else "") for j, (f, v) in enumerate(fields)]
            out.append("   }")
        else:
            out.append('   "response": {}')
        out.append("  }" + ("," if i < len(doc["operations"]) - 1 else ""))
    out.append(" ],")
    out.append(' "unmatched": [')
    out += [f"  {one(u)}" + ("," if j < len(doc["unmatched"]) - 1 else "") for j, u in enumerate(doc["unmatched"])]
    out.append(" ]")
    out.append("}")
    return "\n".join(out) + "\n"


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    parser.add_argument("--android", type=Path, required=True)
    parser.add_argument("--apple", type=Path, required=True)
    parser.add_argument("--roku", type=Path, required=True)
    parser.add_argument("--silo-ref", help="Silo commit whose OpenAPI and route inventory name the routes"
                        " (default: the merge base of HEAD and upstream/main)")
    parser.add_argument("--overrides", type=Path, default=HERE / "overrides.json")
    parser.add_argument("--out", type=Path, default=ROOT / "contracts/client/silo-clients/calls.json")
    args = parser.parse_args(argv)

    silo_ref = args.silo_ref or subprocess.run(["git", "merge-base", "HEAD", "upstream/main"], cwd=ROOT, check=True,
                                               capture_output=True, text=True).stdout.strip()
    routes = load_routes(silo_ref)
    overrides = json.loads(args.overrides.read_text()) if args.overrides.exists() else {}
    all_calls, all_unmatched, all_types, sources = [], [], {}, {}
    for client, root in (("silo-android", args.android), ("silo-apple", args.apple), ("silo-roku", args.roku)):
        calls, unmatched, types = extract(client, root.resolve(), routes)
        all_calls += calls
        all_unmatched += unmatched
        all_types[client] = types
        sources[client] = git_head(root)
    ops = merge(all_calls, all_types, overrides)
    ignored = set(overrides.get("ignore_unmatched", []))
    unmatched = sorted({(u["client"], u["path"], u["source"], u.get("note", "")) for u in all_unmatched
                        if u["path"] not in ignored})
    doc = {
        "schema": 1,
        "generated_by": "scripts/bloem/silo-client-compat/extract_calls.py",
        "sources": {c: {"repository": f"Silo-Server/{c}", "commit": sha} for c, sha in sources.items()},
        "silo_routes_from": silo_ref,
        "operations": ops,
        "unmatched": [{"client": c, "path": p, "source": s, **({"note": n} if n else {})} for c, p, s, n in unmatched],
    }
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(render(doc))
    with_fields = sum(1 for op in ops if op["response"])
    print(f"{len(ops)} operations ({with_fields} with decoded fields), {len(doc['unmatched'])} unmatched paths -> {args.out}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
