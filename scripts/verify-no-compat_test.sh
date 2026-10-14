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
# Ripgrep's ignore handling must not hide a reintroduced import: ignore files,
# hidden paths, Git's own ignore rules or a ripgrep configuration file.
reintroduced_import() {
 mkdir -p "$(dirname -- "$1")"
 printf 'package native\nimport "github.com/Silo-Server/silo-server/internal/jellycompat"\n' > "$1"
}
for ignore_file in .ignore .rgignore; do
 reintroduced_import "$scratch/internal/native.go"
 printf 'native.go\n' > "$scratch/internal/$ignore_file"
 if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo "gate accepted an import hidden by $ignore_file" >&2; exit 1; fi
 rm "$scratch/internal/native.go" "$scratch/internal/$ignore_file"
done
reintroduced_import "$scratch/internal/.hidden/native.go"
if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted an import in a hidden directory' >&2; exit 1; fi
rm -r "$scratch/internal/.hidden"
# Vite bundles modules from a hidden directory when another module imports them.
mkdir -p "$scratch/web/src/.compat"
printf "export const route = 'platform/compatibility'\n" > "$scratch/web/src/.compat/routes.ts"
if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted compatibility UI in a hidden web directory' >&2; exit 1; fi
rm -r "$scratch/web/src/.compat"
reintroduced_import "$scratch/internal/native.go"
printf -- '--glob=!*.go\n' > "$scratch/rg-config"
if (cd "$scratch" && RIPGREP_CONFIG_PATH="$scratch/rg-config" bash gate.sh > /dev/null 2>&1); then echo 'gate accepted an import hidden by RIPGREP_CONFIG_PATH' >&2; exit 1; fi
rm "$scratch/rg-config"
if command -v git > /dev/null 2>&1; then
 git -C "$scratch" init -q
 printf 'native.go\n' > "$scratch/internal/.gitignore"
 if (cd "$scratch" && bash gate.sh > /dev/null 2>&1); then echo 'gate accepted an import hidden by .gitignore' >&2; exit 1; fi
 rm -rf "$scratch/.git" "$scratch/internal/.gitignore"
fi
rm "$scratch/internal/native.go"
(cd "$scratch" && bash gate.sh)
# Without ripgrep, or when a search errors, the content checks cannot tell a
# clean tree from a reintroduction, so the gate must fail rather than pass.
mkdir -p "$scratch/no-rg" "$scratch/broken-rg"
if (cd "$scratch" && PATH="$scratch/no-rg" "$BASH" gate.sh > /dev/null 2>&1); then echo 'gate passed without ripgrep' >&2; exit 1; fi
printf '#!/bin/sh\nexit 2\n' > "$scratch/broken-rg/rg"
chmod +x "$scratch/broken-rg/rg"
if (cd "$scratch" && PATH="$scratch/broken-rg:$PATH" bash gate.sh > /dev/null 2>&1); then echo 'gate passed when ripgrep failed' >&2; exit 1; fi
echo 'PASS: clean tree, removed package/page paths, removed import fixtures, ignore rules and missing or failing ripgrep'
