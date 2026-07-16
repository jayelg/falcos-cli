# falcos-cli

A branded system TUI for [falcos](https://github.com/jayelg/falcos) and other
bootc images. Run it with no arguments for a system panel plus a menu of the
image's `just` recipes; pass a recipe name to run one directly.

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

## Building

```bash
CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o falcos-cli .
```

A single static binary, no cgo. Releases publish a prebuilt
`x86_64-unknown-linux-gnu` tarball with a `.sha256` sidecar; falcos consumes
that pinned by version and checksum.

## License

Apache-2.0.
