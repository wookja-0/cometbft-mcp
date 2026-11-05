#!/usr/bin/env bash
# The README demo, run live against the localnet. Every result is a real
# cometbft-mcp tool call (via mcpcall); nodes are really stopped.
#
#   make localnet-up            # then wait until height > 60 (past the slashing grace window)
#   go build -o /tmp/mcpcall ./docs/demo/mcpcall && make build
#   asciinema rec --headless --window-size 96x24 -c docs/demo/demo.sh /tmp/demo.cast
#   agg --theme github-dark --font-family Menlo --font-size 16 --line-height 1.35 \
#       --idle-time-limit 2 --last-frame-duration 5 --fps-cap 20 /tmp/demo.cast /tmp/demo-raw.gif
#   python3 docs/demo/frame.py /tmp/demo-raw.gif docs/demo.gif      # needs Pillow
#   make localnet-start VAL="val2 val3"
set -euo pipefail
cd "$(dirname "$0")/../.."

call() { /tmp/mcpcall -bin bin/cometbft-mcp -config localnet/config.yaml -width 88 "$@"; }

typed() {
  local s=$1
  printf '\033[?25h' # cursor visible while typing
  for ((i = 0; i < ${#s}; i++)); do
    printf '%s' "${s:i:1}"
    sleep 0.03
  done
  printf '\n\033[?25l'
}
ask()   { printf '\n\033[1;32m❯\033[0m '; typed "$1"; sleep 0.5; }
event() { printf '\n\033[2m# %s\033[0m\n' "$1"; sleep 0.8; }
later() { printf '\033[2m  … %s\033[0m\n' "$1"; }
quiet() { "$@" >/dev/null 2>&1; }

printf '\033[2J\033[H\033[?25l'
trap "printf '\033[?25h'" EXIT
printf '\033[1mcometbft-mcp\033[0m \033[2m· a read-only MCP server for CometBFT validator operators\033[0m\n'
sleep 1.5

ask "Is everything ok?"
call fleet_overview
sleep 3

event "val3 goes down"
quiet make -s localnet-stop VAL=val3
later "15 s later"
sleep 15

ask "Is val3 signing? How long until it gets jailed?"
call validator_health '{"node":"val3","window":30}'
sleep 4

event "val2 goes down too: half of the voting power is offline"
quiet make -s localnet-stop VAL=val2
later "a minute later"
sleep 65

ask "Blocks stopped. Why?"
call consensus_diagnosis
sleep 4

printf '\n\033[1mgithub.com/wookja-0/cometbft-mcp\033[0m  \033[2mread-only · keyless · network details masked\033[0m\n'
sleep 4
