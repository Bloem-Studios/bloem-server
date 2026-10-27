---
title: Bloem User Guide
description: How to watch, listen and read with Bloem once someone has given you a login — signing in, profiles, finding things, playing them, downloading, requesting, and connecting supported apps.
summary: The viewer's guide to Bloem, written for someone opening it for the first time.
tags:
  - end-user
  - getting-started
  - playback
  - profiles
audience:
  - end-user
last_reviewed: 2026-10-27
related:
  - admin-guide.md
  - ../architecture/bloem-compatibility-removal.md
  - ../../FORK.md
---

# Bloem User Guide

Somebody you know runs a Bloem server — a private streaming service for a library they look after —
and has given you a way in. This guide is how to use it. It covers the web app, which works in any
browser, and says where the phone, tablet and TV apps come in.

Nothing here needs you to understand how the server works. If a word is unfamiliar, the
[glossary](#glossary) at the end explains it.

> **About this guide.** Bloem is built on [Silo Server](https://github.com/Silo-Server/silo-server),
> an open-source media server, and follows it closely. The web app you use is Silo's, so parts of it
> say "Silo" — for example "Silo app or website" on the Connect Apps page. Bloem adds a few things on
> top, which this guide points out: Live TV, audiobook and ebook requests, browse-only previews,
> promotional cards and seasonal artwork, and separate sign-ins for household profiles. The guide
> was checked on 2026-10-27 against the web app's source at Bloem `main` `e852f92bf` (Silo through
> `3e2d306b2`). The apps for phones and TVs are separate projects; where this guide describes them,
> it says so, and the details depend on the app you use.

---

## 1. Getting in

### Browsing a preview

An operator may give you a **Browse the library** link. Open that link in your
browser to explore descriptions, series, seasons, episodes and collections.
The preview shows subscription options when you try to watch, listen or read.
Downloading and personal actions are unavailable in a preview. (Previews are a Bloem
addition; the operator can change the wording of these buttons.)

A guest preview ends after a limited time. Use **Keep browsing** to return to
the operator's launch page, or follow its subscription link. Your existing
subscriber login is kept separately. Native-app preview controls are not part
of this web flow.

### Your invitation

You will have received one of:

- **An invitation link, emailed or handed to you.** Open it and choose a password. The link only
  works once and can be replaced or revoked, so if it has gone stale ask the person who sent it
  for a new one.
- **An invite code and a server address.** Open the address in a browser, choose **Sign up** on the
  sign-in page, enter the code, and pick a username and password.
- **A username and password they set up for you.** Open the server address and sign in.

### The server address

Bloem is not a public website; every server has its own address. It looks like
`http://192.168.1.20:8090` on a home network, or `https://films.example.com` if the person running it
has set up a domain. You will need it for the apps as well as the browser, so keep it somewhere.

### Signing in from a TV or phone

Apps ask for the server address first, then your login. When a TV app shows a code or a QR code
instead of a password box, open that code on a phone or computer where you are already signed in:
the server's **Sign in a TV** page (`/activate`) approves it, and the TV finishes signing in.
The web sign-in page offers the reverse too: **Use your phone instead** shows a code you approve on
a device that is already signed in.

### Passwords

Change your password under **Settings → Account**. If your administrator supplies
a temporary password, replace it when prompted
before continuing into the library. For a forgotten local password, use
**Forgot password?** on the sign-in page when the server offers it, or ask an administrator for a
reset link. A reset link is single-use; use the newest one you received. Accounts managed
by an external identity provider use that provider's password recovery instead.

---

## 2. Profiles

One login can have several **profiles** — one per person in the household. Each profile keeps its own
*Continue watching*, its own history, its own recommendations and its own settings, so someone else's
half-watched series does not appear in your row and your taste does not shape their suggestions.
Playback belongs to the profile that started it: another profile on the same login cannot move or
stop it.

- **Choosing a profile** happens right after you sign in, on **Who's watching?**. Pick yours.
- **Creating one:** on that screen, **Add profile**, or **New profile** under **Settings → Profiles**.
  Adding a profile may ask for the primary profile's PIN.
- **A PIN** stops other people opening your profile. Set one in **Settings → Profiles**. A **Kids**
  profile can be limited by a maximum content rating, a maximum advisory age, a maximum playback
  quality and a set of libraries; the administrator or the household's primary profile sets these,
  and anything above the limit does not appear.
- **Switching** is **Switch Profile** in the sidebar menu under your profile picture.

**Settings → Profiles**, **Settings → Account** and **Settings → Signed-in sessions** are for the
household's primary profile (or an administrator); other profiles do not see them.

Under **Settings → Profiles → Profile sign-in credentials** (a Bloem addition), a permitted household
manager can set, rotate or disable a separate profile sign-in for supported clients.
Select and unlock the primary profile and confirm with the account's current local
password. If another change wins first, reload and review it; the rejected stale
action leaves the newer credentials and sessions intact. Rotation or disable signs
out that profile's direct sessions. SSO-only confirmation is not supported here.

The browser still requires **account sign-in**. Creating profile credentials does not
enable direct-profile browser login or shared-device profile pairing. TV account
activation is a separate flow.

---

## 3. Finding things

### Home

Home is rows: *Continue Watching* at the top, then whatever the server offers — Next Up, recently
added, a themed collection, things recommended for you. You can add, hide and reorder rows under
**Settings → Home Screen**.

**Home promotions: Off/On** controls optional server campaign cards for your profile
in this browser; it starts off and is unavailable for children. If browser storage
is blocked, the choice lasts only in the current tab. Dismissible cards offer their
own dismissal control. A pre-playback card always offers **Continue to content**
immediately and does not restart an already opened player.

Seasonal snow and artwork can appear on Home or sign-in. They respect reduced motion,
stop during video playback and offer a device-local switch, **Seasonal effects: On/Off**.
Signed-in Home shows public packs and packs for your current organisation; sign-in shows
public packs only. (Promotions and seasonal artwork are Bloem additions.)

### Libraries

Each **library** is one kind of thing: Movies, Series, a Mixed library of both, Audiobooks, Ebooks,
Manga or Podcasts. The sidebar lists the libraries you can see; open one to browse it. The controls
at the top let you sort and filter, and filters stack. **Settings → Libraries** chooses which
libraries appear in your menu, their order, and per-library languages.

### Search

**Search** in the sidebar finds titles and people. Where requests are switched on, results can also
include a **Not in your library** group: titles the server can look up but does not have, which you
can request; see [Asking for something new](#6-asking-for-something-new).

### Collections

**Collections** are sets someone has put together: a director's films, a franchise in viewing order,
a seasonal list. Some update themselves from outside lists. You can make your own too: open any
item and choose **Add to Collection**.

### Calendar

**Calendar** shows what is arriving and when — new episodes of series you follow or that are in the
library, and upcoming releases.

### People

Click any actor, director or writer to see everything of theirs the library holds.

### Your own lists

- **Watchlist** — things you mean to watch. Add from any item's page.
- **Favorites** — things you loved.
- **History** — everything you have watched. **Mark Watched** and **Mark Unwatched** on an item
  change it.

All three are per profile and in the sidebar.

---

## 4. Watching

### Playing something

Open an item and press **Play**. A series plays from where you left off. A film you had stopped part
way shows **Resume**; choosing it offers **Resume at** the saved time or **Play from Beginning**.

If a title exists in more than one version — a theatrical and an extended cut, two languages — the
item page lets you choose which, and the player's quality menu shows the version playing.

The web app can shuffle libraries, series, seasons and collections, choosing from content your
profile can access. Native apps show shuffle only if they implement it.

### Controls

The web player has:

- **Play/pause, seek, skip back and forward.** The skip intervals default to 10 seconds back and
  30 seconds forward; change them under **Settings → Playback**.
- **Audio** — choose the language or the surround track. If you do not choose, the server starts
  from your preferred language. When that track would have to be converted for your device, it
  picks another main track in the same language that your device plays as it is (never a
  commentary or audio-description track). A track saved for a series still plays as saved. (This
  automatic choice arrived from Silo on 2026-10-27.)
- **Subtitles** — choose a track, turn them off, adjust timing or **Sync to audio**. If the track you
  want is not there, **Add Subtitles…** searches online through the server's subtitle sources, and,
  where the server has AI switched on, **Translate subtitles with AI** or **Generate subtitles with
  AI** makes one.
- **Quality** — normally *Auto*, which picks the best your connection can carry. Set it lower on a
  slow connection, or higher if the picture looks soft and your connection is good.
- **Chapters**, where the file has them.
- **Skip Intro** appears at the start of episodes when the server has found the intro.
- **Next episode** counts down at the end of one; press it or let it run.
- **Sleep timer** stops playback after a set time.

How a TV remote or phone maps to these depends on the app.

### Picture quality and "transcoding"

Bloem sends the file exactly as it is when your device can play it. When it cannot — an unusual
format, a connection too slow for the original — the server converts it as it plays. You do not have
to do anything; it is automatic. If a film that looks perfect at home looks worse away from home,
that is the conversion adapting to the connection, and *Quality → Auto* is doing its job.

### Listening: audiobooks

Audiobooks remember your place per book and have their own speed control, sleep timer and chapter
navigation. Their skip intervals can differ from video's (**Settings → Playback**). Audiobookshelf
apps can connect as well; see section 9.

### Reading: books and comics

Books (EPUB, PDF, MOBI/AZW3, FB2) and comics (CBZ, CBR) open in the reader. Your place is kept per
book. The reader has font, font size, line height and margin, a theme (Light, Sepia or Dark),
paginated or scrolled flow, right-to-left page turning, bookmarks, highlights, text search and
read-aloud, under its own settings.

Provider-backed native EPUB/PDF reading is experimental and requires an enabled,
authorized source. Disabling or uninstalling that source prevents access while
retaining library and reading-progress identities. Native offline downloads and
conversion are not supported; an online
reader capability is not an offline-download guarantee.

### Watching together

**Watch Party** (in the sidebar) lets several people on different devices watch the same thing at
the same time, in sync. Create one there with **Create Watch Party**, or choose **Start a Watch
Party** from the **More** menu on a movie's or episode's page. The host either picks what plays or lets everyone vote.
Share the invite link or the eight-letter room code; others join under **Join a party**. Play, pause
and seek apply to everyone. The room ends two minutes after the host drops off. Whether a phone or TV
app can join depends on the app.

### Live TV and recordings

**Live TV** is a Bloem addition. It appears when your profile has permission. Channels can come
from a network tuner or an Xtream live provider configured by the administrator. You use
your Bloem account/profile, not the provider password. Xtream VOD and series are
not imported. Watching and recording share the provider's configured connection
budget, so a new stream can be refused while that capacity is in use.

The Live TV page has **Guide**, **Channels**, **My recordings** and **Recording rules**. Open a
channel from the guide to watch. You can schedule a guide programme, or a channel and time with
**Schedule by channel and time**; **My recordings** shows status and cancellation.
**Recording rules** can match titles that contain some text, or an exact series selected from the
guide (**Match guide series**), with optional channel and new-only filters. Removing a rule requires
confirmation and leaves its existing recordings in place. Editing rules and automatic retention are
not available.

If a recording action has an uncertain result, use **Reload recordings** and wait for
it to succeed before another action. Check the list before creating a fresh request:
reloading does not resend the previous write. A completed recording without a library
link needs an administrator to scan its recording folder; completion alone does not
make a catalog playback link. Device playback support still depends on the server,
tuner and browser configuration.

---

## 5. Downloads

Offline downloads are a feature of the phone and tablet apps, not of the web app. Whether you may
download, and whether converted (smaller) copies are allowed, is set by the administrator for your
account. How the app presents downloads depends on the app.

Downloads are per device; they do not appear on your other devices. Season and
series requests may queue beyond the concurrent preparation limit rather than
fail immediately. A download queued for the whole account is still checked against what your
profile may see before it is served. These local-media download paths do not support native
provider-backed ebook locations.

---

## 6. Asking for something new

Where the administrator has turned it on, you can **request** things the library does not have.
Open **Requests** in the sidebar: **Discover** lists titles to browse and request, and **Yours** shows
your requests grouped as *Needs attention*, *On the way*, *In your library* and *Cancelled*. You can
also search, and pick a title from the *Not in your library* results. For a series, choose the
seasons you want. If someone already asked for a title, you can follow their request instead of
submitting a duplicate, and you are notified when it arrives. Availability, approval and limits
depend on your account and group policy. **Settings → Requests** can make adding a title to your
watchlist request it too.

Bloem adds **Audiobooks** and **Ebooks** tabs beside **Movies & series** on the Requests page when
the server supports them. An audiobook request takes a title and, optionally, an author, ISBN or
ASIN; an ebook request takes the ISBN of the edition you want.

---

## 7. Notifications

**Notifications** in the sidebar (the bell) collects things the server tells you: a new episode of
something you follow, a request that has been approved, an announcement from the administrator.
**Settings → Notifications** decides which new-episode alerts you want and whether they also arrive
as browser notifications, email, Discord messages or webhooks, as far as the server offers them.
Phone push notifications are set up in the app.

---

## 8. Settings worth knowing about

**Settings** in the web app has these pages, grouped as in its menu:

| Group | Page | What you can change |
|---|---|---|
| Playback | **Playback** | Preferred quality, maximum bitrate, skipping intros, recaps and credits, auto-play next episode, seek intervals. |
| Playback | **Language & Subtitles** | Spoken and metadata languages, subtitle language and behaviour, forced subtitles, and how subtitles look. |
| Playback | **Your Devices** | Overrides for one device (quality, HDR, audio and subtitle preferences, sleep timer), and which profile this browser opens. |
| Appearance | **Navigation & Cards** | Poster size, badges on posters, shortcuts, and your menu. Every card shows its title and details. |
| Appearance | **Title Pages** | Title art, theme music, spoiler blurring, advisory age. |
| Appearance | **Accessibility** | Text size, text weight, contrast, date and time formats. |
| Home & Discovery | **Home Screen** | Which rows appear and in what order. |
| Home & Discovery | **Libraries** | Which libraries you see, their order, per-library languages. |
| Home & Discovery | **Personalize** | Re-tune the taste profile behind your recommendations. |
| Home & Discovery | **Requests** | Whether adding to your watchlist also requests a title (only when requests are on). |
| Connections | **Connect Apps** | Exactly what to type into an app's sign-in screen (see below). |
| Connections | **Watch Providers** | Sync history, ratings and watchlists with services such as Simkl. |
| Connections | **Webhook Sync** | Take watch progress from Plex, Emby or Jellyfin as it happens. |
| Connections | **History Import** | Copy watch history, watchlist and favorites from Emby, Plex or Jellyfin once. |
| Account | **Notifications** | Which alerts you get and where. |
| Account | **Profiles** | Names, PINs, Kids limits, profile sign-in credentials (primary profile only). |
| Account | **Account** | Password and sign-in providers shared by the household (primary profile only). |
| Account | **Signed-in sessions** | Where the account is signed in; sign out a browser or app, or everywhere (primary profile only). |

There is no theme choice: the web app has one theme, and the server's administrator sets its accent
colour.

Server-backed settings follow their account/profile/device scope. Browser Home promotion
choices and the seasonal off switch are local choices and do not roam to every device.

---

## 9. Connecting an app

Use the web app or an app your administrator supports. **Settings → Connect Apps** shows exactly
what to type for the kind of app you have:

- **Silo app or website** — apps that speak Bloem's or Silo's own API. Enter the same server address
  you use in the browser, sign in with your account, then choose a profile in the app's profile
  picker. Use device pairing when the app offers it.
- **Jellyfin-compatible app** (such as Infuse, Findroid or Swiftfin) — these connect to a separate
  address and port your administrator gives you, usually the server address with port `8096`. Sign in
  with `account#profile` as the username (for example `sam#Kids`) and your account password; if the
  profile has a PIN, add `#` and the PIN to the end of the password.
- **Audiobookshelf apps** connect the same way on their own port, usually `13378`.

The compatibility ports only work while the administrator has them switched on. If an app says your
username or password is wrong, the Connect Apps page lists the usual causes.

---

## 10. When something is not working

| What you see | What to try |
|---|---|
| "Cannot connect" in an app | Check the address, including `http` vs `https` and the port, and that you are on a network that can reach the server. Try it in a browser first. |
| A Jellyfin app rejects your password | Check the username is `account#profile` and, for a PIN profile, that the password ends in `#PIN`. See **Settings → Connect Apps**. |
| A film stutters | In the player, set *Quality* to something lower. If it is fine at home and poor elsewhere, that is your connection. |
| Subtitles are out of sync | Try **Sync to audio** or the timing control, or a different track from **Add Subtitles…**. |
| Something is missing that you know is there | It may be in a library your profile cannot see, or it may not have been scanned yet. Ask the administrator. |
| Wrong artwork or title | Tell the administrator — they can rematch it. |
| Your place in a series was lost | Check you are on the right profile; watch history is per profile. |
| You forgot your PIN | The household's primary profile, or an administrator, can change it in **Settings → Profiles**. |

---

## Glossary

- **Direct play** — the file is sent to your device unchanged.
- **Library** — one kind of media: movies, series, audiobooks, ebooks, manga, podcasts.
- **Primary profile** — the household's main profile, which manages the others.
- **Profile** — one person within a login, with their own history and settings.
- **Request** — asking the administrator for something the library does not have.
- **Server address** — the web address of the particular Bloem server you use.
- **Silo** — the open-source media server Bloem is built on.
- **Transcoding** — the server converting a file as it plays so your device can show it.
- **Watch Party** — several people watching the same thing in sync from different devices.

## Source References

- `web/src/lib/userSettingsNav.ts` — the settings pages and their labels
- `web/src/components/AppSidebar.tsx`, `web/src/App.tsx`, `web/src/bloem/routes.tsx` — navigation and routes
- `web/src/pages/` — Home (`BloemHome.tsx`), LibraryPage, Collections, Calendar, PersonDetail,
  Requests (`web/src/bloem/BloemRequests.tsx`), Notifications, `watchtogether/`, EbookReader,
  LiveTV, ActivateDevice, Login and the `settings/` pages named above
- `web/src/player/components/` — the player controls
- `internal/playback/audio_select_v3.go` — the automatic audio choice
- `internal/jellycompat/login.go`, `internal/audiobooks/abs/login.go` — `account#profile` sign-in
- `docs/architecture/bloem-compatibility-removal.md` — Jellyfin and Audiobookshelf compatibility servers
- `docs/architecture/invitations-onboarding.md` — invitations and sign-up
