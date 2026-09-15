#!/usr/bin/env python3
"""Assign every migration-ledger entry to an API section.

A section is the Phase 4 delivery unit: one cohesive route group that one
section PR ports to /api/v2 (docs/architecture/api-contract.md, "Migration
ledger"). The assignment is a pure function of the entry's listener,
disposition and path, so re-running this script on an unchanged ledger rewrites
the file byte for byte. Rows that need no v2 work (removed, documented
exclusions) still carry a section so the gate can say which PR retires or
documents them.

Usage: scripts/apiv2-ledger/assign_sections.py [--check] [contracts/api/v2/migration.json]
"""

import json
import sys
from collections import OrderedDict

LEDGER = "contracts/api/v2/migration.json"

# Sections whose rows live outside /api/v1 on the API listener.
NODE_SECTIONS = {"proxy": "node-proxy", "transcode_node": "node-transcode"}

# /api/v1/admin/<b> -> section
ADMIN = {
    "users": "admin-users", "access-groups": "admin-users", "ips": "admin-users",
    "sessions": "admin-sessions", "node-sessions": "admin-sessions", "devices": "admin-sessions",
    "api-keys": "admin-sessions",
    "policy": "admin-policy", "invitations": "admin-invitations", "invite-codes": "admin-invitations",
    "settings": "admin-settings", "branding": "admin-settings", "server": "admin-settings",
    "system": "admin-settings", "email": "admin-settings", "rate-limits": "admin-settings",
    "jellyfin-compat": "admin-settings", "playback-routing": "admin-settings",
    "logs": "admin-observability", "dashboard": "admin-observability", "stats": "admin-observability",
    "stream-telemetry": "admin-observability", "playback-history": "admin-observability",
    "diagnostics": "admin-observability",
    "items": "admin-catalog", "markers": "admin-catalog", "catalog": "admin-catalog",
    "people": "admin-catalog", "unmatched": "admin-catalog", "files": "admin-catalog",
    "filesystem": "admin-catalog", "literary-works": "admin-catalog",
    "autoscan": "admin-autoscan",
    "plugins": "admin-plugins",
    "collections": "admin-collections", "collection-groups": "admin-collections",
    "libraries": "admin-collections", "sections": "admin-sections",
    "history-imports": "admin-history-imports", "history-import-sources": "admin-history-imports",
    "notifications": "admin-notifications",
    "requests": "admin-requests", "request-integrations": "admin-requests",
    "request-settings": "admin-requests", "request-users": "admin-requests",
    "tasks": "admin-tasks", "jobs": "admin-tasks", "nodes": "admin-nodes",
    "recommendations": "admin-recommendations",
    "subtitles": "admin-subtitles", "subtitle-providers": "admin-subtitles",
    # Bloem additions on the Silo v1 admin surface.
    "tenants": "admin-tenants", "host-stats": "admin-observability",
    "remote": "admin-sessions", "ambience": "ambience", "promotions": "promotions",
}

# /api/v1/<a> -> section
TOP = {
    "auth": "auth-core", "user": "auth-core", "onboarding": "auth-core", "policy": "auth-core",
    "invitations": "auth-invitations", "api-keys": "auth-devices", "devices": "auth-devices",
    "profiles": "profiles", "profile": "profiles",
    "settings": "settings", "audio-prefs": "settings-prefs", "subtitle-prefs": "settings-prefs",
    "library-playback-prefs": "settings-prefs", "theme": "settings-branding", "branding": "settings-branding",
    "libraries": "catalog-libraries", "library": "catalog-libraries",
    "catalog": "catalog-items", "items": "catalog-items", "people": "catalog-items",
    "works": "catalog-items", "search": "catalog-items", "metadata": "catalog-items",
    "sections": "catalog-home", "home": "catalog-home", "calendar": "catalog-home",
    "recommendations": "catalog-recommendations",
    "progress": "personal-progress", "history": "personal-progress", "watched": "personal-progress",
    "watch": "personal-progress", "sync": "personal-progress",
    "favorites": "personal-lists", "watchlist": "personal-lists", "ratings": "personal-lists",
    "collections": "personal-collections",
    "requests": "personal-requests", "watch-providers": "personal-requests",
    "imports": "personal-imports", "history-imports": "personal-imports",
    "plex-sync": "personal-imports", "webhook-sync": "personal-imports",
    "notifications": "notifications", "events": "realtime", "watch-together": "realtime",
    "playback": "playback-control", "markers": "playback-control",
    "subtitles": "playback-subtitles", "stream": "playback-delivery",
    "direct-download": "playback-delivery", "direct-download-proxy": "playback-delivery",
    "transcode": "playback-delivery", "downloads": "downloads",
    "ebooks": "ebooks", "images": "raw-assets",
    "compat": "operational", "autoscan": "operational", "scan": "operational",
    "diagnostics": "operational", "health": "operational", "ready": "operational",
    "plugins": "plugin-proxy", "plugin-assets": "plugin-proxy",
    # Bloem additions on the Silo v1 surface.
    "ambience": "ambience", "promotions": "promotions",
}

