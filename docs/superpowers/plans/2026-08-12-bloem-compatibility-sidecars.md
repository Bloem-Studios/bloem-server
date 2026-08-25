# Bloem External Compatibility Applications Roadmap

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace Bloem Server's embedded Jellyfin and Audiobookshelf protocol implementations with two optional, private applications without moving canonical media or identity state out of Bloem.

**Architecture:** Bloem owns the public address, authentication, authorization, media state, playback, and Live TV. `bloem-audiobookshelf` and `bloem-jellyfin` translate external protocols through a private, versioned compatibility API and are reached only through fixed same-origin gateway routes.

**Tech Stack:** Go 1.26, PostgreSQL/pgx, chi, OpenAPI 3.1, WebSockets/Socket.IO, Docker Compose, React/TypeScript, private GitHub repositories and OCI images.

**Spec:** `docs/superpowers/specs/2026-08-12-bloem-compatibility-sidecars-design.md`

## Global Constraints

- All Bloem repositories and artifacts remain private indefinitely.
- The server is authoritative for credentials, policy, media state, delivery, and Live TV.
- Compatibility applications receive no Bloem database, Redis, filesystem, Docker socket, signing-key, provider, or tuner access.
- Public routing uses fixed paths on the canonical Bloem address; companions expose no host ports by default.
- Direct profile login requires both a globally unique email and password; shared-only profiles remain valid.
- Legacy account login and existing per-profile PIN switching remain compatible.
- One fresh compatibility login at cutover is acceptable; live tokens are not migrated.
- Use RED/GREEN TDD, task-scoped commits, independent review gates, and no persistent `replace` directives.

---

Execute these plans serially:

1. [`2026-08-13-bloem-compatibility-1-foundation.md`](2026-08-13-bloem-compatibility-1-foundation.md) — profile credentials, trust, private API, fixed-path gateway, deployment contracts.
2. [`2026-08-13-bloem-compatibility-2-audiobookshelf.md`](2026-08-13-bloem-compatibility-2-audiobookshelf.md) — extract and cut over the smaller `/audiobookshelf/**` surface.
3. [`2026-08-13-bloem-compatibility-3-jellyfin.md`](2026-08-13-bloem-compatibility-3-jellyfin.md) — extract Jellyfin identity, media, playback, sessions, Live TV, Web, and discovery.
4. [`2026-08-13-bloem-compatibility-4-cutover.md`](2026-08-13-bloem-compatibility-4-cutover.md) — remove embedded implementations, retain rollback data for one release, and complete operations/documentation gates.

Each plan must be green and independently reviewed before the next begins. The final removal plan may not start until both external applications pass the shared black-box contract against their exact private release images.
