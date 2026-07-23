#!/usr/bin/bash
# falcos-helpers.sh — Runtime UI helpers for goojust recipes.
# Source this at the top of any recipe that uses UI interactions:
#
#   source scripts/falcos-helpers.sh
#
# Then call helpers directly without defining them inline:
#
#   prog 50 "Phase..."          # progress bar
#   prog clear                   # hide progress bar
#   name=$(prompt "Name:")       # text input
#   flavor=$(choose "Flavor?" "desktop|laptop")  # option list
#   response=$(confirm "Go?" "Yes|No" 1)         # two-button popup
#                                                 # 3rd arg 1 = clear CLI
#
prog() { local p="${1:-0}" l="${2:-}"; if [[ "$p" == "clear" ]]; then printf '\e]9;4;0;0\e\\'; elif [[ -n "$l" ]]; then printf '\e]9;4;1;%d;%s\e\\' "$p" "$l"; else printf '\e]9;4;1;%d\e\\' "$p"; fi; }
prompt() { printf '\e]9;5;%s;%s\e\\' "$1" "${2:-false}" >&2; read -r r; echo "$r"; }
choose() { printf '\e]9;6;%s;%s\e\\' "$1" "$2" >&2; read -r c; echo "$c"; }
confirm() { local o="${2:-Proceed|Cancel}" c="${3:-0}"; printf '\e]9;7;%s;%s;%s\e\\' "$1" "$o" "$c" >&2; read -r r; echo "$r"; }
summary() { printf '\e]9;10;%s\e\\' "$1"; }
summary_show() { printf '\e]9;11\e\\'; }
