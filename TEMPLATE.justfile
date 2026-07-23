# ─────────────────────────────────────────────────────────
# TEMPLATE: goojust UI Integration Patterns
# ─────────────────────────────────────────────────────────
# Recipes source the shipped helpers for UI functions:
#
#   source /usr/share/goojust/goojust-helpers.sh
#
# Available functions:
#   progNew                      show progress bar at 0%
#   progUpdate <pct> [label]     update progress + optional label
#   progClear                    hide progress bar, reset to 0
#   prompt <text> [secret]       text/password input → stdout
#   choose <prompt> <a|b|c>      option selector → stdout
#   confirm <text> [OK|Cancel] [clear]  two-button → stdout
#   summary <text>               queue a summary line
#   summary_show                 display summary lines immediately
#   summary_clear                clear all summary lines
#   cliHide                      hide CLI output pane
#   cliShow                      show CLI output pane
#
# The only justfile attribute used is [group('Category')] to
# organise recipes in the menu. All UI feedback is driven at
# runtime by the helpers above.
# ─────────────────────────────────────────────────────────
set allow-duplicate-recipes := true
set ignore-comments := true

_default:
    @just --justfile '{{ justfile() }}' --list --list-heading $'Available commands:\n' --list-prefix $' - '

# ─── Progress + Summary + Confirm ────────────────────────
# summary() accumulates lines during execution. summary_show
# makes them visible immediately (useful mid-script between
# stages). confirm with clear=1 also shows them above the
# prompt. Lines also render on successful recipe completion.
[group('System')]
update:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    echo "==> Updating system image..."
    progNew
    progUpdate 10 "bootc upgrade"
    bootc upgrade
    progUpdate 60 "flatpak update"
    flatpak update -y
    progUpdate 100 "Complete"
    progClear
    summary "System: image-20260722 → image-20260723"
    summary "Flatpak: org.example.App 1.0 → 1.1"
    summary_show
    if [[ "$(confirm "Restart now to apply updates?" "Restart now|Later" 1)" == "Restart now" ]]; then
        systemctl reboot
    fi

# ─── Silent + Confirm ────────────────────────────────────
# cliHide suppresses the CLI overlay. Use confirm() inline
# for the yes/no prompt.
[group('Power')]
reboot:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    cliHide
    if [[ "$(confirm "Reboot now?")" == "Proceed" ]]; then
        systemctl reboot
    else
        echo "Cancelled."
    fi

[group('Power')]
shutdown:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    cliHide
    if [[ "$(confirm "Shut down now?")" == "Proceed" ]]; then
        systemctl poweroff
    else
        echo "Cancelled."
    fi

# ─── Text + Password Input ───────────────────────────────
[group('Configuration')]
setup-vpn:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    user=$(prompt "VPN username:")
    pass=$(prompt "VPN password:" true)
    echo "Configuring VPN for $user..."

# ─── Option Select + Confirm ─────────────────────────────
[group('System')]
build:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    flavor=$(choose "Select flavor?" "desktop|laptop|server")
    if [[ "$(confirm "Build $flavor image?")" == "Proceed" ]]; then
        progNew
        progUpdate 10 "Building..."
        echo "Building $flavor image..."
        progUpdate 100 "Complete"
        progClear
        echo "$flavor image built."
    fi

# ─── Parameter + Prompt + Progress ───────────────────────
[group('System')]
install PACKAGE:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    version=$(prompt "Version:")
    progNew
    progUpdate 10 "Installing..."
    echo "Installing {{PACKAGE}}@$version..."
    progUpdate 100 "Complete"
    progClear
    echo "{{PACKAGE}} installed."

# Personal additions
import? "~/.config/just/user.justfile"
