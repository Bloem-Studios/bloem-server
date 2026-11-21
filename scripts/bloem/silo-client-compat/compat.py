#!/usr/bin/env python3
"""Differential check: a Silo client gets the same answers from Bloem as from Silo.

    python3 scripts/bloem/silo-client-compat/compat.py run [--silo-ref REF] [--keep] [--only PATH_PREFIX]
    python3 scripts/bloem/silo-client-compat/compat.py check-allowlist [--base REF]

Commands assume the repository root is the cwd and upstream/main is fetched.
`make silo-client-compat` runs `run`.

`run` builds Bloem from this tree and upstream Silo at SILO_REF (default: the
merge base of HEAD and upstream/main, the Silo commit Bloem last merged). It
starts each server on its own fresh PostgreSQL and Redis, seeds both with the
same invented data through the public API (initial setup, a movie and a
series library of tiny ffmpeg videos, a second account, a profile with a PIN
and one without), waits for the scans by polling library and job state, and
then replays every operation in contracts/client/silo-clients/calls.json, and
every other GET in Silo's OpenAPI as a third-party client might call it, as an
admin, an ordinary user and a PIN-locked profile (plus the refusals those apps
meet: no sign-in, no profile picked, a locked profile without its PIN proof,
another account's profile), against both servers in lockstep. Follow-ups
replay what the answers name: the playback plan's stream URL and the events
WebSocket with its ticket. Before the replay, notices.py publishes one Bloem
announcement on Bloem and checks that a Silo app's inbox, unread count and
events socket still match Silo's. compare.py decides what counts as a divergence. Every divergence not
in allowlist.json fails the run; the allowlist may only shrink.

Ports default to 18230 (Bloem), 18231 (Silo) and 18232-18235 (PostgreSQL and
Redis); override with SCC_PORT_BASE. Credentials are generated per run and
kept in a 0600 file in the work directory, which is removed afterwards unless
--keep is given.
"""

import argparse
import base64
import hashlib
import json
import os
import re
import secrets
import shutil
import signal
import socket
import subprocess
import sys
import threading
import time
import uuid
import urllib.error
import urllib.parse
import urllib.request
from pathlib import Path

HERE = Path(__file__).resolve().parent
ROOT = HERE.parents[2]
sys.path.insert(0, str(HERE))

import compare  # noqa: E402
import notices  # noqa: E402

CALLS = ROOT / "contracts" / "client" / "silo-clients" / "calls.json"
ALLOWLIST = HERE / "allowlist.json"
REQUESTS = HERE / "requests.json"
PG_IMAGE = "pgvector/pgvector:pg18"
REDIS_IMAGE = "redis:7-alpine"
# admin, user and pin are the three a Silo app signs in as: the server's
# admin on its primary profile, an ordinary account on its own profile, and a
# PIN-locked profile after verify-pin. The other four are the refusals those
# apps meet: no sign-in, an account that has not picked a profile yet, a
# locked profile without its token, and another account's profile.
PERSONAS = ("admin", "user", "pin", "anon", "account", "locked", "foreign")
LAST_FIRST = ("anon", "locked", "foreign", "account", "pin", "user", "admin")
DEVICE = {
    "X-Silo-Device-Id": "silo-client-compat",
    "X-Silo-Device-Name": "Compatibility check",
    "X-Silo-Device-Platform": "android",
    "X-Silo-Client": "silo-client-compat",
    "X-Silo-Client-Version": "1.0.0",
}
BODY_LIMIT = 256 * 1024
MISSING = "compat-missing"
DUMP = bool(os.environ.get("SCC_DUMP"))


def log(msg):
    print(f"[{time.strftime('%H:%M:%S')}] {msg}", flush=True)


# ---------------------------------------------------------------- processes and services

def go_env():
    env = dict(os.environ)
    gomod = (ROOT / "go.mod").read_text()
    env.update(GOWORK="off", GOTOOLCHAIN="go" + re.search(r"^go\s+(\S+)", gomod, re.M).group(1))
    return env


def sh(cmd, **kw):
    return subprocess.run(cmd, check=True, **kw)


def free_port():
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


# Binary names that say neither "silo" nor "bloem", so another tool's
# `pkill -f bin/silo` cleaning up its own servers cannot stop these.
BIN_UNDER_TEST, BIN_REFERENCE = "compat-under-test", "compat-reference"


def build(work, silo_ref, bloem_bin=None, silo_bin=None):
    env = go_env()
    out = {}
    if bloem_bin:
        out["bloem"] = Path(bloem_bin)
    else:
        log("building Bloem from this tree")
        sh(["make", "-s", "embed-stub"], cwd=ROOT)
        sh(["go", "build", "-o", str(work / "bin" / BIN_UNDER_TEST), "./cmd/silo"], cwd=ROOT, env=env)
        out["bloem"] = work / "bin" / BIN_UNDER_TEST
    if silo_bin:
        out["silo"] = Path(silo_bin)
    else:
        src = work / "silo-src"
        log(f"building Silo at {silo_ref[:9]}")
        sh(["git", "worktree", "add", "--detach", "--quiet", str(src), silo_ref], cwd=ROOT)
        try:
            sh(["make", "-s", "embed-stub"], cwd=src)
            senv = dict(env)
            senv["GOTOOLCHAIN"] = "go" + re.search(r"^go\s+(\S+)", (src / "go.mod").read_text(), re.M).group(1)
            sh(["go", "build", "-o", str(work / "bin" / BIN_REFERENCE), "./cmd/silo"], cwd=src, env=senv)
        finally:
            subprocess.run(["git", "worktree", "remove", "--force", str(src)], cwd=ROOT)
        out["silo"] = work / "bin" / BIN_REFERENCE
    return out


