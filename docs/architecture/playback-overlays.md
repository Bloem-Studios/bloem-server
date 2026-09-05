# Playback overlays

Garden authors campaigns through the existing admin promotions registry. Playback
campaigns use `surfaces: ["in_playback"]` and `placement.playback_style: "card"`
or `"pip"`. Optional `placement.video_url` is an HTTPS clip; `image_url` is always
required and is the fallback. The approved presentation floats artwork and softly shadowed text directly over the video,
with no enclosing panel. These are playback cards, not home hero decorations.
Existing server admin announcements remain independent and require no Garden access.

`GET /api/v1/notifications/capability` advertises `promotions.playback_overlay`.
`GET /api/v1/promotions?surface=in_playback&content_id=…` returns the existing card
projection with `playback_style`, `video_url`, and exclusive `expires_at`. The profile
must be known and adult; missing profile classification fails closed before campaign
lookup. Scheduling, organization membership and audience filters still apply.

`placement.duration_seconds` configures a 5–60 second lifecycle (default 10), and is
projected as `duration_seconds` on each card. The duration includes a 650 ms entrance
and one-second exit fade. No campaign can pause the content, take audio focus,
or force interaction. PiP creative is always muted; Android disables its audio track.
Clients skip initial loads, seeks, paused/resumed crossings and stale samples. Only
natural positive chapter crossings can trigger; one overlay per content item and profile
in the current application session. No chapters means no overlay. Controls, background
playback, stale delivery data and expired campaigns suppress the card. The card sits
above the ordinary bottom subtitle area; the web player suppresses it for ASS subtitles
whose positioned text may occupy arbitrary regions.

Right deliberately focuses a visible card; it never takes focus on arrival. Close,
retirement and suppression restore player focus when the card owned it. Ordinary
playback controls remain independent. Dismissal does not use the home dismissal table.

`POST /api/v1/promotions/{id}/save?content_id=…` re-evaluates the authenticated
profile's eligibility before saving that campaign to the same profile's inbox.
The server projects the stored CTA rather than trusting caller-supplied destinations.
A deterministic profile/campaign delivery ID makes retries idempotent. Existing enabled
push channels notify the profile's devices; without push the offer remains in its inbox.
The action does not guarantee a phone is registered or online. Saved offers expire with
the campaign. No webhook broadcast is requested by this viewer action.

Kotlin and Swift generated DTOs include the projection. Android TV, Android phone and
the web player implement rendering. Apple rendering remains a follow-up; an older Apple
client never requests `in_playback`, so it cannot accidentally show these cards elsewhere.
Jellyfin compatibility has no promotional playback surface and remains unchanged.

Verification covers validation, child/unknown-profile exclusion, chapter gates, web
focus/lifetime behavior, existing web player tests, Garden authoring and native builds.
A browser preview exercises the real web component against fixtures and a moving sample
video. Production publication, database-backed push delivery, and device playback still
require integration testing against a deployed server version with this capability.

## Approved visual direction — 2026-09-05

No enclosing panel: artwork and softly shadowed text float over the video. Keep
soft fades and deliberate focus. Display duration is editable from 5 to 60 seconds,
with a ten-second default, including both fades.
