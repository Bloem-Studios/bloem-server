# Bloem overlay: Cross-platform settings contract

**What this overlays.** Silo's
[`docs/architecture/settings-contract.md`](../../../architecture/settings-contract.md).
Bloem adds one paragraph to the section “Canonical artifact” and replaces
nothing. An *overlay* is a Bloem-owned page that records Bloem's additions to,
or replacements of, sections of a Silo-owned document. The Silo file stays
exactly as Silo wrote it. See the
[documentation workflow](../../documentation-workflow.md).

**Why Bloem differs.** Silo's repository commits the manifest and its Go and web
bindings. Bloem also commits the Kotlin and Swift bindings, which its Android
and Apple clients copy, and checks them for drift (`8331fe5c2`, 2026-09-04).
The paragraph moved out of the Silo file into this overlay in `47ea7c889`
(2026-09-23).

**Reviewed** on 2026-10-27 against Silo `3e2d306b2` and Bloem `main` at
`e852f92bf` (`Makefile`, `Makefile.bloem`, `contracts/settings/v1/`). Since this
overlay was written (against Silo `8e5ae2f46`), Silo changed the document twice:
`668a431b7` (#1740, 2026-10-02) adds a profile switch to turn off title art, and
`1ca1ba5b3` (#2365, 2026-10-19) adds administrator defaults (`admin_default`,
`defaults.<key>`, `config.AdminDefaults`) and the profile-ahead-of-device
resolution order. Both apply to Bloem; for how administrator defaults behave
with Bloem's organizations, see the
[settings API overlay](../settings-api.md).

Index: [Bloem documentation](../../README.md).

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
