# falcos-cli

An OS TUI for system info and running `just` recipes, for
[falcos](https://github.com/jayelg/falcos) and other bootc images. Run it with
no arguments for a system panel plus a menu of the image's `just` recipes; pass
a recipe name to run one directly.

It is aliased to the OS name in the image (so on falcos you type `falcos`), read
from `/etc/os-release` so the command follows a rebrand.

## What it shows

A fastfetch-style panel — OS, kernel, uptime, packages, desktop, CPU, GPUs,
memory, swap, root and `/etc` disk usage, local IP — then the recipe menu,
grouped by the `[group(...)]` attribute in the justfile.

## Running recipes

Recipes run inside an embedded pane (a real PTY, so prompts, `sudo`, `gum` and
other interactive tools work). A recipe that goes full-screen (its own TUI, an
editor) is handed the whole terminal automatically, and control returns to the
panel when it exits.

Recipes can drive the bottom progress bar by emitting an
[OSC 9;4](https://learn.microsoft.com/en-us/windows/terminal/tutorials/progress-bar-sequences)
progress sequence (falcos ships a `falcos-progress` helper for this); the
sequence is ignored by plain terminals.

- `falcos` — panel + menu
- `falcos <recipe> [args]` — run a recipe in the pane
- non-interactive (piped, scripted) invocations pass straight through to `just`

## Configuration

- `FALCOS_JUSTFILE` — path to the system justfile (default
  `/usr/share/falcos/justfile`)
- `FALCOS_PLAIN` — set to bypass the TUI and exec `just` directly

## UI Integration Convention

Recipes declare how they integrate with the TUI using standard `just` attributes.
The convention covers three patterns:

### 1. Input parameters

Parameters declared in a recipe signature are automatically collected by the TUI
before the recipe runs. Each parameter is prompted for in order, and all values
are passed as arguments to `just`.

```just
[group("System")]
install-package PACKAGE:
    rpm-ostree install {{PACKAGE}}
```

When selected, the TUI shows a form with all parameters. The user provides
values, then the recipe runs automatically, no shell-level prompting needed.

### 2. Confirmation popup

Add a `[confirm("prompt text")]` attribute to display a Proceed/Cancel dialog
before the recipe starts. The prompt text supports `{{param}}` placeholders
that are expanded with the collected parameter values.

```just
[confirm("Install package {{PACKAGE}} on {{HOST}}?")]
install-package PACKAGE HOST:
    rpm-ostree install {{PACKAGE}}
```

The user navigates with ←/→ and confirms with Enter or cancels with Esc.

### 3. Progress bar

Add a `[progress]` attribute to indicate the recipe emits OSC 9;4 progress
sequences. The TUI shows a gradient progress bar at the bottom of the output
pane while the recipe runs. Use the `falcos-progress` helper (shipped with
falcos) inside the recipe:

```just
[progress]
update-system:
    falcos-progress 10 "Checking updates..."
    # ... update commands ...
    falcos-progress 50 "Downloading..."
    # ... more commands ...
    falcos-progress 100 "Done!"
```

The `falcos-progress` helper emits the standard OSC 9;4 terminal sequence:

```
printf '\e]9;4;1;%%s\e\\' "$pct"    # set progress to pct%
printf '\e]9;4;0;0\e\\'              # clear progress bar
```

### 4. Combining patterns

All three attributes can be combined freely on a single recipe:

```just
[confirm("Install {{PACKAGE}} on this system?")]
[progress]
install-package PACKAGE:
    falcos-progress 10 "Preparing..."
    rpm-ostree install {{PACKAGE}}
    falcos-progress 100 "Done!"
```

### 5. Silent execution

Add a `[silent]` attribute to suppress the CLI overlay during execution.
Recipes that produce no useful terminal output (reboot, shutdown) benefit
most. The recipe status line appears above the menu and the menu stays
visible.

```just
[silent]
[confirm("Reboot now?")]
reboot:
    systemctl reboot
```

When combined with other attributes, `[silent]` takes effect only during
the execution phase. Parameters and confirmation still show their UI.

| Attribute   | Effect                                        |
|-------------|-----------------------------------------------|
| `[silent]`  | CLI overlay hidden; status + menu visible     |
| (none)      | CLI overlay shown during execution (default)  |

### Flow summary

```
User selects recipe
  ↓
┌─ Any parameters? ──→ Show input form (collect all)
│                        ↓
└── No ←──────────────┘
  ↓
┌─ [confirm("...")]? ──→ Show Proceed/Cancel popup
│                         ↓
└── Yes ←──────────────┘  No → return to menu
  ↓
┌─ [silent]? ──→ Status line + menu stay visible
│                 (no CLI overlay)
└── No ←───────┘
  ↓
Recipe starts in PTY pane (CLI overlay)
  ↓
┌─ [progress]? ──→ Progress bar rendered below CLI output
│                    (falcos-progress helper)
└── No ←────────┘
  ↓
Recipe exits → exit status + return to menu
```

## Template justfile

A complete template justfile demonstrating all UI integration patterns is
available at [`TEMPLATE.justfile`](TEMPLATE.justfile) in this repository.
Copy it as a starting point for your own image's justfile.

## Building

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o falcos-cli .
```

A single static binary, no cgo. Releases publish a prebuilt
`x86_64-unknown-linux-gnu` tarball with a `.sha256` sidecar; falcos consumes
that pinned by version and checksum.

## License

Apache-2.0.
