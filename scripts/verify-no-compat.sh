#!/usr/bin/env bash
# The caller's cwd is the repository root (also permits isolated gate fixtures).
set -euo pipefail
for path in \
 internal/jellycompat internal/compatgateway internal/compatapi internal/compatapp internal/compatcontract \
 internal/audiobooks/abs internal/audiobooks/abssocket \
 docker-compose.jellyfin.yml docker-compose.audiobookshelf.yml docker-compose.compat-diagnostics.yml \
 web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx \
 web/src/pages/admin-platform/CompatibilityApplicationsPage.tsx; do
 if [[ -e "$path" ]]; then echo "removed compatibility path reintroduced: $path" >&2; exit 1; fi
done
if rg -n '"github.com/Silo-Server/silo-server/internal/(jellycompat|compatgateway|compatapi|compatapp|compatcontract|audiobooks/abs|audiobooks/abssocket)(/[^" ]*)?"' cmd internal --glob '*.go'; then
 echo 'removed compatibility import reintroduced' >&2
 exit 1
fi
if [[ -d internal/config ]] && rg -n '"(jellyfin_compat|audiobookshelf_compat|audiobooks\.abs)\.' internal/config --glob '*.go' --glob '!**/*test.go'; then
 echo 'removed compatibility settings registered' >&2
 exit 1
fi
if [[ -d web/src ]] && rg -n 'platform/compatibility|Jellyfin-compatible app|jellyfin_compat\.' web/src --glob '*.{ts,tsx}' --glob '!*.test.*'; then
 echo 'removed compatibility UI reintroduced' >&2
 exit 1
fi
if rg -n 'Listener(Jellyfin|ABS)|DefaultPort(Jellyfin|ABS)|serveABSCompat|absCompatServer' cmd/silo internal/pluginhost --glob '*.go' --glob '!**/*test.go'; then
 echo 'removed compatibility listener reintroduced' >&2
 exit 1
fi
