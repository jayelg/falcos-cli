# ─────────────────────────────────────────────────────────
# TEMPLATE: falcos-cli UI Integration Patterns
# ─────────────────────────────────────────────────────────
# All patterns use runtime-driven OSC protocol so the
# recipe script controls UI interactions at any point.
# Inline helpers (paste at top of any recipe):
#
#   prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
#   prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }
#   choose() { printf '\e]9;6;%s;%s\e\\' "$1" "$2" >&2; read -r c; echo "$c"; }
#   confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }
#
# See scripts/falcos-helpers.sh for standalone copies.
# ─────────────────────────────────────────────────────────
set allow-duplicate-recipes := true
set ignore-comments := true

_default:
    @just --justfile '{{ justfile() }}' --list --list-heading $'Available commands:\n' --list-prefix $' - '

# ─── Pattern: Progress ──────────────────────────────────
[group('System')]
[progress]
update:
    #!/usr/bin/bash
    prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
    echo "Step 1..."
    prog 25
    sleep 1
    echo "Step 2..."
    prog 50
    sleep 1
    echo "Step 3..."
    prog 75
    sleep 1
    echo "Complete."
    prog 100
    prog clear

# ─── Pattern: Silent + Inline Confirm ───────────────────
# [silent] suppresses the CLI overlay. Use confirm() inline.

[group('Power')]
[silent]
reboot:
    #!/usr/bin/bash
    confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }
    if [[ "$(confirm "Reboot now?")" == "Proceed" ]]; then
        systemctl reboot
    else
        echo "Cancelled."
    fi

[group('Power')]
[silent]
shutdown:
    #!/usr/bin/bash
    confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }
    if [[ "$(confirm "Shut down now?")" == "Proceed" ]]; then
        systemctl poweroff
    else
        echo "Cancelled."
    fi

# ─── Pattern: Runtime Text Input (OSC 9;5) ──────────────
[group('Configuration')]
setup-vpn:
    #!/usr/bin/bash
    prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }
    user=$(prompt "VPN username:")
    pass=$(prompt "VPN password:" true)
    echo "Configuring VPN for $user..."

# ─── Pattern: Progress + Confirm + Clear (OSC 9;7) ──────
# Pass 1 as 3rd arg to clear CLI output before showing popup.
[group('System')]
[progress]
update-with-restart:
    #!/usr/bin/bash
    prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
    confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }

    echo "==> Updating system image..."
    prog 10 "bootc upgrade"
    sleep 2
    prog 40 "bootc upgrade"

    echo "==> Updating flatpaks..."
    prog 50 "flatpak update"
    sleep 2
    prog 80 "flatpak update"

    echo "==> Complete"
    prog 100 "Complete"

    if [[ "$(confirm "Restart now?" "Restart now|Close" 1)" == "Restart now" ]]; then
        systemctl reboot
    fi
    prog clear

# ─── Pattern: Runtime Option Select (OSC 9;6) ──────────
[group('System')]
[progress]
build:
    #!/usr/bin/bash
    prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
    choose() { printf '\e]9;6;%s;%s\e\\' "$1" "$2" >&2; read -r c; echo "$c"; }
    confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }

    flavor=$(choose "Select flavor?" "desktop|laptop|server")
    if [[ "$(confirm "Build $flavor image?")" == "Proceed" ]]; then
        prog 10 "Building..."
        echo "Building $flavor image..."
        sleep 2
        prog 80 "Building..."
        echo "Finalising..."
        sleep 1
        prog 100 "Complete"
        prog clear
        echo "$flavor image built."
    fi

# ─── Pattern: Combined ───────────────────────────────────
[group('System')]
[progress]
install PACKAGE:
    #!/usr/bin/bash
    prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
    prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }

    version=$(prompt "Version:")
    prog 10 "Installing..."
    echo "Installing {{PACKAGE}}@$version..."
    sleep 1
    prog 60 "Installing..."
    echo "Finalising..."
    sleep 1
    prog 100 "Complete"
    prog clear
    echo "{{PACKAGE}} installed."

[group('System')]
[progress]
setup-dotfiles:
    #!/usr/bin/bash
    prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
    prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }

    echo "Configuring dotfiles..."
    prog 25
    repo=$(prompt "Dotfiles repo URL:")
    echo "Cloning $repo..."
    prog 75
    branch=$(prompt "Branch:")
    echo "Using branch $branch"
    prog 100
    prog clear

# Personal additions
import? "~/.config/just/user.justfile"
