# Bloem deployment — November 5, 2026: AI subtitle cue hold

Production has run `main` at `a1315bd8ba336d5b11ae46deffb35d0fe758e043` since 21:52:07 UTC.
It replaces `a9f230203`, recorded in the receipt for
[Silo through `d941225b4`](2026-11-05-silo-sync-d941225b4-deployment.md). Since `a9f230203`
production gained:

- `5c1f2bff0`: that deployment's receipt.
- `5eecc34c4`: AI-generated subtitle cues stay on screen 0.6 s after their speech ends, never
  into the next cue or past the end of their audio chunk
  ([changelog](../bloem/CHANGELOG.md#november-5-2026--ai-subtitle-cues-stay-up-after-speech)).
- `a1315bd8b`: the changelog entry.

No migration, client DTO, fixture, catalogue or scenario pin changed. The seam ledger stays at
320 entries.

The same evening the speech server's voice detection was fixed outside this repository, so
whole-title transcription no longer drops stretches of dialogue. Bloem's settings did not
change: whole-title jobs still send 600-second chunks.

## Procedure and result

Jim asked for the push and the deploy. The deploy ran with `--skip-ci-check`, so it did not
wait for Bloem CI. Bloem CI run `42115650794` was still running when production restarted.

- **Image.** The deploy script built it locally from a `git archive` of the commit, tagged
  `bloem-server:release-a1315bd8b` (image `sha256:7d9b2af3a0af7ced1f000267d390d4e5bbcebd91bc24b6679b35d276490613b3`),
  and loaded it on CT135 and CT172. The binary `/usr/local/bin/silo` has SHA-256
  `2628b1ecee0280427d4abb02bdfc6dd30f14044008db648d971dd7a4c34546af` on both hosts.
- **Before the restart:** container snapshot `pre-main-a1315bd8b`. Database restore point of
  the same name at LSN `4A5/45BB3830`. Segment `00000003000004A500000045`, which holds it, was
  archived.
- **Restart.** The script required the live image to be `a9f230203`. One transcode process
  was running inside the server, and the stop took 38 seconds (exit 0). No migrations ran;
  765 are applied. The server answered ready and healthy at 21:52:07, 46 seconds after the
  stop began. Its runtime configuration fingerprint is the same as before.
- `/api/v2/system/info` reports `a1315bd8`; `/api/v1/ready` and
  `GET /api/bloem/v1/ambience/public` answer `200`. No error lines were logged after the start.
- **Transcode nodes.** Both `bloem-transcode` containers (CT135 and CT172) now run
  `release-a1315bd8b`, healthy, and the server lists both as enabled and healthy on
  `a1315bd8b`.
- **Speech-to-text.** The administrator check `ai_transcription` reports "Speech-to-text
  connection successful: timestamped speech received."
- **Old images.** The script removed `release-eeb919cb5` on both hosts and on the build
  machine. On the build machine `release-8065e5746` stayed because a container still uses it.

## Checks before the push

- `go test ./internal/subtitles/... ./internal/ai/...` passed, including the three new tests
  in `bloem_transcriber_test.go`.
- golangci-lint v2.12.2 built with Go 1.26.10, as Bloem CI runs it, reported no issues on
  `./internal/subtitles/ai/...` against `5c1f2bff0`.
- `scripts/verify-seams.sh` and `make verify-local-paths` passed.
- A transcription probe built from the branch ran Bloem's transcriber on 10-minute excerpts
  against the production speech server. Scored against human subtitles, cue ends within 0.5 s
  rose from 69% to 88% in English and from 34% to 96% in Dutch, with cue starts and wording
  unchanged.

## Rollback

`bloem-deploy --rollback --sha a1315bd8ba336d5b11ae46deffb35d0fe758e043 --previous a9f230203`
returns the server and both transcode nodes to the previous image. No migration needs undoing.
