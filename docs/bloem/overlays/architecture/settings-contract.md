# Bloem overlay: Cross-platform settings contract

This overlay covers Silo's [`docs/architecture/settings-contract.md`](../../../architecture/settings-contract.md), which stays exactly as Silo wrote it.

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
