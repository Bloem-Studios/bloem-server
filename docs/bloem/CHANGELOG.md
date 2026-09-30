# Bloem integration changelog

This records Bloem's integration milestones. Silo's image build numbers and
upstream feature history remain separate from Bloem's application commits.

## September 30, 2026

Bloem `b2ad352a0` includes Silo upstream `8e2e84047`. The source merges preserve
upstream ancestry and retain 396 declared upstream-file seams.

### Inherited from Silo

The imported source includes account password-reset links and temporary-password
handling, age-normalized content restrictions, local and remote stream bitrate
limits, request routing and group limits, request follows and season selection,
real-time library monitoring, theme-song playback, and title-based watchlists.
These are upstream capabilities; use the relevant API's capability discovery
and the client's implemented workflows rather than assuming every client exposes
every feature.

The final follow-up adds bounded retries when a Trakt paginated listing changes
during a read: at most three attempts, restarting the inconsistent listing.
Other failures are returned without that consistency retry.

### Bloem integration

- Preserved organization membership authority and profile-aware policy through
  owned adapters, including the finalized-schema request-group migration fix.
- Retained account ownership fields during setup and shared profile readers
  during transactional lifecycle operations.
- Preserved the upstream playback/download call signatures with profile-aware
  Bloem extensions.
- Kept native deployment-readiness decoration and library relink behavior in
  the fork's integration hooks.
- Moved more web code behind lazy routes and kept Bloem's bundle allowance
  separate from upstream validation rules.

See [integration adapters](../architecture/bloem-upstream-adapters.md) for the
implementation boundary and [deployment and validation](../operations/2026-09-30-upstream-deployment.md)
for the exact running revision, migration correction, passed checks, and open
contract/scenario/UI validation work.

The later documentation refresh updates the GitHub README, contributor and
operator guides, API overlays, historical checkpoints, and links after upstream
documentation moves. A documentation commit does not replace the deployed binary.
