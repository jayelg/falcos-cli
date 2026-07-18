#!/usr/bin/env bash
# Install falcos-cli binary and runtime helpers into the filesystem.
# Designed to run inside a container build context (e.g. Dockerfile RUN)
# or on a live system. Uses the files bundled in the release tarball.
#
# Usage: install.sh [--dest /] [--bindir /usr/bin]
#
# Defaults install to / so the image paths match:
#   /usr/bin/falcos-cli
#   /usr/libexec/falcos-progress
#   /usr/share/falcos/falcos-helpers.sh
set -euo pipefail

dest="${1:-/}"
bindir="${2:-/usr/bin}"

# Resolve script directory so we find sibling files regardless of
# how this script was invoked (directly, sourced, or piped).
here="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

install -Dm755 "${here}/falcos-cli" "${dest}/${bindir}/falcos-cli"
install -Dm755 "${here}/scripts/falcos-progress" "${dest}/usr/libexec/falcos-progress"
install -Dm644 "${here}/scripts/falcos-helpers.sh" "${dest}/usr/share/falcos/falcos-helpers.sh"

echo "Installed falcos-cli, falcos-progress, falcos-helpers.sh"
