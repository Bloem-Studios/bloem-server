---
title: Bloem User Guide
description: How to watch, listen and read with Bloem once someone has given you a login — signing in, profiles, finding things, playing them, downloading, requesting, and using other apps with your Bloem server.
summary: The viewer's guide to Bloem, written for someone opening it for the first time.
tags:
  - end-user
  - getting-started
  - playback
  - profiles
audience:
  - end-user
last_reviewed: 2026-09-05
related:
  - admin-guide.md
  - ../operations/compatibility-applications.md
---

# Bloem User Guide

Somebody you know runs a Bloem server — a private streaming service for a library they look after —
and has given you a way in. This guide is how to use it. It covers the web app, which works in any
browser, and points out where the phone, tablet and TV apps do the same thing differently.

Nothing here needs you to understand how the server works. If a word is unfamiliar, the
[glossary](#glossary) at the end explains it.

---

## 1. Getting in

### Your invitation

You will have received one of:

- **An invitation email.** Open the link, choose a password, and you are in. Your email address is
  your username from then on. The link only works once and expires, so if it has gone stale ask the
  person who sent it for a new one.
- **An invite code and a server address.** Open the address in a browser, choose *Sign up*, enter
  the code, and pick a username and password.
- **A username and password they set up for you.** Open the server address and sign in.

### The server address

Bloem is not a public website; every server has its own address. It looks like
`http://192.168.1.20:8090` on a home network, or `https://films.example.com` if the person running it
has set up a domain. You will need it for the apps as well as the browser, so keep it somewhere.

### Signing in from the apps

The Bloem apps for phone, tablet and TV ask for the server address first, then your login. On a TV
you can usually skip typing: choose *Sign in with a code* on the TV, then open the code shown there
in the app on your phone while signed in. Your phone does the typing.

### Passwords

Change your password under **Settings → Account**. If you forget it, the server administrator can
reset it for you; there is no self-service reset unless they have set up email.

---

## 2. Profiles

One login can have several **profiles** — one per person in the household. Each profile keeps its own
*Continue watching*, its own history, its own recommendations and its own settings, so someone else's
half-watched series does not appear in your row and your taste does not shape their suggestions.

- **Choosing a profile** happens right after you sign in. Pick yours.
- **Creating one:** on the profile screen, *Add profile*. Give it a name and, optionally, a picture.
- **A PIN** stops other people opening your profile. Set one in **Settings → Profiles**. Children's
  profiles can be limited by rating: the administrator or the account holder sets the ceiling, and
  anything above it does not appear.
- **Switching** is in the menu under your profile picture. On a TV, it is in the top menu.

---

## 3. Finding things

### Home

Home is rows: *Continue watching* at the top, then whatever the server offers — recently added, a
themed collection, things recommended for you. Rows you do not want can be hidden from **Settings →
Home screen**, and the order can be changed there too.

### Libraries

Each **library** is one kind of thing: Films, Series, Music, Audiobooks, Books. Open one to browse
it as a grid. The controls at the top let you sort (by title, by date added, by rating, by year) and
filter (by genre, by decade, by whether you have watched it). Filters stack.

### Search

The search box finds titles, people and — where the server has them — descriptions. On a phone or TV
you can speak instead of type. A result that is greyed out is something the server knows about but
does not have; see *Requests* below.

### Collections

**Collections** are sets someone has put together: a director's films, a franchise in viewing order,
a seasonal list. Some update themselves from outside lists. You can make your own too: open any
item and choose *Add to collection*.

### Calendar

**Calendar** shows what is arriving and when — new episodes of series in the library, upcoming
releases the server is tracking.

### People

Click any actor, director or writer to see everything of theirs the library holds.

### Your own lists

- **Watchlist** — things you mean to watch. Add from any item's page.
- **Favourites** — things you loved.
- **History** — everything you have watched, with the option to remove entries or mark something as
  unwatched.

All three are per profile.

---

## 4. Watching

### Playing something

Open an item and press *Play*. A series plays from where you left off; a film starts from the
beginning unless you had paused it, in which case it resumes and offers *Start over*.

If a title exists in more than one version — a theatrical and an extended cut, two languages — the
item page lets you choose which.

### Controls

Every player has the same set, laid out for the device:

- **Play/pause, seek, skip forward and back** by ten seconds.
- **Audio** — choose the language or the surround track.
- **Subtitles** — choose a track, turn them off, or change how they look (size, colour, background).
  If the track you want is not there, *Search subtitles* fetches one from the server's subtitle
  sources, and *Translate* can machine-translate one where the server has that switched on.
- **Quality** — normally *Auto*, which picks the best your connection can carry. Set it lower on a
  slow connection, or higher if the picture looks soft and your connection is good.
- **Chapters**, where the file has them.
- **Skip intro** appears at the start of episodes when the server has found the intro.
- **Next episode** counts down at the end of one; press it or let it run.
- **Sleep timer** stops playback after a set time or at the end of the episode.

On a TV remote: left and right seek, up shows the controls, and holding a direction seeks faster the
longer you hold.

### Picture quality and "transcoding"

Bloem sends the file exactly as it is when your device can play it. When it cannot — an unusual
format, a connection too slow for the original — the server converts it as it plays. You do not have
to do anything; it is automatic. If a film that looks perfect at home looks worse away from home,
that is the conversion adapting to the connection, and *Quality → Auto* is doing its job.

### Listening: music and audiobooks

Music plays in a queue with shuffle and repeat. Audiobooks remember your place per book, have their
own speed control, a sleep timer, and chapter navigation. Both keep playing when your phone's screen
is off, with controls on the lock screen.

### Reading: books and comics

Books (EPUB, PDF) and comics open in the reader. Your place is kept per book across every device.
The reader has text size, theme (light, dark, sepia) and page-turning direction under its own
settings button.

### Watching together

**Watch together** lets several people on different devices watch the same thing at the same time,
in sync, with a shared pause. Open an item, choose *Watch together*, and share the room code or link;
the others join from *Join a room*. Anyone in the room can pause for everyone. It works between the
web app and the phone and TV apps.

---

## 5. Downloads

If the administrator allows it, items can be downloaded to a phone or tablet for watching without a
connection — on a plane, on a train. On the item page, choose *Download* and a quality. Downloads live
under **Downloads** in the app, where you can see how much space they use and remove them. They obey
the same profile you downloaded them on.

Downloads are per device; they do not appear on your other devices.

---

## 6. Asking for something new

Where the administrator has turned it on, you can **request** things the library does not have.
Search for the title; if it is greyed out, open it and choose *Request*. **Requests → My requests**
shows the state of each — waiting, approved, declined, or available — and you are notified when one
turns up.

---

## 7. Notifications

The **Inbox** (the bell icon) collects things the server tells you: a new episode of something you
follow, a request that has been approved, an announcement from the administrator. **Settings →
Notifications** decides which of these you want, and whether they also arrive as push notifications
on your phone.

---

## 8. Settings worth knowing about

Under **Settings** in the web app (and under the same name in the apps):

| Page | What you can change |
|---|---|
| **Account** | Password, email, sign out everywhere. |
| **Profiles** | Names, pictures, PINs, rating limits. |
| **Playback** | Default quality, whether the next episode plays automatically, default audio and subtitle languages. |
| **Subtitle appearance** | How subtitles look, everywhere, once. |
| **Home screen** | Which rows appear and in what order. |
| **Appearance / Theme** | Light or dark, and on the web app a theme editor if you want your own colours. |
| **Accessibility** | Larger text, reduced motion, high contrast. |
| **Devices** | Every device signed in as you; sign any of them out. |
| **Connect apps** | Link outside services (see below) and manage which apps have access. |
| **Card overlays** | What badges appear on posters — resolution, watched ticks, and so on. |

Settings made on one device follow you to the others: a subtitle style set on the TV is the same
on the phone.

---

## 9. Using other apps with your Bloem server

Bloem understands the languages of Jellyfin, Emby and Audiobookshelf, so apps built for those work
with it. If you already use one of these — Infuse, VidHub, Findroid, Audiobookshelf's own apps and
players — you can point it at your Bloem server:

1. In the app, choose to add a Jellyfin or Emby server (or an Audiobookshelf server, for audiobook
   apps).
2. Enter your Bloem server address — the same one you use in the browser.
3. Sign in with your Bloem username and password.

If the app refuses to connect, the administrator may have turned that compatibility off; ask them.

---

## 10. When something is not working

| What you see | What to try |
|---|---|
| "Cannot connect" in an app | Check the address, including `http` vs `https`, and that you are on a network that can reach the server. Try it in a browser first. |
| A film stutters | In the player, set *Quality* to something lower. If it is fine at home and poor elsewhere, that is your connection. |
| Subtitles are out of sync | Try a different subtitle track; the *Search subtitles* option often has a better-timed one. |
| Something is missing that you know is there | It may be in a library your login cannot see, or it may not have been scanned yet. Ask the administrator. |
| Wrong artwork or title | Tell the administrator — it is a one-click fix on their side. |
| Your place in a series was lost | Check you are on the right profile; watch history is per profile. |
| You forgot your PIN | The account holder can clear it from **Settings → Profiles**. |

---

## Glossary

- **Direct play** — the file is sent to your device unchanged.
- **Library** — one kind of media: films, series, music, audiobooks, books.
- **Profile** — one person within a login, with their own history and settings.
- **Request** — asking the administrator for something the library does not have.
- **Server address** — the web address of the particular Bloem server you use.
- **Transcoding** — the server converting a file as it plays so your device can show it.
- **Watch together** — several people watching the same thing in sync from different devices.

## Source References

- `web/src/pages/` — Home, LibraryBrowse, Collections, Calendar, PersonDetail, Requests,
  Notifications, WatchTogether*, EbookReader, and the `settings/` pages named above
- `docs/operations/compatibility-applications.md` — how other apps connect
- `docs/architecture/invitations-onboarding.md` — invitations and sign-up
