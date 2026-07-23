# goojust

An OS TUI for system info and running `just` recipes, for
bootc-based Linux images. Run it with
no arguments for a system panel plus a menu of the image's `just` recipes; pass
a recipe name to run one directly.

It is aliased to the OS name in the image (so on the image you type the OS name), read
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
progress sequence (a `goojust-progress` helper is included for this); the
sequence is ignored by plain terminals.

- `goojust` — panel + menu
- `goojust <recipe> [args]` — run a recipe in the pane
- non-interactive (piped, scripted) invocations pass straight through to `just`

## Configuration

- `GOOJUST_JUSTFILE` — path to the system justfile (default
  `/usr/share/goojust/justfile`)
- `GOOJUST_PLAIN` — set to bypass the TUI and exec `just` directly

## UI Integration Convention

Recipes declare how they integrate with the TUI using standard `just` attributes.
The convention covers six patterns:

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
pane while the recipe runs. Use the `goojust-progress` helper (included with
the image) inside the recipe:

```just
[progress]
update-system:
    goojust-progress 10 "Checking updates..."
    # ... update commands ...
    goojust-progress 50 "Downloading..."
    # ... more commands ...
    goojust-progress 100 "Done!"
```

The `goojust-progress` helper emits the standard OSC 9;4 terminal sequence:

```
printf '\e]9;4;1;%%s\e\\' "$pct"    # set progress to pct%
printf '\e]9;4;0;0\e\\'              # clear progress bar
```

### 4. Inline prompts (during execution)

For prompts that happen mid-recipe (not just parameters), recipes call the
`goojust-prompt` helper which emits an OSC 9;5 sequence. The TUI intercepts it
and shows a focused input overlay. After the user responds, the value is written
back to the recipe's input.

```just
[progress]
setup-dotfiles:
    #!/usr/bin/bash
    repo=$(goojust-prompt "Dotfiles repo URL:"; read -r)
    branch=$(goojust-prompt "Branch:"; read -r)
    echo "Cloning $repo branch $branch..."
```

Password prompts pass `secret` as a second argument to mask input:

```just
setup-vpn:
    #!/usr/bin/bash
    user=$(goojust-prompt "VPN username:"; read -r)
    pass=$(goojust-prompt "VPN password:" secret; read -rs)
    echo "Configuring VPN for $user..."
```

The `goojust-prompt` helper emits the OSC 9;5 terminal sequence:

```
printf '\e]9;5;%s;%s\e\\' "$text" "$secret"    # prompt with optional secret mode
```

The recipe calls `goojust-prompt` followed by `read` (or `read -rs` for secrets).
`goojust-prompt` emits the OSC synchronously and returns; the TUI intercepts it
before the `read` blocks, shows the prompt overlay, and writes the user's
response to the PTY on submit.

### 5. Combining patterns

All attributes can be combined freely on a single recipe:

```just
[confirm("Install {{PACKAGE}} on this system?")]
[progress]
install-package PACKAGE:
    goojust-progress 10 "Preparing..."
    rpm-ostree install {{PACKAGE}}
    goojust-progress 100 "Done!"
```

### 6. Silent execution

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
┌─ goojust-prompt? ──→ Show inline prompt overlay
│                       (user responds → written to PTY)
│                       ↓ Loop back to running state
└── No ←────────────┘
  ↓
┌─ [progress]? ──→ Progress bar rendered below CLI output
│                    (goojust-progress helper)
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
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o goojust .
```

A single static binary, no cgo. Releases publish a prebuilt
`x86_64-unknown-linux-gnu` tarball with a `.sha256` sidecar; images consume
that pinned by version and checksum.

## License

Apache-2.0.
