# Example justfile: goojust UI Integration Patterns
# Recipes source the shipped helpers for UI functions:
#
#   source /usr/share/goojust/goojust-helpers.sh
#
# Available functions:
#   prog <pct> [label]                  set progress bar (0-100)
#   prog clear                          hide progress bar
#   prompt <text> [secret]              text/password input > stdout
#   choose <prompt> <a|b|c>             option selector > stdout
#   confirm <text> [OK|Cancel] [clear]  two-button > stdout
#   summary <text>                      add a summary line
#   summary_show                        display accumulated summary lines
#
# Attributes
# These are to be placed just above the recipe): 
#   [group('Category')]                 the group it will be displayed in the menu
#   [silent]                            suppress CLI overlay
#   [confirm("text")]                   run a confirmation popup
#   [progress]                          enable progress bar + spinner
#   [select("param:opt1|opt2")]         parameter dropdown instead of text input

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
[progress]
update:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    echo "==> Updating system image..."
    prog 10 "bootc upgrade"
    bootc upgrade
    prog 60 "flatpak update"
    flatpak update -y
    prog 100 "Complete"
    prog clear
    summary "System: falcos-20260722 → falcos-20260723"
    summary "Flatpak: org.example.App 1.0 → 1.1"
    summary_show
    if [[ "$(confirm "Restart now to apply updates?" "Restart now|Later" 1)" == "Restart now" ]]; then
        systemctl reboot
    fi

# ─── Silent + Confirm ────────────────────────────────────
# [silent] suppresses the CLI overlay. Use confirm() inline
# for the yes/no prompt.
[group('Power')]
[silent]
reboot:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    if [[ "$(confirm "Reboot now?")" == "Proceed" ]]; then
        systemctl reboot
    else
        echo "Cancelled."
    fi

[group('Power')]
[silent]
shutdown:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
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
[progress]
build:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    flavor=$(choose "Select flavor?" "desktop|laptop|server")
    if [[ "$(confirm "Build $flavor image?")" == "Proceed" ]]; then
        prog 10 "Building..."
        echo "Building $flavor image..."
        prog 100 "Complete"
        prog clear
        echo "$flavor image built."
    fi

# ─── Parameter + Prompt + Progress ───────────────────────
[group('System')]
[progress]
install PACKAGE:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    version=$(prompt "Version:")
    prog 10 "Installing..."
    echo "Installing {{PACKAGE}}@$version..."
    prog 100 "Complete"
    prog clear
    echo "{{PACKAGE}} installed."

# Personal additions
import? "~/.config/just/user.justfile"
