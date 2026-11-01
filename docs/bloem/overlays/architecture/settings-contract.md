# Bloem overlay: Cross-platform settings contract

> **Provenance and status.** Bloem page, rewritten on 2026-11-01 and checked against
> `main` at `b6039036d`, which includes Silo through `63ef34b23`. It lists what Bloem
> adds to Silo's
> [`docs/architecture/settings-contract.md`](../../../architecture/settings-contract.md),
> which stays exactly as Silo wrote it. Bloem replaces nothing there. The addition
> is in the [For developers](#for-developers) section.

Every setting the server and its apps share is described once, in a **settings
manifest**, and each app gets a generated file listing the setting names, a
**binding**. Silo's repository keeps the bindings for the server and the web app.
Bloem also keeps the Android (Kotlin) and Apple (Swift) bindings in this repository,
so its own apps copy them from the server rather than generating their own, and CI
checks that they match the manifest.

## What Bloem changes

- **Two more bindings live here.** The Kotlin and Swift bindings sit beside the Go
  and web ones and are regenerated together.
- **Copying to the apps is a separate step.** Regenerating never touches the app
  repositories unless asked to.
- **A matching file is not an app release.** That the server's bindings are current
  does not mean the Android or Apple app has adopted them.

## Words used in the code

| On this page | In the repository |
| --- | --- |
| Settings manifest | `contracts/settings/v1/` |
| Kotlin binding | `contracts/settings/v1/kotlin/SettingKeys.kt` |
| Swift binding | `contracts/settings/v1/swift/SettingKeys.generated.swift` |
| Regenerate | `make settings-bindings` (with `settings-bindings-native` from `Makefile.bloem`) |
| Check | `make verify-settings-bindings-all` |
| Copy to the apps | `SYNC_CLIENTS=1` or `make client-copies-settings` |

## For developers

### Where this overlay came from

Bloem began committing the Kotlin and Swift bindings, which its Android and Apple
clients copy, and checking them for drift in `8331fe5c2` (2026-09-04). The
paragraph moved out of the Silo file into this overlay in `47ea7c889`
(2026-09-23). It was reviewed on 2026-10-27 against Silo `3e2d306b2` and Bloem
`main` at `e852f92bf` (`Makefile`, `Makefile.bloem`, `contracts/settings/v1/`).
Since this overlay was written (against Silo `8e5ae2f46`), Silo changed the
document twice: `668a431b7` (#1740, 2026-10-02) adds a profile switch to turn off
title art, and `1ca1ba5b3` (#2365, 2026-10-19) adds administrator defaults
(`admin_default`, `defaults.<key>`, `config.AdminDefaults`) and the
profile-ahead-of-device resolution order. Both apply to Bloem; for how
administrator defaults behave with Bloem's organizations, see the
[settings API overlay](../settings-api.md). Silo has not changed the document
again through `63ef34b23`.

### Section by section

---

**Location:** section “Canonical artifact”, after the paragraph beginning
“Clients vendor a pinned copy of the manifest and generate…”. **Bloem adds:**

This repository also commits the Kotlin and Swift bindings,
`contracts/settings/v1/kotlin/SettingKeys.kt` and
`contracts/settings/v1/swift/SettingKeys.generated.swift`, next to the Go and web
bindings. `make settings-bindings` regenerates all of them: in Bloem it depends
on `settings-bindings-native` from `Makefile.bloem`, which runs
`cmd/settingsgen` for Kotlin and Swift. Copying the bindings into sibling client
checkouts happens only on request (`SYNC_CLIENTS=1` or
`make client-copies-settings`). `make verify-settings-bindings-all` checks the Go,
web and native bindings for drift. A generator refresh for existing manifest
values does not itself change the manifest revision. Server-side artifact
parity does not show that the Apple or Android clients have adopted or released
the new bindings.

### Related pages

- [Settings API overlay](../settings-api.md).
- [Writing Bloem documentation](../../documentation-workflow.md#api-and-swagger-sources):
  where each binding comes from.
- Index: [Bloem documentation](../../README.md).
