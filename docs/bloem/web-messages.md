# How Bloem's web text is translated

> **Provenance and status.** Bloem page, written on 2026-11-10 and checked against
> the source at `dc663607d` with Bloem's seven translations added. Bloem's message
> catalog is a Bloem addition that mirrors Silo's rules for its own catalog
> ([`web/src/i18n/README.md`](../../web/src/i18n/README.md)); Silo's catalog stays
> as Silo ships it. The technical rules are in the [For developers](#for-developers)
> section.

The web app speaks the viewer's language: Silo's screens through Silo's catalog,
and Bloem's screens and additions through Bloem's own. This page walks through
where Bloem's text lives, how a screen shows it, how to add a message, and how
translations are added and checked.

## Two catalogs, one language

A **message** is one piece of interface text with a **key** that names it, such as
`bloem.liveTv.page.historyTitle` ("History" on the Live TV page). A **catalog**
holds every message in English, with a description of where each one appears, and
its translations.

The web app has two catalogs:

- **Silo's catalog** holds Silo's text. It is maintained in Silo's translations
  project and copied into `web/src/locales/`. Bloem never adds to it or edits it.
- **Bloem's catalog** holds Bloem's text: every word on Bloem's own screens (Live
  TV, organizations, plans, campaigns, requests for books, the preview) and the
  Bloem additions on Silo's screens. It lives in this repository, under
  `web/src/bloem/i18n/catalog/`.

The viewer picks one **interface language** in Silo's language picker, and Bloem's
text follows it. Bloem is translated into the same languages as Silo: English,
Spanish, German, French, Brazilian Portuguese, Japanese, Simplified Chinese and
Arabic. A message that has no translation yet shows in English.

## Where the text lives

Bloem's catalog has one folder per language, named after its tag (`en`, `de`,
`pt-BR`, `zh-Hans`, …), and one file per **area** of the app in each folder: Live
TV, Live TV administration, plans, organizations, campaigns, requests, and so on.
Each language has the same file names, so `en/liveTv.arb` and `de/liveTv.arb` hold
the same messages. The English files also carry a description for every message;
a translation file holds only the translated text.

A **generator** turns the catalog into the files the app loads, in
`web/src/bloem/locales/`. Nobody edits those by hand.

## How a screen shows a message

1. The screen asks for a message by its key and gives it any values it needs, such
   as a channel name or a count.
2. It gets the message in the viewer's language if that translation has loaded,
   and the English message otherwise.
3. The values are filled in. A count picks the right word for that language:
   "1 account", "2 accounts", and the forms other languages need.

## Everyday text and admin text

Most of Bloem's text is only seen by administrators, so it does not need to load
for a viewer. Bloem splits its catalog in two, as Silo splits its own:

- **Everyday text** is every message whose key does not start with `bloem.admin.`.
  Its English ships with the app itself, so it is ready the moment a page opens.
- **Admin text** is every message whose key starts with `bloem.admin.`. It loads
  only when someone opens the admin pages; the admin pages wait for it before they
  show anything.

Translations of both parts load when they are needed.

So a key starts with `bloem.admin.` only when its text appears inside an admin page.
Text a viewer can see, and admin text shown outside the admin page area (the
sidebar, the header), keeps a key without that prefix, or it would appear empty
until an admin page has been opened.

## What a change must do

A change that adds or changes Bloem text:

1. **Adds the English message** to the right area file in `catalog/en/`, with a
   key and a description (below). New text never goes in as a plain English string
   in the code.
2. **Shows it with `bt(...)`** in a component, or `bloemT(...)` in plain code.
3. **Runs the generator** and commits the regenerated files with the change.
4. **Leaves English as it was** when moving existing text into the catalog: the
   screen must read exactly as before.
5. **Adds the translations**, or leaves them for a later change: an untranslated
   message shows in English until then.
6. **Passes the checks** below.

### Keys and descriptions

- A key is `bloem.` followed by lowerCamel segments: `bloem.<area>.<thing>`,
  for example `bloem.liveTv.seriesRules.allChannels` or
  `bloem.admin.plans.account.choosePlan`.
