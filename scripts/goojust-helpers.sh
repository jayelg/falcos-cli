#!/usr/bin/bash
# goojust-helpers.sh — Runtime UI helpers for goojust recipes.
# Source this at the top of any recipe that uses UI interactions:
#
#   source /usr/share/goojust/goojust-helpers.sh
#
# Then call helpers directly without defining them inline:
#
#   progNew                     # show progress bar at 0%
#   progUpdate 50 "Phase..."    # update progress + optional label
#   progClear                   # hide progress bar, reset to 0
#   name=$(prompt "Name:")      # text input
#   flavor=$(choose "Flavor?" "desktop|laptop")  # option list
#   response=$(confirm "Go?" "Yes|No" 1)         # two-button popup
#                                                 # 3rd arg 1 = clear CLI
#   summary "Packages updated"  # queue a summary line
#   summary_show                # display summary lines immediately
#   summary_clear               # clear all summary lines
#   cliHide                     # hide CLI output pane
#   cliShow                     # show CLI output pane
#
# Progress: ESC ] 9 ; 4 ; <state> ; <pct> [ ; <label> ] ST
progNew()    { printf '\e]9;4;1;0\e\\'; }
progUpdate() { local p="${1:-0}" l="${2:-}"; if [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
progClear()  { printf '\e]9;4;0;0\e\\'; }

# Prompt: ESC ] 9 ; 5 ; <text> ; <secret> ST
prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }

# Choose: ESC ] 9 ; 6 ; <prompt> ; <opt1|opt2|...> ST
choose() { printf '\e]9;6;%s;%s\e\\' "$1" "$2" >&2; read -r c; echo "$c"; }

# Confirm: ESC ] 9 ; 7 ; <prompt> [ ; <opt1|opt2> [ ; <clear> ]] ST
confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }

# CLI visibility: ESC ] 9 ; 8 ; <state> ST  (0=hide, 1=show)
cliHide() { printf '\e]9;8;0\e\\'; }
cliShow() { printf '\e]9;8;1\e\\'; }

# Summary: ESC ] 9 ; 10 ; <text> ST
summary() { printf '\e]9;10;%s\e\\' "$1"; }
# Summary show: ESC ] 9 ; 11 ST
summary_show() { printf '\e]9;11\e\\'; }
# Summary clear: ESC ] 9 ; 12 ST
summary_clear() { printf '\e]9;12\e\\'; }