# ---------------------------------------------------------------------------
# Bloem-native surfaces. /api/bloem/v1 and /api/internal/compat/v1 are Bloem's
# own namespaces, not part of Silo's frozen v1 bridge, so they get their own
# delivery units rather than being swept into "operational" by the fallback
# below. Sections stay under the 40-row review cap enforced by
# internal/contractledger TestEverySectionIsAssignedAndNonEmpty, which is why
# the platform entitlement surface is split by subject (organization vs
# account) and by bulk-policy machinery rather than kept as one unit.
# ---------------------------------------------------------------------------

# /api/bloem/v1/admin/platform/<subject>/... -> section, matched on the first
# non-parameter segment after <subject>.
BLOEM_PLATFORM = {
    "organizations": {
        "entitlement-bulk": "bloem-platform-org-bulk",
        "entitlement-cohorts": "bloem-platform-org-entitlement",
        "entitlement-snapshots": "bloem-platform-org-entitlement",
        "entitlement": "bloem-platform-org-entitlement",
        None: "bloem-platform-organizations",
    },
    "accounts": {
        "entitlement-bulk": "bloem-platform-account-bulk",
        None: "bloem-platform-accounts",
    },
    "entitlement-templates": {None: "bloem-platform-templates"},
    "users": {None: "bloem-platform-templates"},
    "compatibility": {None: "bloem-platform-compatibility"},
}

# /api/bloem/v1/<a>[/admin/<b>] -> section
BLOEM_ADMIN = {"organization": "bloem-admin-organization"}
BLOEM_TOP = {
    "livetv": "bloem-livetv",
    "music": "bloem-native", "notifications": "bloem-native", "watch": "bloem-native",
    "organizations": "bloem-native", "capabilities": "bloem-native", "persons": "bloem-native",
    "server": "bloem-native", "sync": "bloem-native",
}

# /api/internal/compat/v1/<a> -> section
COMPAT = {
    "state": "compat-state", "livetv": "compat-livetv",
    "catalog": "compat-core", "identity": "compat-core", "playback": "compat-core",
    "credentials": "compat-core", "enroll": "compat-core", "events": "compat-core",
    "health": "compat-core",
}


def _first_static(parts):
    return next((s for s in parts if not s.startswith("{")), None)


def bloem_section(parts, entry):
    """Section for a /api/bloem/v1/... path, given the segments after v1."""
    if not parts:
        return "bloem-native"
    if parts[0] == "admin":
        rest = parts[1:]
        if not rest or rest[0] in ("*", "session"):
            return "bloem-admin-core"
        if rest[0] == "platform":
            subject = rest[1] if len(rest) > 1 else None
            table = BLOEM_PLATFORM.get(subject)
            if table is None:
                raise SystemExit(f"unmapped bloem platform route: {entry['method']} {entry['path']}")
            return table.get(_first_static(rest[2:]), table[None])
        if rest[0] in BLOEM_ADMIN:
            return BLOEM_ADMIN[rest[0]]
        raise SystemExit(f"unmapped bloem admin route: {entry['method']} {entry['path']}")
    if parts[0] in BLOEM_TOP:
        return BLOEM_TOP[parts[0]]
    raise SystemExit(f"unmapped bloem route: {entry['method']} {entry['path']}")


def section_for(entry):
    listener = entry["listener"]
    if listener in NODE_SECTIONS:
        return NODE_SECTIONS[listener]
    if listener == "root":
        return "root-operational"
    if entry["namespace"] == "api_v2":
        return "v2-delegation"
    parts = [s for s in entry["path"].split("/") if s]
    if parts[:3] == ["api", "bloem", "v1"]:
        return bloem_section(parts[3:], entry)
    if parts[:4] == ["api", "internal", "compat", "v1"]:
        a = parts[4] if len(parts) > 4 else None
        if a in COMPAT:
            return COMPAT[a]
        raise SystemExit(f"unmapped compat route: {entry['method']} {entry['path']}")
    if len(parts) < 3 or parts[0] != "api" or parts[1] != "v1":
        return "operational"
    a = parts[2]
    if a == "admin":
        if len(parts) == 3:
            return "admin-settings"
        b = parts[3]
        if b in ADMIN:
            return ADMIN[b]
        raise SystemExit(f"unmapped admin route: {entry['method']} {entry['path']}")
    if a in TOP:
        return TOP[a]
    raise SystemExit(f"unmapped route: {entry['method']} {entry['path']}")


def main(argv):
    check = "--check" in argv
    args = [a for a in argv[1:] if not a.startswith("--")]
    path = args[0] if args else LEDGER
    with open(path, encoding="utf-8") as f:
        raw = f.read()
    doc = json.loads(raw, object_pairs_hook=OrderedDict)
    changed = 0
    for entry in doc["entries"]:
        want = section_for(entry)
        if entry.get("section") != want:
            changed += 1
        # Keep the field next to the other curated scheduling fields.
        items = list(entry.items())
        items = [(k, v) for k, v in items if k != "section"]
        idx = next(i for i, (k, _) in enumerate(items) if k == "release_flow")
        items.insert(idx, ("section", want))
        entry.clear()
        entry.update(items)
    out = json.dumps(doc, indent=2, ensure_ascii=False) + "\n"
    if check:
        if out != raw:
            print(f"{path}: {changed} entries would change; run assign_sections.py", file=sys.stderr)
            return 1
        print(f"{path}: sections current")
        return 0
    with open(path, "w", encoding="utf-8") as f:
        f.write(out)
    print(f"{path}: {changed} entries updated")
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv))
