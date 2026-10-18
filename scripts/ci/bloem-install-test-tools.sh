#!/usr/bin/env bash
# Installs the tools Bloem's Go CI gates need on an Ubuntu runner.
#
# Usage (the repository root is the cwd):
#   scripts/ci/bloem-install-test-tools.sh [ripgrep] [ffmpeg]
#
#   ripgrep  scripts/generate-bloem-storage-proto_test.sh matches the
#            generator's output with rg.
#   ffmpeg   The native audio and video tests generate fixtures with real
#            FFmpeg and fail without it. This installs Jellyfin's FFmpeg build,
#            the family the runtime image ships, and puts it on PATH for later
#            steps through GITHUB_PATH.
#
# The runtime image runs jellyfin-ffmpeg8 (JELLYFIN_FFMPEG_VERSION in the
# Dockerfile). CI pins the 7.1 build because FFmpeg 8 prints a stream's display
# matrix as "Display Matrix: rotation of ...", which Silo's mediasample input
# parser does not read yet, so TestGenerateRotatedManifestEndToEndDB fails on
# FFmpeg 8. Move this pin to the Dockerfile's version once that parser accepts
# both spellings (docs/bloem/ci.md).
set -euo pipefail

ffmpeg_package=jellyfin-ffmpeg7
ffmpeg_version=7.1.4-3
ffmpeg_dir=/usr/lib/jellyfin-ffmpeg

want_ripgrep=false
want_ffmpeg=false
for tool in "$@"; do
  case "$tool" in
  ripgrep) want_ripgrep=true ;;
  ffmpeg) want_ffmpeg=true ;;
  *)
    echo "bloem-install-test-tools: unknown tool: $tool" >&2
    exit 2
    ;;
  esac
done

sudo=()
if [[ $(id -u) -ne 0 ]]; then
  sudo=(sudo)
fi

# shellcheck source=/dev/null
codename=$(. /etc/os-release && echo "${VERSION_CODENAME:?}")
arch=$(dpkg --print-architecture)
pinned="${ffmpeg_version}-${codename}"

# Only the distribution's own sources (and Jellyfin's, when needed) are read,
# as in the libvips install, so a slow or broken third-party list on the runner
# image cannot fail the job.
parts=$(mktemp -d)
trap 'rm -rf "$parts"' EXIT
apt_options=(-o "Dir::Etc::sourceparts=$parts")
if [[ -f /etc/apt/sources.list.d/ubuntu.sources ]]; then
  apt_options+=(-o Dir::Etc::sourcelist=/etc/apt/sources.list.d/ubuntu.sources)
fi

packages=()
if $want_ripgrep && ! command -v rg > /dev/null 2>&1; then
  packages+=(ripgrep)
fi
if $want_ffmpeg && [[ $(dpkg-query -W -f='${Version}' "$ffmpeg_package" 2> /dev/null || true) != "$pinned" ]]; then
  keyring=/usr/share/keyrings/bloem-ci-jellyfin.gpg
  curl -fsSL https://repo.jellyfin.org/jellyfin_team.gpg.key | gpg --dearmor > "$parts/jellyfin.gpg"
  "${sudo[@]}" install -m 0644 "$parts/jellyfin.gpg" "$keyring"
  echo "deb [signed-by=$keyring arch=$arch] https://repo.jellyfin.org/ubuntu $codename main" > "$parts/jellyfin.list"
  packages+=("$ffmpeg_package=$pinned")
fi

if ((${#packages[@]} > 0)); then
  "${sudo[@]}" apt-get "${apt_options[@]}" update
  "${sudo[@]}" env DEBIAN_FRONTEND=noninteractive apt-get "${apt_options[@]}" install -y --no-install-recommends "${packages[@]}"
fi

if $want_ripgrep; then
  rg --version | sed -n 1p
fi
if $want_ffmpeg; then
  "$ffmpeg_dir/ffmpeg" -hide_banner -version | sed -n 1p
  if [[ -n ${GITHUB_PATH:-} ]]; then
    echo "$ffmpeg_dir" >> "$GITHUB_PATH"
  fi
fi
