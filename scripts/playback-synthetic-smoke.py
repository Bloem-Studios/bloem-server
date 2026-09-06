#!/usr/bin/env python3
"""Exercise a running disposable playback harness without printing credentials."""
import argparse
import json
import pathlib
import time
import urllib.error
import urllib.parse
import urllib.request
import uuid


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("bootstrap", type=pathlib.Path)
    args = parser.parse_args()
    bootstrap = json.loads(args.bootstrap.read_text())
    origin = bootstrap["url"].rstrip("/")
    headers = {"Content-Type": "application/json", "Accept": "application/json"}

    def call(method, path, body=None):
        url = urllib.parse.urljoin(origin + "/", path)
        if urllib.parse.urlsplit(url).netloc != urllib.parse.urlsplit(origin).netloc:
            raise RuntimeError("Server returned a media URL outside this harness")
        encoded = None if body is None else json.dumps(body, separators=(",", ":")).encode()
        request = urllib.request.Request(url, data=encoded, headers=headers, method=method)
        try:
            response = urllib.request.urlopen(request, timeout=20)
        except urllib.error.HTTPError as error:
            response = error
        with response:
            data = response.read()
            if "json" in response.headers.get("Content-Type", ""):
                data = json.loads(data)
            return response.status, data

    def require(status, expected, label):
        if status != expected:
            raise RuntimeError(f"{label}: HTTP {status}, expected {expected}")

    status, setup = call("GET", "/api/v2/system/setup")
    require(status, 200, "setup state")
    assert setup["needs_setup"] is False
    status, login = call("POST", "/api/v2/auth/login", {
        "username": bootstrap["username"], "password": bootstrap["password"]})
    require(status, 200, "password login")
    headers["Authorization"] = "Bearer " + login["access_token"]
    headers["X-Profile-Id"] = bootstrap["profile_id"]
    status, capability = call("GET", "/api/v2/playback/capabilities")
    require(status, 200, "capability")
    installation = bootstrap["installation_id"]
    assert capability["allowed"] and capability["installation_id"] == installation
    assert capability["deliveries"] == ["original_http"]
    request = {
        "installation_id": installation, "protocol_version": 3,
        "client_features": ["playback_plan_v3"], "file_id": str(bootstrap["file_id"]),
        "profile_id": bootstrap["profile_id"], "playback_attempt_id": str(uuid.uuid4()),
        "quality_preference": "original", "metered": False,
        "subtitle_fidelity_preference": "compatible",
        "client_capabilities": {
            "video_evidence": "exact", "audio_evidence": "exact", "hdr": False,
            "codecs_video": ["h264"], "codecs_video_hardware": ["h264"],
            "codecs_audio": ["aac"], "containers": ["mp4"], "max_resolution": "1080p",
            "video_decode": [{"codec": "h264", "profiles": ["high"], "levels": [41],
                              "bit_depths": [8], "max_width": 1920, "max_height": 1080,
                              "max_frame_rate": 60, "max_bitrate_kbps": 20000, "hardware": True}]},
        "client_playback_context": {
            "protocol_version": 3, "form_factor": "tv", "app_version": "synthetic-smoke",
            "device": {"platform": "android"}, "output": {"output_context_id": "synthetic-output"},
            "deliveries": {"original_http": {
                "enabled": True, "supported_on_device": True, "containers": ["mp4"],
                "video_codecs": ["h264"], "audio_decode_codecs": ["aac"],
                "audio_passthrough_codecs": [], "features": [], "validated_claims": [],
                "transformations": [], "auth_header_refresh": False,
                "subtitles": {"embedded_text": True, "sidecar_text": True,
                              "ass_styling": False, "embedded_bitmap": False,
                              "font_attachments": False, "sidecar_bitmap": False}}}}}
    status, decision = call("POST", "/api/v2/playback/start", request)
    if status != 201:
        raise RuntimeError(f"start: HTTP {status}: {decision.get('detail', '')} {decision.get('errors', [])}")
    session_path = "/api/v2/playback/" + decision["session_id"]
    stop = {"installation_id": installation, "stop_id": str(uuid.uuid4()),
            "sequence": 2, "position": 1.5, "is_paused": False}
    try:
        status, replay = call("POST", "/api/v2/playback/start", request)
        require(status, 201, "start replay")
        assert replay["session_id"] == decision["session_id"]
        plan = decision["playback_plan"]
        assert plan["requested_media_file_id"] == str(bootstrap["file_id"])
        status, media = call("GET", plan["stream"]["url"])
        require(status, 200, "media")
        assert media == pathlib.Path(bootstrap["media_path"]).read_bytes()
        progress = {"installation_id": installation, "sequence": 1,
                    "position": 1.0, "is_paused": False}
        status, receipt = call("POST", session_path + "/progress", progress)
        require(status, 200, "progress")
        assert receipt["accepted"]["sequence"] == 1
        status, _ = call("POST", session_path + "/progress",
                         dict(progress, installation_id=str(uuid.uuid4())))
        require(status, 409, "installation mismatch")
    finally:
        deadline = time.monotonic() + 10
        while True:
            status, receipt = call("DELETE", session_path, stop)
            if status != 202 or time.monotonic() >= deadline:
                break
            time.sleep(0.05)  # Poll observed draining state with a bounded deadline.
        require(status, 200, "stop")
        assert receipt["stop_id"] == stop["stop_id"]
        assert receipt["accepted"]["sequence"] == 2
    status, receipt = call("DELETE", session_path, stop)
    require(status, 200, "stop replay")
    assert receipt["stop_id"] == stop["stop_id"]
    print("PASS: password login, capability, exact start replay, media bytes, progress, installation fence, stop and stop replay")


if __name__ == "__main__":
    main()
