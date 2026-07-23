#!/usr/bin/env bash
# Install goojust binary and runtime helpers into the filesystem.
# Designed to run inside a container build context (e.g. Dockerfile RUN)
# or on a live system. Uses the files bundled in the release tarball.
#
# Usage: install.sh [--dest /] [--bindir /usr/bin]
#
# Defaults install to / so the image paths match:
#   /usr/bin/goojust
#   /usr/libexec/goojust-progress
#   /usr/share/goojust/goojust-helpers.sh
set -euo pipefail

dest="${1:-/}"
bindir="${2:-/usr/bin}"

# Resolve script directory so we find sibling files regardless of
# how this script was invoked (directly, sourced, or piped).
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

install -Dm755 "${here}/goojust" "${dest}/${bindir}/goojust"
install -Dm755 "${here}/scripts/falcos-progress" "${dest}/usr/libexec/goojust-progress"
install -Dm644 "${here}/scripts/goojust-helpers.sh" "${dest}/usr/share/goojust/goojust-helpers.sh"

echo "Installed goojust, falcos-progress, goojust-helpers.sh"
