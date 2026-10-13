#!/usr/bin/env bash
set -euo pipefail
repo_root=$(cd -- "$(dirname -- "$0")/.." && pwd)
scratch=$(mktemp -d)
trap 'rm -rf "$scratch"' EXIT
mkdir -p "$scratch/cmd/silo" "$scratch/internal/pluginhost" "$scratch/internal/config" "$scratch/web/src/pages/admin-settings" "$scratch/web/src/pages/admin-platform"
cp "$repo_root/scripts/verify-no-compat.sh" "$scratch/gate.sh"
(cd "$scratch" && bash gate.sh)
for forbidden in internal/jellycompat internal/audiobooks/abs web/src/pages/admin-settings/CompatibilityProxiesSettings.tsx; do
 mkdir -p "$(dirname -- "$scratch/$forbidden")"
 touch "$scratch/$forbidden"
 if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo "gate accepted $forbidden" >&2; exit 1; fi
 rm "$scratch/$forbidden"
done
cat > "$scratch/internal/native.go" <<'GO'
package native
import "github.com/Silo-Server/silo-server/internal/compatgateway"
GO
if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted removed import' >&2; exit 1; fi
rm "$scratch/internal/native.go"
cat > "$scratch/internal/config/admin_settings.go" <<'GO'
package config
var removed = "audiobookshelf_compat.enabled"
GO
if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted removed setting' >&2; exit 1; fi
rm "$scratch/internal/config/admin_settings.go"
cat > "$scratch/internal/pluginhost/listener.go" <<'GO'
package pluginhost
const ListenerJellyfin = "jellyfin"
GO
if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted removed listener' >&2; exit 1; fi
rm "$scratch/internal/pluginhost/listener.go"
(cd "$scratch" && bash gate.sh)
echo 'PASS: clean tree, removed package/page paths and removed import fixtures'
