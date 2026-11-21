"""A Bloem notice is invisible to a Silo app (compat.py runs this after seeding).

Bloem's own notices (system.announcement, system.alert) are rows a Silo server
never writes. compare.py compares shapes, so an extra row in a list or a
count of 1 instead of 0 is not a divergence there. This check publishes one
announcement to everyone on the Bloem server only, then reads, as each
signed-in Silo persona, what a Silo app reads on both servers: the inbox,
its unread filter, a sync, the unread count and the events socket's
notifications snapshot must hold the same number of rows on both, and the
announcement's ID must get Silo's answer for an ID it does not have.
"""

import json

import compare

PERSONAS = ("admin", "user", "pin")
NOTICE = {
    "type": "system.announcement",
    "body": {"title": "Maintenance tonight", "body": "The server restarts at ten.", "severity": "info",
             "dismissible": True},
    "targeting": {"audience": "all"},
}


def publish(http, server, admin):
    """Publish NOTICE on the Bloem server as its platform administrator."""
    ctx = http(server.base, "POST", "/api/bloem/v2/admin/session", body={"scope": "platform"},
               headers={"Authorization": f"Bearer {admin.access}"})
    token = (ctx.get("json") or {}).get("access_token")
    if ctx["status"] != 200 or not token:
        raise RuntimeError(f"bloem: the platform context answered {ctx['status']}; cannot publish a notice")
    r = http(server.base, "POST", "/api/bloem/v2/admin/platform/announcements", body=NOTICE,
             headers={"Authorization": f"Bearer {token}"})
    if r["status"] != 201 or not (r.get("json") or {}).get("recipient_count"):
        raise RuntimeError(f"bloem: publishing a notice answered {r['status']}: {json.dumps(r.get('json'))[:300]}")


def notice_ids(session):
    """The persona's notice deliveries, from Bloem's native inbox."""
    r = session.call("GET", "/api/bloem/v2/notifications", expect=(200,))
    return [i["id"] for i in (r.get("json") or {}).get("items", []) if i.get("type", "").startswith("system.")]


def view(session, snapshot, notice_id):
    """What a Silo app reads: row counts, and the answers for the notice's ID."""
    def rows(resp, key="items"):
        body = resp.get("json") or {}
        return len(body.get(key) or []) if resp["status"] == 200 else f"status {resp['status']}"

    listing = session.call("GET", "/api/v2/notifications")
    unread_listing = session.call("GET", "/api/v2/notifications", query={"status": "unread"})
    sync = session.call("GET", "/api/v2/notifications/sync")
    count = session.call("GET", "/api/v2/notifications/unread-count")
    out = {
        "GET /api/v2/notifications": rows(listing),
        "GET /api/v2/notifications?status=unread": rows(unread_listing),
        "GET /api/v2/notifications/sync": rows(sync),
        "GET /api/v2/notifications/sync unread_count": (sync.get("json") or {}).get("unread_count"),
        "GET /api/v2/notifications/unread-count": (count.get("json") or {}).get("count"),
        "events socket notifications snapshot": snapshot(session),
    }
    for method, path in (("GET", "/api/v2/notifications/{id}"), ("POST", "/api/v2/notifications/{id}/read"),
                         ("GET", "/api/v2/notifications/push/apple/display/{delivery_id}")):
        resp = session.call(method, path.replace("{id}", notice_id).replace("{delivery_id}", notice_id))
        out[f"{method} {path} for the notice"] = (resp["status"], compare.problem_type(resp))
    return out


def diffs(silo, bloem):
    """Every reading that differs is a divergence: Silo's value is the right one."""
    return [compare.Diff("bloem notice", reading, f"Silo {silo.get(reading)!r}, Bloem {bloem.get(reading)!r}", True)
            for reading in silo if silo.get(reading) != bloem.get(reading)]


def check(http, servers, sessions, snapshot, allow):
    """Publish a notice on Bloem and compare each persona's Silo-app view; returns report entries."""
    publish(http, servers["bloem"], sessions["bloem"]["admin"])
    results = []
    for persona in PERSONAS:
        ids = notice_ids(sessions["bloem"][persona])
        if not ids:
            raise RuntimeError(f"bloem: the {persona} persona's native inbox has no notice after publishing")
        views = {name: view(sessions[name][persona], snapshot, ids[0]) for name in ("silo", "bloem")}
        op = "NOTICE a Silo app's inbox with a Bloem notice present"
        entry = {"op": op, "persona": persona, "silo_status": 200, "bloem_status": 200, "diffs": [],
                 "silo": views["silo"], "bloem": views["bloem"]}
        for d in diffs(views["silo"], views["bloem"]):
            allowed = allow.match(op, persona, d)
            entry["diffs"].append({**d._asdict(), "allowed": bool(allowed),
                                   "reason": allowed.get("reason") if allowed else None})
        results.append(entry)
    return results
