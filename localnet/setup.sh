#!/usr/bin/env bash
# Generates a 4-validator + 1-sentry simd network into /data (once).
#
# Topology:   val0 ── sentry ── val1, val2, val3   (val1-3 also peer each other)
#             val0 is "behind the sentry": its only peer is the sentry.
#
# Genesis is tuned so failures show up in minutes, not days:
#   slashing window 50 blocks, min signed 50%, jail 60s; gov voting 30s; 1s blocks.
set -euo pipefail

DATA=/data
CHAIN_ID=localnet-1
VALS=4
# Must match the static IPs in docker-compose.yml.
VAL_IP_START=172.30.0.10
SENTRY_IP=172.30.0.20

if [[ -f "$DATA/.initialized" ]]; then
  echo "localnet already initialized"
  exit 0
fi

simd testnet init-files \
  --validator-count "$VALS" \
  --output-dir "$DATA" \
  --chain-id "$CHAIN_ID" \
  --starting-ip-address "$VAL_IP_START" \
  --keyring-backend test \
  --commit-timeout 1s \
  --minimum-gas-prices 0stake

simd init sentry --home "$DATA/sentry/simd" --chain-id "$CHAIN_ID" >/dev/null 2>&1

home() { echo "$DATA/$1/simd"; }
node_id() { simd comet show-node-id --home "$(home "$1")"; }

# set_toml FILE SECTION KEY VALUE  (VALUE is written verbatim: quote strings yourself)
set_toml() {
  local file=$1 section=$2 key=$3 value=$4
  awk -v section="[$section]" -v key="$key" -v value="$value" '
    BEGIN { in_section = (section == "[]") }  # empty section = top-level keys
    /^\[/ { in_section = ($0 == section) }
    in_section && $0 ~ "^"key" *=" { print key " = " value; next }
    { print }
  ' "$file" > "$file.tmp" && mv "$file.tmp" "$file"
}

# --- genesis -----------------------------------------------------------------
GEN="$(home node0)/config/genesis.json"
jq '
  .app_state.slashing.params.signed_blocks_window = "50"
  | .app_state.slashing.params.min_signed_per_window = "0.500000000000000000"
  | .app_state.slashing.params.downtime_jail_duration = "60s"
  | .app_state.gov.params.max_deposit_period = "60s"
  | .app_state.gov.params.voting_period = "30s"
  | .app_state.gov.params.expedited_voting_period = "15s"
' "$GEN" > "$GEN.tmp" && mv "$GEN.tmp" "$GEN"
for n in node1 node2 node3 sentry; do
  cp "$GEN" "$(home "$n")/config/genesis.json"
done

# --- peers -------------------------------------------------------------------
SENTRY_ID=$(node_id sentry)
declare -a IDS
for i in $(seq 0 $((VALS - 1))); do IDS[$i]=$(node_id "node$i"); done
val_peer() { echo "${IDS[$1]}@172.30.0.$((10 + $1)):26656"; }

all_vals=""
for i in $(seq 0 $((VALS - 1))); do all_vals+="${all_vals:+,}$(val_peer "$i")"; done
public_vals=""
for i in $(seq 1 $((VALS - 1))); do public_vals+="${public_vals:+,}$(val_peer "$i")"; done
sentry_peer="$SENTRY_ID@$SENTRY_IP:26656"

for n in node0 node1 node2 node3 sentry; do
  cfg="$(home "$n")/config/config.toml"
  app="$(home "$n")/config/app.toml"

  set_toml "$cfg" rpc laddr '"tcp://0.0.0.0:26657"'
  set_toml "$cfg" instrumentation prometheus true
  set_toml "$cfg" instrumentation prometheus_listen_addr '":26660"'
  set_toml "$cfg" p2p addr_book_strict false
  set_toml "$cfg" p2p allow_duplicate_ip true
  set_toml "$cfg" p2p pex false
  set_toml "$cfg" consensus timeout_commit '"1s"'

  # Validators keep the REST API off, as in production; the sentry serves it.
  set_toml "$app" api enable false
  set_toml "$app" telemetry enabled false
done

set_toml "$(home node0)/config/config.toml" p2p persistent_peers "\"$sentry_peer\""
for i in $(seq 1 $((VALS - 1))); do
  set_toml "$(home "node$i")/config/config.toml" p2p persistent_peers "\"$public_vals,$sentry_peer\""
done

SENTRY_CFG="$(home sentry)/config/config.toml"
set_toml "$SENTRY_CFG" p2p persistent_peers "\"$all_vals\""
set_toml "$SENTRY_CFG" p2p private_peer_ids "\"${IDS[0]}\""
set_toml "$(home sentry)/config/app.toml" api enable true
set_toml "$(home sentry)/config/app.toml" api address '"tcp://0.0.0.0:1317"'
set_toml "$(home sentry)/config/app.toml" '' minimum-gas-prices '"0stake"'

# --- summary -----------------------------------------------------------------
echo
echo "validator consensus addresses:"
for i in $(seq 0 $((VALS - 1))); do
  printf '  val%d  %s\n' "$i" "$(simd comet show-address --home "$(home "node$i")")"
done

touch "$DATA/.initialized"
echo "localnet initialized"
