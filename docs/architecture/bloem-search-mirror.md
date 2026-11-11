# How the search mirror works

> **Provenance and status.** Bloem page, rewritten on 2026-11-11 and checked against
> `main` at `691b148e2` with the search mirror's own commits on top, which land
> together with this page. The search mirror is a Bloem addition: Silo has one
> Meilisearch and no second copy of its index. The mirror is off unless an operator
> turns it on. The technical rules are in the [For developers](#for-developers)
> section.

Bloem can keep a second Meilisearch, the **mirror**, up to date with the catalog
search index. Every change that reaches the main search index reaches the mirror a
minute or so later. This page walks through what the mirror is, how it is turned on,
how it stays current, how it makes a full copy, how it handles a failover, which
outbox rows it keeps, what happens when something fails, and what it shows an
operator.

## What the mirror is

Silo finds titles through one Meilisearch, the **main index**. An operator who runs a
second search server, for example next to a standby copy of Silo, can name it as the
mirror. Bloem then keeps the same catalog index there, under the same name, with the
same settings and the same vectors.

- **After a failover, the new main index is complete.** Nobody runs SQL to catch it
  up. Bloem replays the changes the old mirror had not received. The old main server
  becomes the new mirror and catches up as soon as it answers.
- **The mirror is not a fallback for searches.** Each Silo copy still searches only
  its own main index. There is one mirror, and it holds only the catalog search index.
- **The mirror is never required.** A mirror that is down, slow or set up wrongly
  never slows, blocks or breaks the main index, and never stops Silo from starting.
  The mirror also never writes to the main server's catalog index. The only things it
  writes to the main server are its identity marker and the removal of its own leftover
  copy indexes (see [leftover side indexes](#leftover-side-indexes-on-the-new-main)).
- **The mirror is off unless it is configured.** With no mirror address, Bloem
  registers no mirror task, writes no marker and no row, exports no metric, shows no
  admin section and logs nothing. The only footprint is one column and one index on
  the search outbox (see [the bookmark](#the-bookmark-which-transactions-it-has-seen)).

## Turning it on

An operator sets environment variables. Nothing is typed into the admin page.

| Variable | Meaning |
| --- | --- |
| `SILO_MEILISEARCH_MIRROR_URL` | The mirror's address. Setting it turns the mirror on. Unset or blank means off. |
| `SILO_MEILISEARCH_MIRROR_API_KEY_FILE` | Path to a file holding the mirror's key. Preferred. |
| `SILO_MEILISEARCH_MIRROR_API_KEY` | The key itself, for parity with `SILO_MEILISEARCH_API_KEY`. |
| `SILO_MEILISEARCH_MIRROR_MAX_LAG` | How far behind the mirror may fall before it stops catching up and makes a fresh full copy. Default `72h`. While Silo switching is enabled, at most `20h`. |

**Nothing is stored.** This differs from the main search address, which Silo copies
into `server_settings` at each start.

- The mirror's values stay in the process's memory. They are never written to the
  database. They are never shown in full: the admin section shows the address with
  any password part removed, and the key is never shown or logged.
- They are read once, at start. Every Silo process that shares one database must name
  the same mirror.
- Setting both key variables is an error. A key file with a trailing newline works,
  and an empty file means no key, which suits a Meilisearch with no master key.
- There is no fallback to the main search key. Sending it to a mistyped address would
  leak it.
- A key file must be readable inside the container, so mount it on every Silo copy.
- An address is checked by the same rules as the main address. A trailing `/` is fine.
- Everything else comes from the main search settings: the index name, the embedder,
  the semantic and quantization flags, the item types and the batch sizes. The mirror
  must match the main index exactly.

**A bad configuration never stops Silo.** If the address is invalid, both keys are
set, the key file cannot be read, the address is the main server's, or the lag limit
is not a duration above zero, Silo logs one clear English line at start, the mirror
stays off, and the admin section and the metrics show `misconfigured`. A key that the
mirror server rejects, and an address that proves to reach the main server, show up
the same way once the mirror first talks to the server. The main search variables
behave differently, because a bad value there stops startup. The mirror follows
"never required" instead.

## Keeping the mirror current

Silo writes a row to an **outbox** (`catalog_search_index_events`) for every change
that affects search, in the same transaction as the change. The main index works
through those rows and marks each one done. The mirror reads the same rows, but keeps
its own bookmark, because the "done" marks belong to the main index. A slow or broken
mirror falls behind and catches up. It never holds the main index up.

### The bookmark: which transactions it has seen

Outbox rows get their numbers when a change *writes* its row. A row becomes visible
only when the change's *transaction finishes*. A slow transaction can therefore
finish after a row with a higher number is already visible. A bookmark that said
"done up to row 1,000" would skip the slow row 999 for good, and nobody would notice.
That is drift, and the mirror must never drift.

So the mirror's bookmark is a Postgres **transaction snapshot**: a record of which
transactions had finished at one moment.

1. Each run takes a new snapshot.
2. It applies exactly the rows from transactions that finished between the bookmark
   and the new snapshot, in the order of their numeric transaction id.
3. It stores the new snapshot as the bookmark.

A long-running transaction elsewhere does not hold the mirror up. Its rows are picked
up in the run after it finishes. This is how PgQ, Skype's queue, reads its tables.

This example is made up. Transaction A writes row 100 and stays open. Transaction B
writes row 101 and commits. The mirror runs and applies row 101; its bookmark says A
has not finished. A commits. The mirror runs again, sees that A's transaction is now
visible, and applies row 100. A row-number bookmark would have moved past 101 and lost
row 100.

If the mirror loses a bookmark update in a crash failover, the bookmark only rolls
*back*, so the mirror replays a little. A replay is harmless (see the next section).
A bookmark that is ahead of the database's current transaction counter must come from
another database, for example a manual dump loaded into a new cluster. It triggers a
full copy.

Rows written before the mirror existed have no transaction recorded. They are ignored,
because the first full copy covers them.

### Make item X match the database

The mirror reads every outbox row as "item X changed". That covers upserts, deletes
and renames (both the old and the new id). It never trusts the row's action.

1. For each page of rows, the mirror collects the item ids.
2. It rebuilds their documents from Postgres with the same function the main index
   uses.
3. It upserts the items that exist and deletes the rest.

So a delete followed by the item reappearing ends with the item present. Order does
not matter, and applying a row twice is harmless. The mirror differs from the main
index only where the main would wrongly delete an item that exists, which can happen
when an old delete is applied out of order. There, the mirror is more correct.

A page of 10,000 documents with large vectors can be bigger than one Meilisearch
request allows. The mirror sends it in several payloads, as the main index does, and
never treats the size as a rejected page.

### Pace and waiting

The mirror runs at startup and every 60 seconds. Within one run it applies pages back
to back for up to 5 minutes, so it catches up faster than the main task's one batch a
minute. A page is as large as the live sync batch size.

While the main index is not current, the mirror waits (state `waiting`). That covers a
search provider that is not Meilisearch, an index that is not built yet, and a main
index whose schema version differs from the one this server builds, because the main
is about to rebuild. Documents built for one schema must not go into an index made
for another.

## Making a full copy

A **full copy** builds the whole index again from Postgres. It happens at first setup,
when the main index was rebuilt under a new name, when the mirror fell behind past the
lag limit, when the mirror server is new or was replaced, when the manual task
`rebuild_catalog_search_mirror` is run, and after a backup restore.

The copy is always built from Postgres, the way the main index rebuilds, with the same
document builder and settings. Vectors come from Postgres, so nothing is embedded
again. An index cloned from a snapshot of the main server is never adopted: it cannot
say how old it is, so it could miss changes.

1. **Record a snapshot.** It becomes the bookmark after the copy.
2. **Create a side index**, `<prefix>_mirrorcopy_<unix seconds>`, with the main
   index's settings.
3. **Fill it** from the catalog in content-id order, with at most 2 Meilisearch tasks
   in flight. That is gentler than the main rebuild, because the main index is serving
   on the same database.
4. **Publish it** under the real index name in one step.
5. **Delete the side index.**

A half-finished copy is never visible under the real name. That matters because this
server can become the main one in a failover halfway through. The copy saves how far
it got after each acknowledged batch, so it carries on after a Silo restart. While a
copy runs, the mirror applies no small changes: the final catch-up from the starting
snapshot covers them.

A batch the mirror refuses is split, as a page is (see
[when something fails](#when-something-fails)). The documents Meilisearch itself
refuses are dead-lettered and the copy goes on. Only a batch of two or more documents
with none accepted stops the copy, and then the mirror is `failing`.

After publishing, the bookmark becomes the snapshot from step 1, so everything that
changed during the copy is applied next. The dead-letters from before the copy are
cleared, because the copy holds those items as they are now. The ones the copy itself
refused stay.
Superseded indexes **on the mirror only** are deleted: this prefix's `_rebuild_*` and
`_mirrorcopy_*` names and the previously published index. The identity marker index
and every index outside the prefix are never touched. A copy that a failover
interrupts leaves its side index on the server that becomes the main; the mirror
removes it from there after the swap (see
[leftover side indexes](#leftover-side-indexes-on-the-new-main)).

### Following the main index's rebuilds

A rebuild of the main index builds a new index and points the main state row at it.
The mirror notices the new index name or schema version and makes a full copy of it,
under the name the main state row names. It does this **after** the main index
publishes, never alongside the main rebuild. Until its copy is finished, the mirror
still holds the old index (see [known limits](#known-limits)).

## When the two servers swap roles

After a failover, the new live copy's main search is the old mirror. Changes the old
mirror had not received are already marked done for the main index, so the main
index would never send them. Silo cannot tell the two servers apart by address: both
copies may use the same one.

### Each server carries an identity

The mirror writes a small separate index, `<prefix>_bloem_target`, with one document
holding a random id. It goes on both the main and the mirror server, once. The mirror's
state row remembers which id is the main and which is the mirror. Each run reads both
markers, two tiny reads. The name never matches the main rebuild's cleanup pattern, so
the main index never deletes it.

Before its first write, the mirror proves that its address is not the main server. It
writes a marker on one side and checks that it does not appear on the other. If it
does, the mirror refuses with "the mirror points at the main search server" and writes
nothing more.

| What the markers show | Meaning | What the mirror does |
| --- | --- | --- |
| Main is the remembered main, mirror is the remembered mirror | Normal | Applies changes |
| Main is the remembered **mirror** (or mirror is the remembered **main**) | **The roles swapped: a failover** | The swap step, below |
| Both carry the same id | The mirror server is a clone of the main | New id on the mirror, then a full copy |
| The mirror has an unknown id or none | The mirror server is new or was replaced | Writes an id, then a full copy |
| The main has an unknown id or none | The main server was replaced | Writes an id. The main index rebuilds by itself, because its index is missing. The mirror carries on |
| The mirror's marker cannot be read, and the main is the remembered **mirror** | A failover while the old main is down | The swap step, below. It needs only the database and the main server |
| The mirror's marker cannot be read, otherwise | The mirror server is down | Applies nothing to the mirror |
| The main's marker cannot be read | The main server is down | Records the error and applies nothing |

The mirror's marker matters. The bookmark always describes the server whose marker
matches, so a change applied to the wrong server cannot move it on.

The swap is decided from the main's marker alone, so a crash failover works with the
old node still down. The swap step, the settle replay and a main rebuild a swap left
owed run whatever the mirror's own marker shows, because none of them needs the mirror
server.

### The swap step

The swap step runs in one database transaction. It holds the mirror's lock and, for a
moment, the main index's lock, so it cannot race the main index's sync or rebuild.
This is the only time the mirror takes the main index's lock.

1. **If the old mirror was following the current index:**
   - It reopens every main-index row the old mirror's bookmark had not covered:
     `processed_at` is cleared and the tries are reset. The main index re-sends those
     changes to the new main within a minute or two.
   - It adds fresh upsert rows for the old mirror's dead-lettered items.
2. **If the old mirror was mid-copy or hopelessly behind:**
   - With no index under the current name, the main index's own code rebuilds it.
     Search falls back to Postgres meanwhile, as it does whenever an index is
     missing.
   - With a stale index under that name, the mirror starts the normal main rebuild
     task. It does so only when that index is the one the old mirror itself
     published. Silo's rebuild publishes every index under a fresh name, so an index
     under a name the mirror never published was built on that server after the
     failover, and is fresh. The rebuild is recorded in the mirror's row and started
     after the swap has released the main index's lock, which the rebuild takes
     itself. Every later run starts it again until the main state names another
     index, so a start that fails is not lost. Search keeps using the stale index
     until the new one is published.
3. **The new mirror, the old main, gets a bookmark** from the oldest main-index row it
   may lack. That is the oldest unfinished row, or else the oldest row processed since
   the earliest lease start of the processes that began after the previous swap, less
   5 minutes. Those processes include the new copy's, whose main sync may have sent
   rows only to the new main before this swap. Leases silent for longer than the cap
   are left out. A wider replay is harmless, because every item is rebuilt from
   Postgres.
4. **It swaps the remembered identities.** It also records when the swap happened
   (database time), which process did it, and that the swap is **settling**. All of
   this is one transaction: a crash leaves the old state or the new one, never half.

The mirror's row is saved by compare-and-set on a generation counter. Every save
requires the generation the run loaded. A run that lost the row, because another run
saved it, or lost its lock, because a database failover dropped it, stops quietly and
records nothing. A stale process therefore cannot undo a swap.

### Leftover side indexes on the new main

A copy that a failover interrupts leaves its side index, `<prefix>_mirrorcopy_<unix
seconds>`, on the server that is now the main. Silo's own cleanup never removes it: it
deletes only the main's `_rebuild_*` names and the previous index. A leftover can be
as big as a whole catalog index.

After a swap, and once every time a process starts, the mirror deletes its own leftover
`<prefix>_mirrorcopy_*` indexes from the main server. It deletes nothing else there:
never the main's active index, never a `_rebuild_*` index, never the identity marker
and never a name outside that pattern. The only writes the mirror makes to the main
server are therefore its identity marker and the removal of its own copy leftovers.

### Two Silo copies run together for a while

During a move, the old copy keeps running for a short while after the new one serves.
In measured moves that was 24 to 62 seconds. The old copy reconnects to the new
database primary, so its tasks keep running. Its unmodified main sync takes the same
lock and reads the same outbox, but writes to *its* search server, which is now the
old main. Rows the swap step reopened, or new rows, can be sent to the wrong server
and marked done. Bloem cannot stop the old copy without changing Silo's sync, and it
must work for operators who have no controller to stop one. So it tolerates the
overlap, in three parts.

1. **The old process stands down.** Only a process that started after the last
   recorded swap, or the process that recorded it, may swap roles. A process takes its
   start time from the database clock on its first mirror run. Any other process whose
   main server is the remembered mirror stops its mirror task for the rest of its
   life and logs "another Silo copy took over search". That prevents flip-backs, which
   would set the bookmark from the oldest unfinished row and could hide rows the old
   copy sent to the wrong server.
2. **The settle replay.** While the swap is **settling**, the mirror waits for the old
   copy, and only for it. Each process records in its lease which main server it talks
   to: the id of that server's identity marker, in the column `main_target`. The
   settle waits for a process that has touched its lease within 2 minutes and either
   still talks to another main than the new one (the old main, which is now the
   mirror), or started before the swap and has not reported a main yet. The process
   that did the swap is never waited for. A process of the new copy talks to the new
   main, so the new copy can run several processes without keeping the swap settling.
   When no such process is left, the mirror reopens every main-index row marked done
   since the swap, less a minute's margin, so the main index re-sends them to the new
   main. It clears "settling". Rows the old copy handled *before* the swap need
   nothing: the bookmark describes the old mirror server's content exactly, whoever
   processed what.
3. **The new mirror needs nothing extra.** Whatever the old copy sent to the old main
   after the swap is already on the old main. Everything else is replayed from the
   oldest-unfinished-row bookmark.

"Settling" shows in the admin section and in the state metric. If it has not settled
after 30 minutes, it becomes a warning. The mirror keeps waiting and does not reopen
early.

If only one Silo copy has the mirror variables, nobody runs the swap step after a
failover. The other copy's admin status shows the warning
`configured_on_one_copy_only`, and that copy never runs a swap step.

### Example: a failover

This example is made up. An operator runs two Silo copies on one database and one
search server beside each copy: server A beside copy one, server B beside copy two.
Each copy's main search is the server beside it, and its mirror is the server beside
the other copy.

1. **Normal.** Copy one is live. Its main is A, and B is its mirror, about 40 seconds
   behind. The mirror's state row remembers: A carries id `a1`, B carries id `b2`.
2. **The move.** The operator makes copy two live. Its main search is now B, which is
   complete up to 40 seconds ago, and its mirror is A.
3. **The swap step.** Copy two's first mirror run reads both markers and finds that
   its main carries `b2`, the remembered mirror. The roles swapped. In one
   transaction it reopens the rows B had not received, so the main index sends those
   40 seconds of changes to B within a minute or two. It gives A, the new mirror, a
   bookmark from the oldest row A had not finished. It swaps the remembered ids and
   marks the swap as settling.
4. **The overlap.** Copy one keeps running for about 30 seconds. Its mirror task sees
   that its main server, A, carries the remembered mirror's id, does not swap, and
   stands down for the rest of its life.
5. **Settled.** Two minutes after copy one's last sign of life, the settle replay
   reopens the rows marked done since the swap, B receives anything copy one sent to
   A in the overlap, and "settling" clears. Both servers now equal the database.

## Keeping the rows the mirror needs

Silo's own cleanup (`cleanup_catalog_search_index_events`, a step of
`database_maintenance`) deletes outbox rows processed more than 24 hours ago that are
behind the main index's fence. It knows only the main index's bookmark, so on its own
it would delete rows the mirror still needs.

Bloem puts a **mirror-aware wrapper** around that step.

- **No mirror row, or the mirror is past its cap:** the wrapper calls Silo's own
  cleanup unchanged. An operator without a mirror runs exactly Silo's code.
- **Otherwise:** it applies the same conditions plus one more. It keeps every row the
  mirror's bookmark has not covered yet. While a copy runs, that means every row
  since the copy began.

The second case runs a copy of Silo's query, kept in `searchMirrorSiloPruneSQL`. A test
reads Silo's `PruneProcessed` and fails when the two differ, so a change in Silo cannot
leave operators with a mirror on the old query unnoticed (see
[the tests](#tests)).

**The cap** is `SILO_MEILISEARCH_MIRROR_MAX_LAG`, 72 hours by default, which covers a
weekend outage of the other node. It cannot be 24 hours, because cleanup already keeps
rows that long. It is stored in the mirror's row so cleanup reads the same value the
mirror does. Both sides measure the cap from the same time in the same row:

- while a copy runs, the copy's last progress. A copy that keeps making progress is
  never past the cap, however long it takes. A copy that made no progress for longer
  than the cap starts again;
- otherwise, the later of the bookmark's snapshot and the last published copy.

Past the cap, the mirror marks itself `needs_full_copy`. When the mirror server answers
again, the mirror makes the copy by itself. Cleanup stops holding rows 15 minutes after
the cap. A run decides "past the cap" when it starts and works for at most 5 minutes,
so the margin lets a run that started under the cap finish, and covers clock skew.

**While Silo switching is enabled, the cap is at most 20 hours.** Upstream Silo, when
it serves the database, holds nothing for the mirror and deletes rows processed more
than 24 hours earlier. The database records only whether switching is enabled, not
who serves. A mirror less than 24 hours behind cannot lose a row it needs to Silo's
cleanup, because every such row was processed after the bookmark's snapshot. See
[how Silo backend switching works](silo-backend-switching.md).

**The cost** is outbox rows held for longer. During a re-embed the outbox grows by
roughly 2 million rows a day, so a mirror 72 hours behind can hold about 6 million
rows until it catches up.

## When something fails

The mirror tells three kinds of trouble apart.

1. **The mirror cannot be reached:** connection refused, time-outs, server errors and
   answers it cannot decode. Nothing counts as a failed try. The bookmark stays put,
   lag grows, and the state is `unreachable` since a time. Cleanup holds rows up to the
   cap. This matches the main index, whose outages do not use up tries either.
2. **The key is rejected** (HTTP 401 or 403). The state is `misconfigured`, reason
   "key rejected". No tries are used, and the process stops trying until Silo
   restarts. Meilisearch also answers 403 when the key's indexes do not cover the index
   asked for. The finding clears after a page succeeds, for example once the key is
   fixed and Silo restarted.
3. **The mirror rejects a page,** because a Meilisearch task fails or another 4xx
   comes back, or Postgres fails while building the page.
   1. The page is retried with the main index's timing: 30 seconds times the number
      of tries, up to 15 minutes, for 10 tries.
   2. On the 10th failure the page is split into halves and each half is retried,
      recursively, so only the documents Meilisearch really refuses are left. For one
      bad document in a page of 10,000 that is about 14 extra tasks. The main index
      instead dead-letters the whole batch.
   3. The refused items go to a new table, `bloem_search_mirror_dead_letters`: the
      content id, the first and last failure, the tries and the last error.
   4. The mirror moves on.

**Only what the split proves bad is dead-lettered.** If a page of two or more items
has nothing accepted, for example with a full disk or a broken index, the mirror
server itself is failing. Then nothing is dead-lettered, the bookmark stays put, and
the state is `failing` with the error. A broken mirror cannot dead-letter the whole
catalog. A refused page of one item is dead-lettered, because nothing tells a refusing
server from a bad item.

Once an hour the mirror retries its dead-lettered items by id. A success removes the
entry, and so does any later change to the same item. A full copy clears them all.

A mirror that was `unreachable` goes back to `following` as soon as the mirror server
answers again. A mirror that was `failing`, or whose key was rejected, goes back to
`following` after a page succeeds. Either way its last error and retry timing are
cleared. A shutdown in the middle of a page never counts as an error and never saves a
failure state.

## Watching the mirror

### States

The mirror is always in one of eight states. When more than one applies, the first in
this list wins.

| State | Meaning |
| --- | --- |
| `misconfigured` | A configuration fault, a rejected key, or an address that reaches the main server |
| `unreachable` | The mirror server cannot be reached |
| `failing` | The mirror server accepts nothing of a page or copy batch of two or more items, or another step (a copy, a publish, a marker write) fails on a rejection or a database error |
| `needs_full_copy` | The mirror fell behind past the cap, or is otherwise due a full copy that has not started |
| `copying` | A full copy is running |
| `waiting` | The main index is not current yet |
| `settling` | A role swap has happened and the settle replay has not run. Shown only while the underlying state is `following` |
| `following` | Normal: the mirror applies changes as they come |

### Metrics

The metrics are exported only when the mirror is configured. They are sampled from the
database every 30 seconds, like Silo's queue gauges, so every API process reports the
same values. Aggregate them with `max`. A sample older than 90 seconds is left out.

| Metric | Meaning |
| --- | --- |
| `silo_search_mirror_state{state}` | 1 for the current state: `following`, `copying`, `waiting`, `unreachable`, `needs_full_copy`, `misconfigured`, `failing`, `settling` |
| `silo_search_mirror_lag_seconds` | Now minus the time of the snapshot the mirror holds: the copy's start while a copy runs, the bookmark otherwise |
| `silo_search_mirror_pending_events` | Outbox rows the mirror has not applied, counted up to 1,000,000. Absent when the count fails; the other series are still exported |
| `silo_search_mirror_dead_letters` | Items the mirror could not index |
| `silo_search_mirror_last_success_timestamp_seconds` | Last page or copy batch that succeeded. It stays still in a quiet catalog, so do not alert on it alone |
| `silo_search_mirror_copy_progress_ratio` | Progress of a full copy, from 0 to 1, also while it waits out an outage; 0 when none is under way |
| `silo_search_mirror_errors_total{kind}` | `unreachable`, `rejected` or `database`. A rejected key counts as `rejected` |
| `silo_search_mirror_role_swaps_total` | Failovers the mirror handled |

Suggested alerts:

- **Warning:** lag above 15 minutes for 10 minutes.
- **Critical:** lag above 1 hour.
- `needs_full_copy` or `misconfigured` for 5 minutes.
- Dead-letters above 0 for 1 hour.

### The admin section

The Search settings page gets a small read-only section, right after the search status
panel. It shows nothing unless the mirror is configured or mirror state exists, so
there is no new navigation entry. It has no inputs, and it links to the two mirror
tasks on the existing tasks page: `sync_catalog_search_mirror` and
`rebuild_catalog_search_mirror`, the manual full copy. It reads one route,
`GET /api/bloem/v1/admin/search/mirror`, which returns:

- whether the mirror is configured, and where from (the environment);
- the mirror's address with any password part removed, and whether a key is set and
  from where;
- the state, with progress while it is copying;
- the lag and "up to date as of", measured from the same snapshot as the lag metric;
- the rows waiting;
- the published index name next to the main index's name;
- the last full copy;
- the last error and since when;
- the dead-letter count;
- the cap;
- warnings, such as `configured_on_one_copy_only`.

## Known limits

- **A failover within hours of a main rebuild.** Until the mirror finishes copying the
  newly named index, a failover lands on a server without it. The new live copy then
  searches through Postgres until its own rebuild finishes, which takes hours. That is
  what happens whenever an index is missing. A later improvement, not built yet, is to
  start the mirror's copy as soon as a new `<prefix>_rebuild_*` index appears on the
  main, so both finish at about the same time.
- **A few minutes after each move, some updates may still be missing on the new
  main.** These are updates the old Silo copy sent to the old server during the
  overlap. The settle replay sends them again once the old copy is gone, normally
  within about 3 minutes of the move.
- **A crash failover with asynchronous replication can lose the last milliseconds** of
  database changes. Search may then show a few documents for changes the database lost.
  Silo loads hits from the database, so they drop out. This is true of the main index
  today.
- **Main-index dead-letters stay stale on the main server until its next rebuild,** as
  today. The mirror may hold the right version.
- **The first full copy is slow and needs room.** It reads the whole catalog from
  Postgres, which takes hours on a large one, and the mirror server must hold the old
  and the new index side by side until the copy is published. Run it off-peak.
- **Meilisearch upgrades are the operator's job.** The mirror does not carry the
  Meilisearch binary, so upgrade both servers and keep their versions equal.

## Who can change what

| Who | What they do |
| --- | --- |
| Operator | Sets the four environment variables on every Silo copy, creates the mirror's key, keeps both Meilisearch servers on the same version. |
| Platform administrator | Reads the admin section and runs the two mirror tasks from the tasks page. Nothing about the mirror can be changed in the admin page. |
| Bloem | Everything else: the worker, the bookmark, the swap step, cleanup and the metrics. Silo's own files change only at seams that already exist. |

## Words used in the code

| On this page | In the code and database |
| --- | --- |
| The mirror | the worker in `internal/catalog/bloem_search_mirror*.go`; its row in `catalog_search_index_state` has provider `meilisearch_mirror` |
| The two mirror tasks | `sync_catalog_search_mirror` and `rebuild_catalog_search_mirror` (`internal/taskmanager/tasks/bloem_search_mirror.go`) |
| The outbox | `catalog_search_index_events`, written by Silo |
| The bookmark | a Postgres snapshot, saved in the `bloem_mirror` JSONB column of the mirror's state row; rows are read by `tx_id` |
| Side index | `<prefix>_mirrorcopy_<unix seconds>`, where `<prefix>` is `CatalogSearchSettings.MeilisearchIndex` |
| Identity marker | the index `<prefix>_bloem_target` |
| Dead-letters | `bloem_search_mirror_dead_letters` |
| The lease | `bloem_search_mirror_processes`, one row per Silo process with the mirror configured: when it started, when it last touched the row, and `main_target`, the id of the identity marker of the main server it talks to |
| Mirror-aware cleanup | `SearchMirrorAwarePruner` in `internal/catalog/bloem_search_mirror_prune.go`, wired by `bloemWireSearchMirror` in `cmd/silo/bloem_main.go` |
| The metrics | `internal/catalog/bloem_search_mirror_metrics.go`, prefix `silo_search_mirror_` |
| The admin route | `GET /api/bloem/v1/admin/search/mirror` |
| Silo switching | the `bloem_silo_switching` flag |

## For developers

**API:** the route's contract is in the [native reference](../bloem-api-reference.md)
and in `internal/apiv2/bloem_search_mirror_document.go`. Nothing is added to `/api/v1`
or to Silo's search status call.

### Where the code lives

Every new line is in a `bloem_*` file, a Bloem-owned file or an existing seam.
`contracts/seams.txt` does not grow.

| Part | Files |
| --- | --- |
| Settings and redaction | `internal/catalog/bloem_search_mirror_config.go` |
| Meilisearch calls on Silo's `*meilisearchClient` | `internal/catalog/bloem_search_mirror_meili.go` |
| State row, snapshot helpers, process lease | `internal/catalog/bloem_search_mirror_state.go` |
| Paging a snapshot window and applying items | `internal/catalog/bloem_search_mirror_follow.go` |
| Full copy, publish, cleanup on the mirror | `internal/catalog/bloem_search_mirror_copy.go` |
| Markers and verdicts | `internal/catalog/bloem_search_mirror_identity.go` |
| Swap step, stand-down, settle replay | `internal/catalog/bloem_search_mirror_failover.go` |
| Retry timing, split, dead-letters | `internal/catalog/bloem_search_mirror_failures.go` |
| `SearchMirror`: construction, `RunOnce`, `ForceFullCopy`, lease loop | `internal/catalog/bloem_search_mirror.go` |
| Cleanup wrapper | `internal/catalog/bloem_search_mirror_prune.go` |
| Metrics | `internal/catalog/bloem_search_mirror_metrics.go` |
| Admin status | `internal/catalog/bloem_search_mirror_status.go`, `internal/api/handlers/bloem_search_mirror.go`, `internal/api/router_bloem_search_mirror.go`, `internal/apiv2/bloem_search_mirror_document.go` |
| The two tasks | `internal/taskmanager/tasks/bloem_search_mirror.go` |
| Wiring | `cmd/silo/bloem_main.go`, with one added line in `cmd/silo/main.go`, an existing seam |
| Admin section | `web/src/bloem/admin/SearchMirrorStatus.tsx`, one import and one element in `web/src/pages/admin-settings/SearchSettings.tsx`, an existing seam; its text is in Bloem's catalog under `bloem.admin.searchMirror.*`, translated into the seven Bloem locales |

### Configuration

- `LoadSearchMirrorConfig` never fails. A fault is the configuration's `Problem`, and
  only the first one found is reported. A configuration with a problem carries no key.
- The problem codes are `invalid_url`, `both_keys_set`, `key_file_unreadable`,
  `same_address_as_main` and `invalid_max_lag`. Two more are set at run time:
  `key_rejected` (the server answered 401 or 403) and `same_server` (the markers show
  the address reaches the main server).
- The 20-hour ceiling for `SILO_MEILISEARCH_MIRROR_MAX_LAG` while `bloem_silo_switching`
  is enabled is applied at run time, not when the settings are read.
- A misconfiguration logs once per start, in English, on one line. No log line ever
  contains a `SearchMirrorConfig` value, because it holds the key.

### Database changes

- `migrations/sql/20261111083319_bloem_search_mirror.sql` adds the nullable column
  `catalog_search_index_events.tx_id xid8` and sets its default to
  `pg_current_xact_id()` in a separate statement, so there is no table rewrite. That
  function gives the top-level transaction id inside savepoints too. Old rows keep a
  NULL `tx_id`. Silo's own inserts, including those from upstream Silo during
  switching, get the default without knowing about the column.
- The same migration adds `catalog_search_index_state.bloem_mirror jsonb`, which holds
  the bookmark snapshot, the resume point, the state, both servers' identities, the
  copy progress, the last error and since when, and the lag cap. It also creates
  `bloem_search_mirror_dead_letters` and `bloem_search_mirror_processes`. The lease
  table also holds `main_target`, the id of the identity marker of the main server the
  process talks to, which the settle replay reads.
- `migrations/sql/20261111083321_bloem_search_mirror_tx_index.sql` builds
  `idx_catalog_search_index_events_tx` on `(tx_id, id)` with `CREATE INDEX
  CONCURRENTLY`, in a no-transaction migration, after dropping an invalid leftover of
  an earlier failed build.
- Both new tables are in `bloemLoggedTables` in `internal/backup/bloem_classification.go`,
  so dumps leave their rows out, like the outbox. A restore already deletes every
  `catalog_search_index_state` row (`internal/backup/interrupt.go`), so a restore
  resets the mirror and it makes a full copy. No restore hook was added.
- The cost to an operator without a mirror is 8 bytes per outbox row and one more
  index on that table. Nobody without Meilisearch writes outbox rows at all.

### The snapshot window

- Each run takes `pg_current_snapshot()`. The rows to apply are those whose `tx_id` is
  visible in the new snapshot (`pg_visible_in_snapshot()`) but not in the bookmark. The
  index range runs from the bookmark's oldest running transaction
  (`pg_snapshot_xmin()`) to the new snapshot's next transaction (`pg_snapshot_xmax()`).
- A large window is paged inside by `(tx_id, id)`, ordered by the numeric `xid8`
  column and never by its text, so `idx_catalog_search_index_events_tx` serves each
  page and the page stops at its size. The resume point `(tx_id, id)` is saved with
  the page. The bookmark moves only when the window is drained, so a restart
  in the middle of a catch-up, such as the daily deploy while the mirror is hours behind,
  continues exactly where it stopped and skips no row.
- The page size is the live sync batch size. Documents go in payload batches
  (`catalogSearchDocumentPayloadBatches`, bounded by `meilisearchMaxDocumentPayloadBytes`),
  as the main index sends them.

### Timings

| What | Value |
| --- | --- |
| Runs | At startup and every 60 s; each run applies pages for at most 5 min |
| Page size | The live sync batch size; copy batches use the rebuild batch size |
| Meilisearch tasks in flight during a copy | At most 2 |
| Retry wait | 30 s times the tries, up to 15 min; 10 tries, then the page is split |
| Dead-letter retry | Hourly |
| Settle quiet time | 2 min; a warning after 30 min |
| Process lease | Touched every 15 s |
| Metrics | Sampled every 30 s; samples older than 90 s are left out; the pending count has its own 10 s budget |
| Lag cap | `SILO_MEILISEARCH_MIRROR_MAX_LAG`, 72 h by default, at most 20 h while Silo switching is enabled |

### Publishing a copy on Meilisearch 1.56

The calls are pinned by contract tests that run against an in-process fake always and
against `getmeili/meilisearch:v1.56.0` when `BLOEM_TEST_MEILISEARCH_URL` and
`BLOEM_TEST_MEILISEARCH_MASTER_KEY` are set (`TestSearchMirrorContractPublishToNewName`,
`TestSearchMirrorContractPublishOverExisting`, `TestSearchMirrorContractMarkerRoundTrip`,
`TestSearchMirrorContractErrorKinds`, `TestSearchMirrorContractKeyActionsAreEnough`).
What 1.56 does:

- `swap-indexes` with `"rename": true` works only when the target does not exist. Onto
  an existing index it fails with `index_already_exists`, and a swap with a missing
  index fails with `index_not_found`. So `publishIndex` renames the side index when the
  target is absent. Otherwise it swaps with `"rename": false`, which leaves the old
  contents under the side name, and then deletes the side index.
- A key limited to `<prefix>*` cannot read a swap task, because the task has no index
  uid: `GET /tasks/{uid}` answers `task_not_found`. So `publishIndex` never reads it. It
  enqueues the deletion of a document no item has (`bloem_publish_fence`) from the
  target and waits for that. Meilisearch handles a swap before any index task enqueued
  after it, so once the deletion is done the swap has been processed. When the rename
  did not happen, the deletion fails with `index_not_found`, which is expected and
  ignored.
- After that, `publishIndex` checks that the target's `createdAt` is the one the side
  index had before. Only then does it delete the side index, so a swap that did not
  take effect never loses the copy. Callers wait for every task on the side index
  before they publish it.
- Deleting an index that does not exist, or creating one that exists, returns 202 and
  then fails as a task. The marker write therefore does not wait for the creation task.
  Tasks on one index run in the order they were enqueued, so the marker document is
  stored after the creation either way.
- The marker is read back with Meilisearch's own `index_not_found` and
  `document_not_found` codes meaning "no identity". A bare 404 from something in front
  of Meilisearch is an error, because reading "no identity" would make the mirror
  write a new one.

### How errors are sorted

`classifySearchMirrorError` sorts every Meilisearch error:

| Kind | What it covers | Metric `kind` |
| --- | --- | --- |
| `unreachable` | A network error, a deadline, a canceled context, HTTP 5xx, an answer that cannot be decoded | `unreachable` |
| `key_rejected` | HTTP 401 and 403 | `rejected` |
| `rejected` | A failed or canceled task, and any other 4xx | `rejected` |
| `database` | A Postgres error, classified by the caller | `database` |

A canceled context counts as `unreachable` so that a shutdown in the middle of a page
never costs a try or moves a bookmark.

### Key actions

A mirror key can be the server's master key, or a dedicated key limited to the indexes
`<prefix>*` and to the actions in `searchMirrorKeyActions` (in
`internal/catalog/bloem_search_mirror_meili_test.go`, proved enough on a real server by
`TestSearchMirrorContractKeyActionsAreEnough`):

| Actions | Used for |
| --- | --- |
| `indexes.create`, `indexes.delete`, `indexes.swap` | Side indexes, publishing, the marker index |
| `indexes.get` | Listing the mirror's indexes for cleanup after a copy |
| `documents.add`, `documents.delete` | Applying changes and filling a copy |
| `documents.get` | Reading the marker |
| `settings.update` | Giving a copy the main index's settings |
| `tasks.get` | Waiting for Meilisearch tasks |
| `stats.get` | Asking whether an index exists |

`indexes.get` is the easiest to miss: the cleanup after a copy lists the mirror's
indexes with it. `<prefix>*` covers the real index, the side indexes and the marker
index.

### Invariants

- The mirror never writes to the main Meilisearch's catalog index. The only writes to
  the main server are its identity marker and the deletion of its own leftover
  `<prefix>_mirrorcopy_*` indexes, after a swap and once at each process start. It
  never deletes the main's active index or any other name.
- The mirror never takes `searchIndexMaintenanceLockID`, except inside the swap step.
  Its own advisory lock, its own database connection and its own Meilisearch client
  keep it apart from the main sync.
- A mirror never changes the main index's sync: with the mirror down or slow, the main
  sync's runs, timings and results are unchanged.
- The cleanup wrapper's copy of Silo's query must stay identical to Silo's
  `PruneProcessed`. `TestSearchMirrorPrunePinsSiloQuery` fails when Silo changes it:
  update `searchMirrorSiloPruneSQL` in `bloem_search_mirror_prune.go` and re-check the
  hold.
- The mirror's row is saved by compare-and-set on its generation counter, so a run
  that no longer owns the mirror never overwrites a newer row.
- The wrapper is always installed, even without a variable. With no mirror row it calls
  Silo's own `PruneProcessed`. A process without the variables therefore still holds
  rows that another, configured process needs.
- The state label set is exactly the eight values above. Alert rules depend on them.
- The liveness signal for the settle replay is the mirror's own lease table, not Silo's
  `node_heartbeats`. That table has one row per node id, and two copies that share a
  compose file may share a node id. Every process with the mirror configured, which
  are the processes that run Silo's main sync, writes its lease every 15 seconds.
- The lease also says which main server the process talks to (`main_target`, the id of
  that main's identity marker). The settle replay waits only for live processes that
  talk to another main than the new one, or that started before the swap and have
  reported none yet, so a copy may run any number of processes.

### Tests

- Database tests each get their own schema, which holds `LIKE public.… INCLUDING ALL`
  copies of the outbox, state, dead-letter and lease tables. The pool's `search_path`
  is `<schema>,public`, so real Silo SQL and real snapshots run in isolation.
- The Meilisearch tests use in-process fake servers. The contract tests above run the
  same cases against the fake and, when configured, a real server, which keeps the fake
  true to 1.56.
- The tests that exist cover:
  - the settings (`bloem_search_mirror_config_test.go`): off when the address is unset
    or blank, the key file trimmed, an empty key file meaning no key, each problem and
    only the first one reported, addresses compared after normalising, the lag cap
    parsed, the address redacted, and no fallback to the main key;
  - the database changes (`bloem_search_mirror_schema_db_test.go`): `tx_id` is the
    writing transaction and separates transactions, the column is nullable with its
    default, the index is valid, and the state column and both new tables exist;
  - the state and its harness (`bloem_search_mirror_state_db_test.go`): the row's
    round trip, an absent row, a restore deleting it, the held snapshot, snapshot
    parsing, the process lease keeping its start time, finding an older live process,
    the lag cap while Silo switching is enabled, the default options, and the test
    environment's isolated schemas, main sync and fake documents;
  - Meilisearch (`bloem_search_mirror_meili_test.go`): the contract tests above, how
    errors are sorted, the fake's helpers, and a publish that keeps the side index
    when the swap fails;
  - the cleanup's copy of Silo's query (`bloem_search_mirror_prune_pin_test.go`):
    `TestSearchMirrorPrunePinsSiloQuery` reads `PruneProcessed` out of
    `search_index_repo.go` and compares its query, white space aside, with
    `searchMirrorSiloPruneSQL`. It needs no database. It fails with "Silo changed
    PruneProcessed; update searchMirrorSiloPruneSQL in bloem_search_mirror_prune.go and
    re-check the hold";
  - the wiring (`cmd/silo/bloem_search_mirror_wire_test.go`), which runs for every
    operator:
    - `TestBloemWireSearchMirrorOffRegistersNothing`: with the mirror variables unset,
      no mirror task, no log line, and Silo's `cleanup_catalog_search_index_events` step
      is still replaced, in place, by the mirror-aware one;
    - `TestBloemWireSearchMirrorBadConfigNeverStopsSilo`: for each of the five problems,
      the wiring returns, registers no task, logs exactly one error line, and that line
      holds no key, key file content or password;
    - `TestBloemWireSearchMirrorConfiguredRegistersBoth`: both tasks are registered and
      the process writes its lease. It needs the test database and skips when
      `SILO_TEST_DATABASE_URL` is unset;
  - the web: `web/src/bloem/admin/SearchMirrorStatus.test.tsx` (what the section shows
    in each state, the warnings, the copy progress, the capped count, the last error
    and the dead-letters, and nothing outside a Platform context) and
    `web/src/hooks/queries/admin/searchMirror.test.tsx` (the query runs only in a
    Platform context, bound to its authority).
- Following, full copies, failover, settle, what the cleanup does with a mirror row,
  failure handling, the metrics and the status route have no tests yet. That was a
  deliberate decision for this release, not an oversight.

### Related pages

- [How search finds titles by words and by meaning](bloem-search-and-embeddings.md):
  the main index the mirror copies.
- [How Silo backend switching works](silo-backend-switching.md): the flag that caps the
  mirror's lag.
- [Switching between Silo and Bloem](bloem-silo-switching-explained.md): the plain
  tour of taking turns on one database.
- [How Bloem keeps up with Silo](bloem-following-silo.md): why Silo's files stay
  untouched and where Bloem keeps its own work.
- [Writing Bloem documentation](../bloem/documentation-workflow.md): the shape of this
  page.
