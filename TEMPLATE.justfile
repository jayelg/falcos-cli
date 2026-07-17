# ─────────────────────────────────────────────────────────
# TEMPLATE: falcos-cli UI Integration Patterns
# ─────────────────────────────────────────────────────────
# Illustrates each UI convention pattern with example
# recipes. The patterns combine freely.
#
# See README.md for full documentation.
# ─────────────────────────────────────────────────────────
set allow-duplicate-recipes := true
set ignore-comments := true

_default:
    @just --justfile '{{ justfile() }}' --list --list-heading $'Available commands:\n' --list-prefix $' - '

# ─── Pattern: Silent ─────────────────────────────────────
# No attributes needed. The recipe runs immediately on
# selection with no UI preamble.

# Show system information
[group('System')]
sysinfo:
    uname -a
    uptime

# List available disk space
[group('System')]
disk-usage:
    df -h

# ─── Pattern: Progress ──────────────────────────────────
# [progress] tells the TUI this recipe emits OSC 9;4
# sequences via falcos-progress. An indeterminate spinner
# animates until the first progress update arrives.

[group('System')]
[progress]
update:
    #!/usr/bin/bash
    prog=/usr/libexec/falcos-progress
    [ -x "$prog" ] || prog=:
    echo "Step 1..."
    "$prog" 25
    sleep 1
    echo "Step 2..."
    "$prog" 50
    sleep 1
    echo "Step 3..."
    "$prog" 75
    sleep 1
    "$prog" 100
    "$prog" clear
    echo "Done."

# ─── Pattern: Confirm ────────────────────────────────────
# [confirm("prompt")] shows a Proceed/Cancel popup before
# the recipe starts. Supports {{param}} placeholders.

[group('Power')]
[confirm("Reboot now?")]
reboot:
    systemctl reboot

[group('Power')]
[confirm("Shut down now?")]
shutdown:
    systemctl poweroff

[group('Power')]
[confirm("Reboot into UEFI setup?")]
reboot-uefi:
    if [ ! -d /sys/firmware/efi ]; then
        echo "Not a UEFI system."
        exit 1
    fi
    systemctl reboot --firmware-setup

# ─── Pattern: Input parameters ──────────────────────────
# Parameters in the recipe signature are collected by the
# TUI before running. All values are passed as arguments
# to `just`.

[group('Configuration')]
set-hostname NAME:
    hostnamectl hostname {{NAME}}

[group('Configuration')]
set-motd MESSAGE:
    echo "{{MESSAGE}}" > /etc/motd

# ─── Pattern: Select options ────────────────────────────
# [select("param:opt1|opt2|opt3")] shows a navigable list
# instead of a freeform text input for that parameter.

[group('Configuration')]
[select("SIZE:1 GiB|2 GiB|4 GiB|8 GiB")]
create-swap SIZE:
    fallocate -l {{SIZE}} /swapfile
    chmod 600 /swapfile
    mkswap /swapfile
    swapon /swapfile

[group('Configuration')]
[select("BACKEND:docker|podman")]
install-container-tool BACKEND:
    # Install the selected container runtime.
    echo "Installing {{BACKEND}}..."

# ─── Pattern: Combined ───────────────────────────────────
# All attributes combine freely on a single recipe.

[group('System')]
[confirm("Install package {{PACKAGE}}?")]
[progress]
install PACKAGE:
    #!/usr/bin/bash
    prog=/usr/libexec/falcos-progress
    [ -x "$prog" ] || prog=:
    "$prog" 10
    echo "Installing {{PACKAGE}}..."
    sleep 1
    "$prog" 60
    echo "Finalising..."
    sleep 1
    "$prog" 100
    echo "{{PACKAGE}} installed."

[group('System')]
[select("FLAVOR:desktop|laptop|server")]
[confirm("Build {{FLAVOR}} image?")]
[progress]
build FLAVOR:
    #!/usr/bin/bash
    prog=/usr/libexec/falcos-progress
    [ -x "$prog" ] || prog=:
    "$prog" 10
    echo "Building {{FLAVOR}} image..."
    sleep 2
    "$prog" 80
    echo "Finalising..."
    sleep 1
    "$prog" 100
    echo "{{FLAVOR}} image built."

# Personal additions
import? "~/.config/just/user.justfile"