- Admin-page text starts `bloem.admin.` ([above](#everyday-text-and-admin-text)).
- Every message has a description that says where it appears and what each
  placeholder holds: "Live TV page: badge next to the heading. {count} is the
  number of channels."
- A sentence stays one message, with named placeholders for its values:
  `"Recording {title} on {channel}"`, never three pieces joined in code. A
  translator may need to put the values in a different order.
- A count is a **plural**: `{count, plural, one {# channel} other {# channels}}`,
  with the count passed as a number. Each language then uses its own forms.
- Bold or linked words inside a sentence are **tags**:
  `"Remove <b>{name}</b>?"`, filled in by the code.

### What stays English

Data is not interface text. Ids, enum values, keys, addresses, values sent to the
server and test ids stay English in the code; only the label a person reads comes
from the catalog.

## How translations are added and checked

A translator copies an English area file into the language's folder and replaces
each English text, keeping the key, the placeholders and the tags. The generator
then checks every language against English and refuses a catalog with:

- a key that is not shaped `bloem.<lowerCamel segments>`;
- an English message without a description, or a description without a message;
- a key that appears in two area files;
- a translation of a key English does not have;
- a translation whose placeholders differ from the English ones;
- a message that does not parse, or an ASCII apostrophe right before `{`, `}`, `#`
  or `|` (use `’` there).

Three more checks guard Bloem's text:

- **The coverage test** looks through Bloem's own areas of the web app and fails on
  any plain English text a screen shows: words written straight into a page, in a
  visible property such as a label, title or placeholder, or in a pop-up toast.
- **The freshness test** fails when the generated files are out of date with the
  catalog.
- **The pseudo-locales** show what a translation would do to the layout without a
  translator. **en-XA** shows every Bloem and Silo message accented, longer and in
  brackets ("History" becomes `⟦Ĥîšţöŕý~~~⟧`), so clipped or still-English text
  stands out. **ar-XB** shows English laid out right to left, so mirroring mistakes
  stand out. They appear in the language picker in development builds.

## Example: a new Live TV notice

This example is made up.

A change adds a notice to the Live TV guide: "3 channels have no guide data".

1. The writer adds `bloem.liveTv.page.channelsWithoutGuide` to
   `catalog/en/liveTv.arb` as
   `{count, plural, one {# channel has no guide data} other {# channels have no guide data}}`,
   described as "Live TV guide: notice above the grid. {count} is how many channels
   have no listings."
2. The guide shows it with `bt("bloem.liveTv.page.channelsWithoutGuide", { count })`.
   The key has no `admin.` because viewers see the guide.
3. The writer runs the generator. German, French and the other languages show the
   English notice until their files get the message.
4. A translator later adds the key to `de/liveTv.arb` with German plural forms, and
   the generator checks that it uses `{count}` as English does.

## Who can change what

| Who | What they change |
| --- | --- |
| A Bloem contributor | English messages and their descriptions in `catalog/en/`, and the code that shows them. |
| A translator | The translated text in `catalog/<tag>/`, keeping keys, placeholders and tags. |
| The generator | Everything in `web/src/bloem/locales/`. Change the catalog, then regenerate. |
| Silo | Silo's catalog in `web/src/locales/`. Bloem takes it in unchanged. |

## Words used in the code

| On this page | In the repository |
| --- | --- |
| Bloem's catalog | `web/src/bloem/i18n/catalog/<tag>/<area>.arb` |
| Silo's catalog | `web/src/locales/`, generated from Silo's translations project ([`web/src/i18n/README.md`](../../web/src/i18n/README.md)) |
| Message, key | an ARB entry; keys match `^bloem(\.[a-z][A-Za-z0-9]*)+$` |
| Description | the entry's `@<key>` object, `{ "description": "…" }`, in the English file only |
| The generator | `web/scripts/bloem-messages.mjs` |
| Generated files | `web/src/bloem/locales/<tag>.json`, `<tag>.admin.json` and `keys.ts` (`BloemMessageKey`, `BLOEM_LOCALES`) |
| Admin text | keys starting `bloem.admin.` (`BLOEM_ADMIN_PREFIX`) |
| Showing a message | `useBloemT()` in components, `bloemT()` in plain code (`web/src/bloem/i18n`) |
| The coverage test | `web/scripts/bloem-i18n-coverage.test.mjs` |
| The freshness test | `web/scripts/bloem-messages.test.mjs` |
| Pseudo-locales | `en-XA` and `ar-XB` |

## For developers

### The API

`web/src/bloem/i18n` sits beside Silo's `@/i18n` and exports:

- `useBloemT()` for components. It re-renders when the interface language changes
  and when more Bloem messages load. `bt(id, values)` returns a string, or React
  nodes when a value is a tag function:
  `bt("bloem.x.y", { b: (chunks) => <strong>{chunks}</strong>, name })`.
- `bloemT(id, values)` for plain code: helpers, error builders, label maps. Call it
  when the text is used, never at module scope. A module-level label map becomes a
  function or a getter.
- `bloemEnglishText(id, values)`: a message in English whatever the language, for
  search terms and other data.
- `useBloemAdminMessages()`: asks for the admin part and reports whether it is
  ready in the current language. `AdminScopedOutlet` (`web/src/bloem/BloemAdminShell.tsx`),
  the outlet of Silo's `AdminLayout`, renders nothing until it is, as Silo's layout
  waits for Silo's admin part. Asking for Silo's admin part asks for Bloem's too
  (`subscribeAdminRequest` in `web/src/bloem/i18n/catalog.ts`).
- `BloemMessageKey`: an unknown key fails the typecheck.

There is no `defaultMessage` in code: English comes from the generated
`en.json`/`en.admin.json`, so English has one source.

A Bloem screen that shows Silo's own wording may call Silo's `useT()` with Silo's
`web.*` key. Bloem never adds a key to Silo's catalog; new text goes into Bloem's.
`web/src/bloem/preview/adapters/i18nWithBloem.ts` gives a Silo module a `useT()`
that also formats `bloem.*` ids, for lists Bloem adds rows to.

### Loading and fallback

- `web/src/bloem/i18n/catalog.ts` imports `en.json` eagerly: it is the fallback for every message in
  every language and part of the launch bundle. `en.admin.json` and every other
  language's two parts are lazy chunks (`import.meta.glob`).
- Bloem follows Silo's formatter: in `format.ts`, `subscribeAppIntl` calls
  `setBloemLanguage` with each formatter's locale, and `catalogTag()` maps it to a catalog tag
  (`ar-u-nu-arab` → `ar`).
- A language without a message, or whose chunk failed to load (a newer release
  replaced it), shows that message in English with English plural rules, as Silo's
  does.
- An admin message formatted before its part has loaded renders `""` and asks for
  the part; `useBloemT` re-renders when it arrives. Under Vitest it throws
  `bloemT: <id> is not loaded` instead. `web/src/test-setup.ts` loads the English
  admin part (`loadBloemAdminEnglishForTests`), so tests render real English and
  keep asserting today's English.

### Formatting rules

`format.ts` prepares values the way Silo's `src/i18n/format.ts` does, and
`format.test.ts` checks the output against Silo's formatter:

- In a right-to-left language every printed string value is wrapped in Unicode
  isolates (U+2068 … U+2069); bare digit strings and `select`/`plural` selectors
  are not. English bytes never change.
- In a language's own translation, number values and the plural `#` print in that
  locale's digits. Pass a count as a number, not `String(n)`.
- Pass dates, durations and sizes already formatted, as strings.

### Catalog rules

- Keys match `^bloem(\.[a-z][A-Za-z0-9]*)+$`; `bloem.admin.*` only for text rendered
  inside the admin page outlet. Text in the sidebar, the header or any viewer page
  must not start with `bloem.admin.`, or it renders empty until the admin part loads.
- Every English message needs a non-empty `@<key>.description` saying where it
  appears and what each placeholder holds. Translation files carry no descriptions.
- Plurals are one `plural` spanning the message,
  `{count, plural, one {# account} other {# accounts}}`, with a number value.
  Moving existing text into the catalog keeps English byte-identical, except that a
  "1 accounts"-style mistake may become a correct plural.
- A sentence a value splits stays one message with named placeholders, never
  fragments such as `"."` or `":"` joined in code.
- Rich text uses tags (`<b>{name}</b>`), filled by functions in the values.
- An ASCII apostrophe right before `{`, `}`, `#` or `|` starts ICU quoting; the
  generator rejects it. Use `’`.
- Data stays English: ids, enum values, keys, URLs, API values, test ids and
  values sent to the server. Default names the app writes to the server (a new
  guide source's display name, for example) are data.

### The generator

From `web/`: `node scripts/bloem-messages.mjs` rewrites `src/bloem/locales/`;
`--check` writes nothing and fails when a file is stale.
`scripts/bloem-messages.test.mjs` (part of the web tests) fails the same way, and
also fails when the directory holds a file the generator no longer writes.

It merges each language's area files, compiles every message with
`@formatjs/icu-messageformat-parser` and checks the rules listed in
[How translations are added and checked](#how-translations-are-added-and-checked).
It writes per language `<tag>.json` (everyday text) and `<tag>.admin.json`
(`bloem.admin.*`), the two pseudo-locales generated from English, and `keys.ts`.
`BLOEM_LOCALES` there lists `en` and every language with a catalog folder.

The language picker offers Silo's languages (`LOCALES` in
`web/src/locales/keys.ts`), so Bloem translates into those. A new language is a new
`catalog/<tag>/` folder with the same area files, then a regenerate.

### Pseudo-locales

`en-XA` accents every letter, pads each message with `~` and brackets it in `⟦ ⟧`;
`ar-XB` wraps English literals in right-to-left overrides behind a right-to-left
mark. Both are bundled only in `vite dev` and in builds with
`VITE_PSEUDO_LOCALES=1` (`PSEUDO_LOCALES_ENABLED` in `catalog.ts` repeats Silo's
condition, so a production build drops them). Check a change under both: anything
still plain English is unconverted, and anything that does not mirror is laid out
with physical directions.

### The coverage test

`web/scripts/bloem-i18n-coverage.test.mjs` runs ESLint's Node API with an inline
configuration (no configuration file is read): the `typescript-eslint` parser,
`formatjs/no-literal-string-in-jsx` with the visible properties Silo checks
(`label`, `title`, `subtitle`, `description`, `help`, `placeholder`, `currentNote`,
`alt`, `tooltip` and `aria-*`), and the `no-restricted-syntax` rule against literal
toast messages from `web/eslint.config.js`. It covers Bloem's own areas:

- `src/bloem/**`, without tests and the generated `src/bloem/locales/`;
- `src/components/{engagement,livetv,seasonal}/**`;
- `src/pages/{admin-platform,admin-organization,livetv}/**`;
- `src/pages/{AdminLiveTV,LiveTV,LiveTVWatch,BloemHome,OrganizationAdminAccessGroups}.tsx`.

It fails listing each finding as `file:line:column rule: message`, and ESLint
throws when a pattern no longer matches a file. Bloem files inside Silo's
converted folders (`src/components/admin/**`, for example) are already checked by
`pnpm run lint`, through `I18N_CONVERTED` in Silo's `web/eslint.config.js`, which
Bloem does not edit. The test runs on Vitest's `threads` pool
(`THREADS_TESTS` in `web/vite.config.ts`): ESLint's module loading fails inside a
VM context.

Like Silo's rule, it cannot see text a plain helper returns; the pseudo-locales
can.

### The launch budget

Bloem's eager English (`en.json`) counts toward the launch bundle that
`pnpm run budget:check` measures after `pnpm exec vite build`. When it pushes the
launch bundle over, move keys under `bloem.admin.*` only where their text renders
inside the admin outlet.

### Verifying a change

From `web/`:

```sh
node scripts/bloem-messages.mjs --check
pnpm exec tsc -p tsconfig.app.json --noEmit
pnpm run lint
pnpm exec vitest run scripts src/bloem
pnpm exec vite build && pnpm run budget:check
```

### Related pages

- [Silo's translation rules](../../web/src/i18n/README.md), which Bloem's mirror.
- [What the web app covers](../architecture/bloem-web-feature-coverage.md).
- [Writing Bloem documentation](documentation-workflow.md).
