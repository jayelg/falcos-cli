# goojust

An OS TUI for system info and running `just` recipes, for bootc-based Linux images. It provides a similar functionality to universal blue's ujust command for running `just` recipes but provides an interface for just recipes to emit a richer UI including progress bars, text prompts, option selectors confirmation prompts and human readable summary outputs.

## Usage

```
goojust [flags]                  Launch interactive TUI
goojust [flags] <recipe> [args]  Run a recipe in the embedded terminal pane
```

| Flag | Purpose |
|---|---|
| `--justfile <path>` | Path to the system justfile (default `/usr/share/goojust/justfile`) |
| `--plain` | Bypass the TUI and exec `just` directly |
| `--version`, `-V` | Print version and exit |
| `--help`, `-h` | Print help and exit |

## Recipe Groups

Recipes can be grouped by categories in the scripts menu but using a `[group('Category')]` tag in the line above the script.

### Recipe functions

Recipes can inteface with the UI elements by adding the helper script at the start of the script:

```bash
source /usr/share/goojust/goojust-helpers.sh
```

With this helper script the following commands can be used throughout the script to interact and control the UI elements:

| Function | Purpose |
|---|---|
| `progNew` | Show the progress bar at 0% |
| `progUpdate <pct> [label]` | Set the bar to a percentage, with an optional phase label |
| `progClear` | Hide the bar and reset to 0 |
| `prompt <text> [secret]` | Ask for text input; pass `true` to mask the response |
| `choose <prompt> <a\|b\|c>` | Present a list of options and return the selected one |
| `confirm <text> [OK\|Cancel] [clear]` | Show a two-button popup; pass `1` as third arg to clear the CLI first |
| `summary <text>` | Queue a line to display at the end of the recipe |
| `summary_show` | Display accumulated summary lines immediately |
| `summary_clear` | Clear all accumulated summary lines |
| `cliHide` | Hide the CLI output pane |
| `cliShow` | Show the CLI output pane |

### Examples

A system update with progress tracking, summary, and an inline restart confirmation:

```just
[group('System')]
update:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    progNew
    progUpdate 10 "Checking for updates"
    rpm-ostree update
    progUpdate 60 "Updating flatpaks"
    flatpak update -y
    progUpdate 100 "Complete"
    progClear
    summary "System packages updated"
    summary_show
    if [[ "$(confirm "Restart now?" "Restart now|Later" 1)" == "Restart now" ]]; then
        systemctl reboot
    fi
```

A silent reboot: hide the CLI pane, confirm, then act:

```just
[group('Power')]
reboot:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    cliHide
    if [[ "$(confirm "Reboot now?")" == "Proceed" ]]; then
        systemctl reboot
    fi
```

Collecting input mid-recipe:

```just
[group('Configuration')]
setup-vpn:
    #!/usr/bin/bash
    source /usr/share/goojust/goojust-helpers.sh
    user=$(prompt "VPN username:")
    pass=$(prompt "VPN password:" true)
    proto=$(choose "Protocol?" "wireguard|openvpn")
    echo "Configuring $proto VPN for $user..."
```

A full template demonstrating every pattern is at [`TEMPLATE.justfile`](TEMPLATE.justfile).

## Building

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o goojust .
```

A single static binary with no runtime dependencies. Releases publish a prebuilt `x86_64-unknown-linux-gnu` tarball with a `.sha256` sidecar.

## License

Apache-2.0.
