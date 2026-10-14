#!/usr/bin/env bash
# The caller's cwd is the repository root (also permits isolated gate fixtures).
set -euo pipefail
# The content checks below need ripgrep. Without it every search would look
# like "no match" and the gate would pass vacuously, so refuse to run instead.
if ! command -v rg > /dev/null 2>&1; then
 echo 'verify-no-compat: ripgrep (rg) is required but was not found on PATH' >&2
 exit 2
fi
# forbid MESSAGE RG_ARGS...: fail when rg finds a match. rg exits 0 on a match,
# 1 on none and 2 or more on an error; only "none" passes, so a broken search
# fails closed. Every file under the searched paths is read: ignore files
# (.gitignore, .ignore, .rgignore), hidden paths and a RIPGREP_CONFIG_PATH
# configuration cannot hide a reintroduction.
forbid() {
 local message=$1 status=0
 shift
 rg --no-config --no-ignore --hidden -n "$@" || status=$?
 case $status in
 0) echo "$message" >&2; exit 1 ;;
 1) ;;
 *) echo "verify-no-compat: rg failed with exit status $status" >&2; exit 2 ;;
 esac
}
for path in \
 internal/jellycompat internal/compatgateway internal/compatapi internal/compatapp internal/compatcontract \
 internal/audiobooks/abs internal/audiobooks/abssocket \
 docker-compose.jellyfin.yml docker-compose.audiobookshelf.yml docker-compose.compat-diagnostics.yml \
 web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx \
 web/src/pages/admin-platform/CompatibilityApplicationsPage.tsx; do
 if [[ -e "$path" ]]; then echo "removed compatibility path reintroduced: $path" >&2; exit 1; fi
done
forbid 'removed compatibility import reintroduced' \
 '"github.com/Silo-Server/silo-server/internal/(jellycompat|compatgateway|compatapi|compatapp|compatcontract|audiobooks/abs|audiobooks/abssocket)(/[^" ]*)?"' cmd internal --glob '*.go'
if [[ -d internal/config ]]; then
 forbid 'removed compatibility settings registered' \
  '"(jellyfin_compat|audiobookshelf_compat|audiobooks\.abs)\.' internal/config --glob '*.go' --glob '!**/*test.go'
fi
if [[ -d web/src ]]; then
 forbid 'removed compatibility UI reintroduced' \
  'platform/compatibility|Jellyfin-compatible app|jellyfin_compat\.' web/src --glob '*.{ts,tsx}' --glob '!*.test.*'
fi
forbid 'removed compatibility listener reintroduced' \
 'Listener(Jellyfin|ABS)|DefaultPort(Jellyfin|ABS)|serveABSCompat|absCompatServer' cmd/silo internal/pluginhost --glob '*.go' --glob '!**/*test.go'