class Services:
    def __init__(self, sid, name, pg_port, redis_port, pg_password):
        self.pg = f"scc-{sid}-{name}-pg"
        self.redis = f"scc-{sid}-{name}-redis"
        self.pg_port, self.redis_port, self.pg_password = pg_port, redis_port, pg_password

    def start(self):
        sh(["docker", "run", "-d", "--rm", "--name", self.pg, "-e", "POSTGRES_USER=silo",
            "-e", f"POSTGRES_PASSWORD={self.pg_password}", "-e", "POSTGRES_DB=silo",
            "-p", f"127.0.0.1:{self.pg_port}:5432", "--shm-size=512m", PG_IMAGE,
            "-c", "max_connections=200", "-c", "max_locks_per_transaction=512",
            "-c", "fsync=off", "-c", "synchronous_commit=off", "-c", "full_page_writes=off"],
           stdout=subprocess.DEVNULL)
        sh(["docker", "run", "-d", "--rm", "--name", self.redis, "-p", f"127.0.0.1:{self.redis_port}:6379", REDIS_IMAGE],
           stdout=subprocess.DEVNULL)

    def wait(self):
        probe = ["docker", "exec", self.pg, "pg_isready", "-h", "127.0.0.1", "-U", "silo", "-d", "silo"]
        until(lambda: subprocess.run(probe, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode == 0,
              120, "PostgreSQL ready")

    def stop(self):
        for c in (self.pg, self.redis):
            subprocess.run(["docker", "rm", "-f", c], stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)

    @property
    def database_url(self):
        return f"postgres://silo:{self.pg_password}@127.0.0.1:{self.pg_port}/silo?sslmode=disable"

    @property
    def redis_url(self):
        return f"redis://127.0.0.1:{self.redis_port}/0"


def until(check, timeout, what, interval=1.0):
    deadline = time.monotonic() + timeout
    while True:
        result = check()
        if result:
            return result
        if time.monotonic() > deadline:
            raise RuntimeError(f"timed out after {timeout}s waiting for {what}")
        time.sleep(interval)


class Server:
    def __init__(self, name, binary, port, services, work, secret_key, media):
        self.name, self.binary, self.port, self.services = name, binary, port, services
        self.dir = work / name
        self.media = media
        self.secret_key = secret_key
        self.proc = None
        self.base = f"http://127.0.0.1:{port}"

    def start(self, append=False):
        for sub in ("transcode", "downloads", "plugins", "artwork", "tmp", "home"):
            (self.dir / sub).mkdir(parents=True, exist_ok=True)
        env = {
            "PATH": os.environ.get("PATH", "/usr/bin:/bin"),
            "HOME": str(self.dir / "home"), "TMPDIR": str(self.dir / "tmp"),
            "PORT": str(self.port), "JF_PORT": str(free_port()), "ABS_PORT": str(free_port()),
            "MODE": "integrated", "SECRET_KEY": self.secret_key,
            "DATABASE_URL": self.services.database_url, "REDIS_URL": self.services.redis_url,
            "SILO_TRANSCODE_DIR": str(self.dir / "transcode"),
            "SILO_DOWNLOAD_ARTIFACT_DIR": str(self.dir / "downloads"),
            "SILO_PLUGIN_CACHE_DIR": str(self.dir / "plugins"),
            "SILO_ARTWORK_PATH": str(self.dir / "artwork"),
            "SILO_PUBLIC_URL": self.base, "SILO_SERVER_NAME": "Compatibility check",
            "SILO_LAN_DISCOVERY": "false",
        }
        self.log_path = self.dir / "server.log"
        self.proc = subprocess.Popen([str(self.binary)], cwd=self.dir, env=env, stdout=self.log_path.open("a" if append else "w"),
                                     stderr=subprocess.STDOUT, start_new_session=True)

    def wait_ready(self):
        def ready():
            if self.proc.poll() is not None:
                raise RuntimeError(f"{self.name} exited with {self.proc.returncode}; log: {self.log_path}")
            try:
                with urllib.request.urlopen(self.base + "/api/v2/system/info", timeout=3) as r:
                    return r.status == 200
            except (urllib.error.URLError, ConnectionError, OSError):
                return False
        until(ready, 300, f"{self.name} to answer /api/v2/system/info")

    def stop(self):
        if self.proc and self.proc.poll() is None:
            os.killpg(self.proc.pid, signal.SIGTERM)
            try:
                self.proc.wait(30)
            except subprocess.TimeoutExpired:
                os.killpg(self.proc.pid, signal.SIGKILL)


def make_media(media):
    """A movie and a two-episode series, a few seconds each."""
    files = [
        media / "movies" / "Invented Harbour (2021)" / "Invented Harbour (2021).mp4",
        media / "shows" / "Invented Lighthouse" / "Season 01" / "Invented Lighthouse - S01E01.mkv",
        media / "shows" / "Invented Lighthouse" / "Season 01" / "Invented Lighthouse - S01E02.mkv",
    ]
    for i, f in enumerate(files):
        f.parent.mkdir(parents=True, exist_ok=True)
        sh(["ffmpeg", "-loglevel", "error", "-y", "-f", "lavfi", "-i", f"testsrc=duration=4:size=320x180:rate=24",
            "-f", "lavfi", "-i", f"sine=frequency={440 + 110 * i}:duration=4", "-c:v", "libx264", "-preset",
            "veryfast", "-pix_fmt", "yuv420p", "-profile:v", "high", "-c:a", "aac", "-shortest", "-metadata", "title=Invented", str(f)])
    return media / "movies", media / "shows"


# ---------------------------------------------------------------- HTTP

class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        return None


OPENER = urllib.request.build_opener(NoRedirect)


def http(base, method, path, query=None, body=None, headers=None, timeout=30):
    url = base + path
    if query:
        url += "?" + urllib.parse.urlencode(query, doseq=True)
    data = None
    hdrs = dict(headers or {})
    if body is not None:
        data = json.dumps(body).encode()
        hdrs.setdefault("Content-Type", "application/json")
    req = urllib.request.Request(url, data=data, method=method, headers=hdrs)
    started = time.monotonic()
    try:
        resp = OPENER.open(req, timeout=timeout)
        status, rh, stream = resp.status, resp.headers, resp
    except urllib.error.HTTPError as err:
        status, rh, stream = err.code, err.headers, err
    except (urllib.error.URLError, TimeoutError, ConnectionError, OSError) as err:
        return {"status": 0, "headers": {}, "json": None, "kind": "error", "error": str(err), "ms": 0}
    try:
        raw = stream.read(BODY_LIMIT + 1) if method != "HEAD" else b""
    except (TimeoutError, OSError):
        raw = b""
    finally:
        stream.close()
    headers_out = {k.lower(): v for k, v in rh.items()}
    out = {"status": status, "headers": headers_out, "json": None, "ms": int((time.monotonic() - started) * 1000)}
    mt = compare.media_type(headers_out)
    if not raw:
        out["kind"] = "empty"
    elif mt.endswith("json"):
        try:
            out["json"] = json.loads(raw)
            out["kind"] = "json"
        except ValueError:
            out["kind"] = "truncated" if len(raw) > BODY_LIMIT else "text"
    elif mt.startswith("text/"):
        out["kind"] = "text"
        out["text"] = raw[:200].decode(errors="replace")
    else:
        out["kind"] = "binary"
    return out


def websocket_first_frame(base, path, protocols, headers, timeout=5):
    """Open a WebSocket like a Silo app and read the server's first text frame."""
    return websocket_frames(base, path, protocols, headers, 1, timeout)


def websocket_frames(base, path, protocols, headers, count, timeout=5):
    """Open a WebSocket like a Silo app and read up to count frames.

    The answer is the first frame's, as websocket_first_frame returns it, with
    every frame read under "frames"."""
    host, port = urllib.parse.urlsplit(base).hostname, urllib.parse.urlsplit(base).port
    key = base64.b64encode(secrets.token_bytes(16)).decode()
    lines = [f"GET {path} HTTP/1.1", f"Host: {host}:{port}", "Upgrade: websocket", "Connection: Upgrade",
             f"Sec-WebSocket-Key: {key}", "Sec-WebSocket-Version: 13", f"Sec-WebSocket-Protocol: {', '.join(protocols)}"]
    lines += [f"{k}: {v}" for k, v in headers.items()]
    out = {"status": 0, "headers": {}, "json": None, "kind": "empty", "frames": []}
    try:
        with socket.create_connection((host, port), timeout=timeout) as sock:
            sock.sendall(("\r\n".join(lines) + "\r\n\r\n").encode())
            buf = b""
            while b"\r\n\r\n" not in buf:
                chunk = sock.recv(4096)
                if not chunk:
                    break
                buf += chunk
            head, _, rest = buf.partition(b"\r\n\r\n")
            status_line, *header_lines = head.decode(errors="replace").split("\r\n")
            out["status"] = int(status_line.split()[1]) if len(status_line.split()) > 1 else 0
            out["headers"] = {k.strip().lower(): v.strip() for k, _, v in (h.partition(":") for h in header_lines)}
            if out["status"] != 101:
                if rest and out["headers"].get("content-type", "").startswith("application"):
                    try:
                        out["json"], out["kind"] = json.loads(rest), "json"
                    except ValueError:
                        out["kind"] = "text"
                return out
            data = rest
            def need(n):
                nonlocal data
                while len(data) < n:
                    chunk = sock.recv(65536)
                    if not chunk:
                        raise ConnectionError("socket closed")
                    data += chunk
            while len(out["frames"]) < count:
                need(2)
                length = data[1] & 0x7F
                offset = 2
                if length == 126:
                    need(4)
                    length, offset = int.from_bytes(data[2:4], "big"), 4
                elif length == 127:
                    need(10)
                    length, offset = int.from_bytes(data[2:10], "big"), 10
                need(offset + length)
                opcode, payload = data[0] & 0x0F, data[offset:offset + length]
                data = data[offset + length:]
                try:
                    frame = {"frame_opcode": opcode, "json": json.loads(payload), "kind": "json"}
                except ValueError:
                    frame = {"frame_opcode": opcode, "json": None, "kind": "binary"}
                if not out["frames"]:
                    out.update(frame)
                out["frames"].append(frame)
    except (OSError, ValueError) as err:
        out.update(status=out["status"] or 0, kind=out["kind"] if out["frames"] else "error", error=str(err))
    return out


def notifications_snapshot(session):
    """The events socket's notifications snapshot, as a Silo app subscribes to it: its row count."""
    ticket = session.call("POST", "/api/v2/events/ws-ticket")
    value = (ticket.get("json") or {}).get("ticket")
    if not value:
        return f"ticket status {ticket['status']}"
    got = websocket_frames(session.server.base, "/api/v2/events/ws?channels=notifications",
                           ["silo.events.v2", f"silo.ticket.{value}"], dict(DEVICE), 4)
    for frame in got["frames"]:
        body = frame.get("json") or {}
        if body.get("type") == "snapshot" and body.get("channel") == "notifications":
            return len(body.get("data") or [])
    return f"no snapshot (status {got['status']}, {got.get('error') or len(got['frames'])} frames)"


class Session:
    def __init__(self, server, access=None, profile_id=None, profile_token=None):
        self.server = server
        self.access, self.profile_id, self.profile_token = access, profile_id, profile_token

    def headers(self, extra=None):
        h = dict(DEVICE)
        if self.access:
            h["Authorization"] = f"Bearer {self.access}"
        if self.profile_id:
            h["X-Profile-Id"] = self.profile_id
        if self.profile_token:
            h["X-Profile-Token"] = self.profile_token
        h.update(extra or {})
        return h

    def call(self, method, path, query=None, body=None, headers=None, expect=None):
        resp = http(self.server.base, method, path, query, body, self.headers(headers))
        if expect and resp["status"] not in expect:
            detail = resp.get("json") or resp.get("text") or resp.get("error")
            raise RuntimeError(f"{self.server.name}: {method} {path} answered {resp['status']}: {json.dumps(detail)[:400]}")
        return resp


# ---------------------------------------------------------------- seeding

def login(server, username, password):
    s = Session(server)
    r = s.call("POST", "/api/v2/auth/login", body={"username": username, "password": password}, expect=(200,))
    s.access = r["json"]["access_token"]
    return s


def items_of(resp):
    body = resp.get("json") or {}
    if isinstance(body, list):
        return body
    for key in ("items", "data", "results", "libraries", "profiles"):
        if isinstance(body.get(key), list):
            return body[key]
    return []


def seed(server, creds, movies_dir, shows_dir):
    """The same invented data on one server through Silo's public API; returns its ids."""
    ctx = {"user_password": creds["user"], "admin_password": creds["admin"], "pin": creds["pin"]}
    anon = Session(server)
    anon.call("POST", "/api/v2/auth/setup", body={
        "username": "compat-admin", "email": "compat-admin@example.com", "password": creds["admin"],
        "create_default_profile": True, "default_profile_name": "Main",
    }, expect=(200, 201))
    admin = login(server, "compat-admin", creds["admin"])
    profiles = items_of(admin.call("GET", "/api/v2/profiles", expect=(200,)))
    admin.profile_id = profiles[0]["id"]
    ctx["admin_profile"] = admin.profile_id
    me = admin.call("GET", "/api/v2/account/me", expect=(200,))["json"]
    ctx["admin_account"] = str(me.get("id"))
    # The replay sends hundreds of writes in seconds, which request rate
    # limiting would refuse at moments that depend on timing. An operator turns
    # it off in the admin rate-limit settings; run() restarts both servers.
    current = admin.call("GET", "/api/v2/admin/rate-limits/config", expect=(200,))
    admin.call("PATCH", "/api/v2/admin/rate-limits/config", body={"enabled": False},
               headers={"If-Match": current["headers"].get("etag", "*")}, expect=(200, 204))
    # The replay registers Silo-app push devices as the apps do. With mobile
    # push delivery off, as an operator can set it, a notification fanned out
    # to one ends at the server and never reaches a real push relay.
    current = admin.call("GET", "/api/v2/admin/settings", expect=(200,))
    admin.call("PUT", "/api/v2/admin/settings", body={"values": {
        "notifications.apple_push_delivery_enabled": "false", "notifications.android_push_delivery_enabled": "false",
    }}, headers={"If-Match": current["headers"].get("etag", "*")}, expect=(200,))
    for persona in PERSONAS:
        # Each persona's device gets its own invented APNs token (hex).
        ctx[f"push_apns_token:{persona}"] = hashlib.sha256(f"silo-client-compat:{persona}".encode()).hexdigest()

    for kind, name, path in (("movies", "Invented Movies", movies_dir), ("series", "Invented Shows", shows_dir)):
        r = admin.call("POST", "/api/v2/libraries", body={"name": name, "type": kind, "paths": [str(path)]},
                       expect=(200, 201))
        ctx[f"{kind}_library"] = str(r["json"]["id"])
    for kind in ("movies", "series"):
        admin.call("POST", "/api/v2/scan", body={"library_id": ctx[f"{kind}_library"]}, expect=(202,))

    def scanned():
        libs = items_of(admin.call("GET", "/api/v2/libraries", expect=(200,)))
        done = [lib for lib in libs if str(lib.get("id")) in (ctx["movies_library"], ctx["series_library"])
                and lib.get("last_scanned_at")]
        if len(done) < 2:
            return False
        found = {}
        for kind in ("movies", "series"):
            r = admin.call("GET", "/api/v2/catalog", query={"library_id": ctx[f"{kind}_library"], "limit": 10})
            got = items_of(r)
            if not got:
                return False
            found[kind] = got[0]
        return found
    found = until(scanned, 300, f"{server.name} scans to finish", 2)
    ctx["movie"] = str(found["movies"].get("id") or found["movies"].get("content_id"))
    ctx["series"] = str(found["series"].get("id") or found["series"].get("content_id"))

    def episodes():
        r = admin.call("GET", f"/api/v2/catalog/series/{ctx['series']}/seasons/1/episodes")
        got = items_of(r)
        return got if len(got) >= 2 else False
    eps = until(episodes, 180, f"{server.name} episodes", 2)
    ctx["episode"] = str(eps[0].get("id") or eps[0].get("content_id"))
    seasons = items_of(admin.call("GET", f"/api/v2/catalog/series/{ctx['series']}/seasons"))
    if seasons:
        ctx["season"] = str(seasons[0].get("id") or seasons[0].get("content_id") or "")
    watch = admin.call("GET", f"/api/v2/watch/{ctx['movie']}").get("json") or {}
    for key in ("files", "versions", "media_files"):
        if isinstance(watch.get(key), list) and watch[key]:
            ctx["movie_file"] = str(watch[key][0].get("id") or watch[key][0].get("file_id"))
            break
    wait_for_jobs(admin, server)

    user = admin.call("POST", "/api/v2/admin/users", body={
        "username": "compat-user", "email": "compat-user@example.com", "password": creds["user"], "role": "user",
        "create_default_profile": True, "default_profile_name": "Main",
        "library_ids": [ctx["movies_library"], ctx["series_library"]],
    }, expect=(200, 201))["json"]
    ctx["user_account"] = str(user["id"])
    ctx["admin_profile:admin"] = ctx["admin_profile"]
    admin.call("POST", "/api/v2/profiles", body={"name": "Guest"}, expect=(200, 201))
    kids = admin.call("POST", "/api/v2/profiles", body={"name": "Kids", "pin": creds["pin"]}, expect=(200, 201))["json"]
    ctx["pin_profile"] = str(kids["id"])

    user_s = login(server, "compat-user", creds["user"])
    user_s.profile_id = items_of(user_s.call("GET", "/api/v2/profiles", expect=(200,)))[0]["id"]
    ctx["user_profile"] = user_s.profile_id
    pin_s = login(server, "compat-admin", creds["admin"])
    verify = pin_s.call("POST", f"/api/v2/profiles/{ctx['pin_profile']}/verify-pin", body={"pin": creds["pin"]},
                        expect=(200,))["json"]
    if not verify.get("valid"):
        raise RuntimeError(f"{server.name}: the seeded PIN was refused")
    pin_s.profile_id, pin_s.profile_token = ctx["pin_profile"], verify.get("profile_token")
    account = login(server, "compat-admin", creds["admin"])
    locked = Session(server, pin_s.access, ctx["pin_profile"])
    foreign = Session(server, user_s.access, ctx["admin_profile"])
    for name, profile in (("anon", "admin_profile"), ("account", "admin_profile"), ("locked", "pin_profile"),
                          ("foreign", "admin_profile")):
        ctx[f"{name}_profile"] = ctx[profile]
    return ctx, {"admin": admin, "user": user_s, "pin": pin_s, "anon": Session(server), "account": account,
                 "locked": locked, "foreign": foreign}


def wait_for_jobs(admin, server):
    """Background work the scan queued (probes, thumbnails) settles before the replay."""
    def idle():
        r = admin.call("GET", "/api/v2/admin/jobs", query={"limit": 100})
        if r["status"] != 200:
            return True
        return not any(not j.get("terminal", True) for j in items_of(r))
    try:
        until(idle, 120, f"{server.name} jobs to finish", 2)
    except RuntimeError as err:
        log(f"warning: {err}; replaying anyway")


# ---------------------------------------------------------------- replay

class Spec:
    """Silo's OpenAPI at SILO_REF: which parameters and body members an operation requires."""

    def __init__(self, doc):
        self.doc = doc

    def resolve(self, node):
        seen = 0
        while isinstance(node, dict) and "$ref" in node and seen < 20:
            parts = node["$ref"].lstrip("#/").split("/")
            node = self.doc
            for p in parts:
                node = node[p]
            seen += 1
        return node

    def op(self, method, path):
        return self.doc.get("paths", {}).get(path, {}).get(method.lower())

    def params(self, method, path):
        op = self.op(method, path) or {}
        out = []
        for p in op.get("parameters", []):
            out.append(self.resolve(p))
        return out

    def body(self, method, path, values):
        op = self.op(method, path)
        if not op or "requestBody" not in op:
            return None
        content = self.resolve(op["requestBody"]).get("content", {})
        if "application/json" not in content:
            return None
        return self.sample(content["application/json"].get("schema", {}), values, "", 0)

    def sample(self, schema, values, name, depth):
        schema = self.resolve(schema)
        if depth > 6:
            return None
        for key in ("oneOf", "anyOf"):
            if key in schema:
                return self.sample(schema[key][0], values, name, depth + 1)
        if "allOf" in schema:
            merged = {"type": "object", "properties": {}, "required": []}
            for part in schema["allOf"]:
                part = self.resolve(part)
                merged["properties"].update(part.get("properties", {}))
                merged["required"] += part.get("required", [])
            schema = merged
        if name in values:
            return values[name]
        if "enum" in schema:
            return schema["enum"][0]
        if "examples" in schema and schema["examples"] and name not in ("password",):
            ex = schema["examples"][0]
            if not isinstance(ex, (dict, list)):
                return ex
        t = schema.get("type")
        if isinstance(t, list):
            t = next((x for x in t if x != "null"), "string")
        if t == "object" or "properties" in schema:
            out = {}
            for prop in schema.get("required", []):
                out[prop] = self.sample(schema.get("properties", {}).get(prop, {}), values, prop, depth + 1)
            return out
        if t == "array":
            n = schema.get("minItems", 0)
            return [self.sample(schema.get("items", {}), values, name, depth + 1) for _ in range(n)]
        if t == "integer" or t == "number":
            return max(schema.get("minimum", 1), 1) if "maximum" not in schema else schema.get("minimum", 0)
        if t == "boolean":
            return False
        fmt = schema.get("format")
        if fmt == "email":
            return "compat@example.com"
        if fmt in ("uri", "url"):
            return "https://example.com/compat"
        if fmt == "date-time":
            return "2026-01-01T00:00:00Z"
        if fmt == "date":
            return "2026-01-01"
        return "compat" + ("x" * max(0, schema.get("minLength", 0) - 6))


ID_NAMES = {
    "item_id": "movie", "content_id": "movie", "owner_id": "movie", "series_id": "series", "season_id": "season",
    "episode_id": "episode", "library_id": "movies_library", "media_file_id": "movie_file", "file_id": "movie_file",
    "user_id": "user_account", "profile_id": "@profile", "session_id": "playback_session", "shuffle_id": "shuffle",
    "room_id": "room", "collection_id": "collection",
}
ROUTE_IDS = [
    (r"/profiles/\{id\}", "@profile"),
    (r"/admin/users/\{(?:id|user_id)\}", "user_account"),
    (r"/users/\{(?:id|user_id)\}", "user_account"),
    (r"/librar(?:y|ies)(?:-playback-prefs)?/\{(?:id|library_id)\}", "movies_library"),
    (r"/catalog/series/\{id\}", "series"),
    (r"/seasons/\{num\}", "=1"),
    (r"/(?:catalog/items|watch|watched|ratings|watchlist|favorites|item-posters|recommendations/similar)/\{(?:id|content_id|item_id)\}", "movie"),
    (r"/(?:subtitle|audio)-prefs/\{(?:id|series_id)\}", "series"),
    (r"/subtitles/\{(?:media_file_id|file|file_id)\}", "movie_file"),
    (r"/collections/\{id\}", "collection"),
    (r"/settings/values/\{key\}", "=playback.buffering_quality_mode"),
    (r"/home/dismissals/\{surface\}", "=continue_watching"),
]


def lookup(ctx, key, persona):
    """A seeded or captured value: the persona's own first, then the shared one."""
    if key == "profile":
        return ctx.get(f"{persona}_profile")
    return ctx.get(f"{key}:{persona}") or ctx.get(key)


def resolve_path(template, ctx, persona):
    def value(key):
        if key.startswith("="):
            return key[1:]
        if key == "@profile":
            return lookup(ctx, "profile", persona) or MISSING
        return lookup(ctx, key, persona) or MISSING
    out = template
    for rx, key in ROUTE_IDS:
        m = re.search(rx, out)
        if m:
            param = re.search(r"\{[^}]+\}", out[m.start():]).group(0)
            out = out[:m.start()] + out[m.start():].replace(param, urllib.parse.quote(value(key), safe=""), 1)
    for name in re.findall(r"\{([^}]+)\}", out):
        key = ID_NAMES.get(name, "")
        out = out.replace("{" + name + "}", urllib.parse.quote(value(key) if key else f"{MISSING}-{name}", safe=""), 1)
    return out


def plan_request(op, spec, ctx, persona, overrides):
    """The request both servers get for one operation and persona."""
    key = f"{op['method']} {op['path']}"
    over = overrides.get(key, {})
    values = {
        "library_id": ctx.get("movies_library"), "content_id": ctx.get("movie"), "item_id": ctx.get("movie"),
        "series_id": ctx.get("series"), "media_file_id": ctx.get("movie_file"), "file_id": ctx.get("movie_file"),
        "profile_id": lookup(ctx, "profile", persona),
        "user_id": ctx.get("user_account"), "pin": "0000", "password": "compat-wrong-password-1",
        "username": "compat-nobody", "email": "compat-nobody@example.com", "name": "Compat", "q": "Invented",
        "query": "Invented", "limit": 5,
    }
    query = {}
    for p in spec.params(op["method"], op["path"]):
        if p.get("in") != "query":
            continue
        name = p.get("name")
        if p.get("required") or name == "limit" or name in over.get("query_names", []):
            sch = spec.resolve(p.get("schema", {}))
            v = values.get(name)
            if v is None:
                v = spec.sample(sch, values, name, 0)
            if isinstance(v, bool):
                v = str(v).lower()
            if isinstance(v, list):
                v = ",".join(str(x) for x in v)
            query[name] = v
    query.update({k: fill(v, ctx, persona) for k, v in over.get("query", {}).items()})
    body = over.get("body")
    if body is not None:
        body = fill(body, ctx, persona)
    elif op["method"] in ("POST", "PUT", "PATCH"):
        body = spec.body(op["method"], op["path"], values)
    headers = {k: fill(v, ctx, persona) for k, v in over.get("headers", {}).items()}
    return {"method": op["method"], "path": resolve_path(op["path"], ctx, persona), "query": query, "body": body,
            "headers": headers}


def fill(value, ctx, persona):
    if isinstance(value, str):
        whole = re.fullmatch(r"\$\{([a-z_]+)\}", value)
        if whole and whole.group(1) == "persona":
            return persona
        if whole and whole.group(1) == "persona_uuid":
            return str(uuid.uuid5(uuid.NAMESPACE_URL, "silo-client-compat:" + persona))
        if whole:
            return lookup(ctx, whole.group(1), persona) or MISSING
        return re.sub(r"\$\{([a-z_]+)\}", lambda m: persona if m.group(1) == "persona"
                      else str(lookup(ctx, m.group(1), persona) or MISSING), value)
    if isinstance(value, dict):
        return {k: fill(v, ctx, persona) for k, v in value.items()}
    if isinstance(value, list):
        return [fill(v, ctx, persona) for v in value]
    return value


DESTRUCTIVE = re.compile(
    r"^(DELETE |POST /api/v2/auth/logout|POST /api/v2/auth/sessions/revoke|POST /api/v2/account/password"
    r"|POST /api/v2/auth/refresh|POST /api/v2/admin/users/\{[^}]+\}/(?:disable|reset))"
)


def phase_of(op, overrides):
    key = f"{op['method']} {op['path']}"
    if "phase" in overrides.get(key, {}):
        return overrides[key]["phase"]
    if op["method"] in ("GET", "HEAD"):
        return 0
    return 2 if DESTRUCTIVE.match(key) else 1


def replay(ops, spec, servers, sessions, ctxs, overrides, decoded, allow, only=None):
    results = []
    todo = []
    for op in ops:
        key = f"{op['method']} {op['path']}"
        if only and not op["path"].startswith(only):
            continue
        over = overrides.get(key, {})
        if over.get("skip"):
            results.append({"op": key, "persona": "*", "skipped": over["skip"]})
            continue
        todo.append(op)
    todo.sort(key=lambda o: (phase_of(o, overrides), o["path"], o["method"]))
    order = {0: PERSONAS, 1: PERSONAS, 2: LAST_FIRST, 3: LAST_FIRST}
    for phase in (0, 1, 2, 3):
        for persona in order[phase]:
            for op in (o for o in todo if phase_of(o, overrides) == phase):
                key = f"{op['method']} {op['path']}"
                answers = {}
                for name in ("silo", "bloem"):
                    req = plan_request(op, spec, ctxs[name], persona, overrides)
                    answers[name] = sessions[name][persona].call(req["method"], req["path"], req["query"],
                                                                 req["body"], req["headers"])
                    answers[name]["request"] = req
                    capture(overrides.get(key, {}).get("capture", {}), answers[name], ctxs[name], persona)
                check_alive(servers)
                diffs = compare.compare(op["method"], op["path"], answers["silo"], answers["bloem"],
                                        decoded.get(key))
                if answers["silo"]["status"] == 0 or answers["bloem"]["status"] == 0:
                    diffs.insert(0, compare.Diff("error", "", f"Silo {answers['silo'].get('error', 'ok')}; "
                                                 f"Bloem {answers['bloem'].get('error', 'ok')}", True))
                entry = {"op": key, "persona": persona, "silo_status": answers["silo"]["status"],
                         "bloem_status": answers["bloem"]["status"], "diffs": []}
                for d in diffs:
                    allowed = allow.match(key, persona, d) if d.failing else None
                    entry["diffs"].append({**d._asdict(), "allowed": bool(allowed),
                                           "reason": allowed.get("reason") if allowed else None})
                if DUMP or any(d["failing"] and not d["allowed"] for d in entry["diffs"]):
                    entry["silo_body"] = excerpt(answers["silo"])
                    entry["bloem_body"] = excerpt(answers["bloem"])
                    entry["request"] = redact({k: v for k, v in answers["silo"]["request"].items() if k != "headers"},
                                              secrets_of(ctxs["silo"]))
                results.append(entry)
                for follow in overrides.get(key, {}).get("followups", []):
                    results.append(followup(follow, sessions, ctxs, persona, allow))
    return results


def check_alive(servers):
    """A server that exits mid-replay is a broken run, not hundreds of divergences."""
    for s in servers.values():
        if s.proc and s.proc.poll() is not None:
            raise RuntimeError(f"{s.name} exited during the replay (code {s.proc.returncode}); its log: {s.log_path}")


def followup(follow, sessions, ctxs, persona, allow):
    """A request built from an earlier answer, such as the stream URL a playback plan names."""
    key = follow["op"]
    answers = {}
    for name in ("silo", "bloem"):
        url = fill(follow["url"], ctxs[name], persona)
        path, _, query = url.partition("?")
        if url == MISSING or not path.startswith("/api/"):
            answers[name] = {"status": -1, "headers": {}, "json": None, "kind": "none"}
            continue
        s = sessions[name][persona]
        hdrs = s.headers(follow.get("headers")) if follow.get("auth", True) else dict(DEVICE, **follow.get("headers", {}))
        target = path + ("?" + query if query else "")
        if "websocket" in follow:
            protocols = [fill(p, ctxs[name], persona) for p in follow["websocket"]]
            if any(MISSING in p for p in protocols):
                answers[name] = {"status": -1, "headers": {}, "json": None, "kind": "none"}
                continue
            answers[name] = websocket_first_frame(s.server.base, target, protocols, dict(DEVICE))
        else:
            answers[name] = http(s.server.base, follow.get("method", "GET"), target, None, None, hdrs)
    method, _, template = key.partition(" ")
    diffs = compare.compare(method, template, answers["silo"], answers["bloem"])
    entry = {"op": key, "persona": persona, "silo_status": answers["silo"]["status"],
             "bloem_status": answers["bloem"]["status"], "diffs": []}
    for d in diffs:
        allowed = allow.match(key, persona, d) if d.failing else None
        entry["diffs"].append({**d._asdict(), "allowed": bool(allowed), "reason": allowed.get("reason") if allowed else None})
    return entry


def capture(rules, resp, ctx, persona):
    """Remember ids one server answered, for the requests that follow on that server."""
    body = resp.get("json")
    for ctx_key, path in rules.items():
        value = body
        for part in path.split("."):
            if isinstance(value, list):
                value = value[0] if value else None
            value = value.get(part) if isinstance(value, dict) else None
        if value not in (None, ""):
            ctx[f"{ctx_key}:{persona}"] = str(value)
            ctx.setdefault(ctx_key, str(value))


def secrets_of(ctx):
    return [v for k, v in ctx.items() if k in ("user_password", "admin_password", "pin") and v]


def redact(value, hidden):
    text = json.dumps(value)
    for h in hidden:
        text = text.replace(h, "<redacted>")
    return json.loads(text)


def excerpt(resp):
    if resp.get("json") is not None:
        return json.dumps(resp["json"])[:None if DUMP else 600]
    return resp.get("text") or resp.get("kind")


# ---------------------------------------------------------------- report

def report(results, allow, out, full=True):
    failing = [r for r in results if any(d["failing"] and not d["allowed"] for d in r.get("diffs", []))]
    notes = [r for r in results if any(not d["failing"] for d in r.get("diffs", []))]
    replayed = [r for r in results if "skipped" not in r]
    by_op = {}
    for r in failing:
        by_op.setdefault(r["op"], []).append(r)
    lines = []
    for op, rows in sorted(by_op.items()):
        lines.append(f"DIVERGENCE {op}")
        seen = set()
        for r in rows:
            for d in r["diffs"]:
                if not d["failing"] or d["allowed"]:
                    continue
                sig = (d["kind"], d["field"], d["detail"])
                personas = sorted({x["persona"] for x in rows for y in x["diffs"]
                                   if (y["kind"], y["field"], y["detail"]) == sig})
                if sig in seen:
                    continue
                seen.add(sig)
                lines.append(f"  {d['kind']:<14} {d['field'] or '-':<40} {d['detail']}  [{', '.join(personas)}]")
    stale = allow.unused() if full else []
    for e in stale:
        lines.append(f"STALE ALLOWLIST ENTRY {e['op']} {e['kind']} {e.get('field', '*')}: the divergence is gone; remove it")
    summary = (f"silo-client-compat: {len(replayed)} replays, {len({r['op'] for r in replayed})} operations, "
               f"{len(by_op)} operations diverge, {len(notes)} replays with notes, "
               f"{len([r for r in results if 'skipped' in r])} skipped")
    out.write_text(json.dumps({"summary": summary, "results": results}, indent=1) + "\n")
    print("\n".join(lines))
    print(summary)
    return 1 if by_op or stale else 0


# ---------------------------------------------------------------- commands

def git(*args):
    return subprocess.run(["git", *args], cwd=ROOT, check=True, capture_output=True, text=True).stdout.strip()


def default_silo_ref():
    return git("merge-base", "HEAD", "upstream/main")


def run(args):
    started = time.monotonic()
    if check_allowlist(argparse.Namespace(base=args.allowlist_base)) != 0:
        return 1
    silo_ref = git("rev-parse", "--verify", (args.silo_ref or default_silo_ref()) + "^{commit}")
    base = int(os.environ.get("SCC_PORT_BASE", "18230"))
    work = Path(args.work or f"/var/tmp/scc-{time.strftime('%Y%m%dT%H%M%S')}-{os.getpid()}")
    work.mkdir(parents=True, exist_ok=True)
    os.chmod(work, 0o700)
    creds = {"admin": secrets.token_urlsafe(18), "user": secrets.token_urlsafe(18), "pin": f"{secrets.randbelow(9000) + 1000}",
             "pg": secrets.token_urlsafe(18), "secret": base64.b64encode(secrets.token_bytes(48)).decode()}
    cred_file = work / "credentials.json"
    cred_file.touch(mode=0o600)
    cred_file.write_text(json.dumps(creds))
    sid = f"{os.getpid()}"
    services = {"bloem": Services(sid, "bloem", base + 2, base + 3, creds["pg"]),
                "silo": Services(sid, "silo", base + 4, base + 5, creds["pg"])}
    servers = {}
    status = 1
    try:
        bins = build(work, silo_ref, args.bloem_bin, args.silo_bin)
        log(f"built in {int(time.monotonic() - started)}s; Silo {silo_ref[:9]}, Bloem {git('rev-parse', '--short=9', 'HEAD')}")
        movies, shows = make_media(work / "media")
        for s in services.values():
            s.start()
        for s in services.values():
            s.wait()
        servers = {"bloem": Server("bloem", bins["bloem"], base, services["bloem"], work, creds["secret"], work / "media"),
                   "silo": Server("silo", bins["silo"], base + 1, services["silo"], work, creds["secret"], work / "media")}
        for s in servers.values():
            s.start()
        for s in servers.values():
            s.wait_ready()
        log("both servers answer; seeding")
        ctxs, sessions = {}, {}
        errors = {}

        def seed_one(name):
            try:
                ctxs[name], sessions[name] = seed(servers[name], creds, movies, shows)
            except Exception as err:  # noqa: BLE001 - reported below
                errors[name] = err
        threads = [threading.Thread(target=seed_one, args=(n,)) for n in servers]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        if errors:
            for name, err in errors.items():
                print(f"SEED FAILED on {name}: {err}")
            if "silo" in errors:
                raise RuntimeError("Silo could not be seeded; the harness is broken, not Bloem")
            print("DIVERGENCE seeding: Bloem cannot be set up through Silo's public API the way Silo can")
            return 1
        for item in args.bloem_setting:
            key, _, value = item.partition("=")
            admin = sessions["bloem"]["admin"]
            current = admin.call("GET", f"/api/v2/admin/settings/{key}", expect=(200,))
            admin.call("PUT", f"/api/v2/admin/settings/{key}", body={"value": value},
                       headers={"If-Match": current["headers"].get("etag", "*")}, expect=(200,))
            log(f"Bloem only: {key} = {value}")
        for s in servers.values():
            s.stop()
        for s in servers.values():
            s.start(append=True)
        for s in servers.values():
            s.wait_ready()
        log(f"seeded and restarted in {int(time.monotonic() - started)}s; replaying")
        canary = {n: http(servers[n].base, "GET", "/api/bloem/v2/capabilities")["status"] for n in servers}
        if canary["bloem"] == canary["silo"]:
            print(f"CANARY FAILED: both servers answer GET /api/bloem/v2/capabilities with {canary['silo']}; "
                  "the Bloem build under test is not Bloem, or the Silo build is not Silo")
            return 1
        allow = compare.Allowlist(json.loads(ALLOWLIST.read_text()) if ALLOWLIST.exists() else [])
        notice_results = notices.check(http, servers, sessions, notifications_snapshot, allow)
        log("checked a Silo app's inbox with a Bloem notice present")
        calls = json.loads(CALLS.read_text())
        overrides = json.loads(REQUESTS.read_text()) if REQUESTS.exists() else {}
        spec_doc = json.loads(git("show", f"{silo_ref}:contracts/api/v2/openapi.json"))
        decoded = {f"{op['method']} {op['path']}": op.get("response", {}) for op in calls["operations"]}
        ops = list(calls["operations"])
        if not args.apps_only:
            known = {(op["method"], op["path"]) for op in ops}
            for path, methods in spec_doc.get("paths", {}).items():
                if "get" in methods and ("GET", path) not in known and not re.search(r"/(ws|stream|download)\b|/events$", path):
                    ops.append({"method": "GET", "path": path, "response": {}, "clients": {"openapi": []}})
        results = notice_results + replay(ops, Spec(spec_doc), servers, sessions, ctxs, overrides, decoded, allow, args.only)
        status = report(results, allow, work / "results.json", full=not (args.only or args.apps_only))
        log(f"done in {int(time.monotonic() - started)}s; details in {work / 'results.json'}")
        return status
    finally:
        for s in servers.values():
            s.stop()
        for s in services.values():
            s.stop()
        if status != 0:
            for s in servers.values():
                print(f"--- last {s.name} log lines ({s.log_path})")
                subprocess.run(["tail", "-n", "15", str(s.log_path)])
        if not args.keep:
            keep = work / "results.json"
            if keep.exists():
                shutil.copy(keep, Path("/var/tmp") / f"scc-results-{os.getpid()}.json")
            shutil.rmtree(work, ignore_errors=True)


def check_allowlist(args):
    entries = json.loads(ALLOWLIST.read_text())
    try:
        previous = json.loads(git("show", f"{args.base}:{ALLOWLIST.relative_to(ROOT)}"))
    except subprocess.CalledProcessError:
        previous = None
    problems = compare.validate_allowlist(entries, previous)
    for p in problems:
        print(f"allowlist: {p}")
    print(f"allowlist: {len(entries)} entries" + (f" (base has {len(previous)})" if previous is not None else ""))
    return 1 if problems else 0


def main(argv=None):
    parser = argparse.ArgumentParser(description=__doc__.split("\n\n")[0])
    sub = parser.add_subparsers(dest="command", required=True)
    r = sub.add_parser("run", help="build, seed and compare both servers")
    r.add_argument("--silo-ref", default=os.environ.get("SILO_REF"))
    r.add_argument("--bloem-bin", help="use this Bloem binary instead of building one")
    r.add_argument("--silo-bin", help="use this Silo binary instead of building one")
    r.add_argument("--work", help="work directory (default: a new one under /var/tmp)")
    r.add_argument("--keep", action="store_true", help="keep the work directory")
    r.add_argument("--only", help="replay only operations whose path starts with this")
    r.add_argument("--apps-only", action="store_true",
                   help="replay only what the apps call, not also every other GET in Silo's OpenAPI")
    r.add_argument("--bloem-setting", action="append", default=[], metavar="KEY=VALUE",
                   help="set an admin setting on Bloem only after setup, such as a Bloem feature Silo lacks")
    r.add_argument("--allowlist-base", default="origin/main", help="commit whose allowlist this one may not outgrow")
    c = sub.add_parser("check-allowlist", help="fail when the allowlist grew or an entry lacks a reason")
    c.add_argument("--base", default="origin/main")
    args = parser.parse_args(argv)
    if args.command == "run":
        return run(args)
    return check_allowlist(args)


if __name__ == "__main__":
    sys.exit(main())
