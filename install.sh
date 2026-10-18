#!/usr/bin/env bash
# Install or upgrade LAN Dash on Ubuntu (systemd).
#
#   ./build.sh && sudo ./install.sh          # fresh install or upgrade
#   sudo ./install.sh --uninstall            # remove service + binary (keeps data and key)
#
# Idempotent: re-running upgrades the binary and unit, never overwrites an
# existing master key, database or /etc/default/lan-dash.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
BIN_SRC="${BIN_SRC:-$ROOT/bin/lan-dash}"
BIN_DST=/usr/local/bin/lan-dash
UNIT_DST=/etc/systemd/system/lan-dash.service
USER_NAME=landash
DATA_DIR=/var/lib/lan-dash
CONF_DIR=/etc/lan-dash
KEY_FILE=$CONF_DIR/master.key
DEFAULTS=/etc/default/lan-dash
DOC_DIR=/opt/lan-dash

log() { printf '\033[1;34m==>\033[0m %s\n' "$*"; }
die() { printf '\033[1;31merror:\033[0m %s\n' "$*" >&2; exit 1; }

[[ $EUID -eq 0 ]] || die "run as root (sudo ./install.sh)"
command -v systemctl >/dev/null || die "systemd is required"

if [[ "${1:-}" == "--uninstall" ]]; then
  log "Stopping and removing service"
  systemctl disable --now lan-dash 2>/dev/null || true
  rm -f "$UNIT_DST" "$BIN_DST"
  systemctl daemon-reload
  log "Removed. Data ($DATA_DIR), key ($KEY_FILE) and user '$USER_NAME' were kept."
  log "To delete everything: sudo rm -rf $DATA_DIR $CONF_DIR $DEFAULTS $DOC_DIR && sudo userdel $USER_NAME"
  exit 0
fi

[[ -x "$BIN_SRC" ]] || die "binary not found at $BIN_SRC — run ./build.sh first"

# 1. Service user (system account, no login shell).
if ! id "$USER_NAME" >/dev/null 2>&1; then
  log "Creating system user '$USER_NAME'"
  useradd --system --home-dir "$DATA_DIR" --no-create-home --shell /usr/sbin/nologin "$USER_NAME"
fi

# 2. Directories.
log "Preparing directories"
install -d -m 0750 -o "$USER_NAME" -g "$USER_NAME" "$DATA_DIR" "$DATA_DIR/backups"
install -d -m 0750 -o root -g "$USER_NAME" "$CONF_DIR"
install -d -m 0755 "$DOC_DIR"

# 3. Master key (never regenerated: losing it makes stored credentials unreadable).
if [[ -f "$KEY_FILE" ]]; then
  log "Keeping existing master key $KEY_FILE"
else
  log "Generating master key $KEY_FILE"
  ( umask 077; head -c 32 /dev/urandom | od -An -v -tx1 | tr -d ' \n' > "$KEY_FILE"; echo >> "$KEY_FILE" )
fi
chown "$USER_NAME:$USER_NAME" "$KEY_FILE"
chmod 0600 "$KEY_FILE"
[[ $(tr -d '\n' < "$KEY_FILE" | wc -c) -eq 64 ]] || die "master key $KEY_FILE is not 64 hex characters"

# 4. Binary (atomic replace so a running service is never left with a partial file).
UPGRADE=0
[[ -f "$BIN_DST" ]] && UPGRADE=1
log "Installing binary to $BIN_DST ($("$BIN_SRC" -version))"
install -m 0755 "$BIN_SRC" "$BIN_DST.new"
mv -f "$BIN_DST.new" "$BIN_DST"

# 5. systemd unit + defaults + docs.
log "Installing systemd unit"
install -m 0644 "$ROOT/deploy/lan-dash.service" "$UNIT_DST"
if [[ ! -f "$DEFAULTS" ]]; then
  cat > "$DEFAULTS" <<'EOF'
# LAN Dash service options (read by lan-dash.service).
# Listen address. Default is all interfaces on port 8080.
LANDASH_LISTEN=:8080
# Extra flags, e.g. "-log-level debug" or "-mock -seed-demo" to try it without hardware.
LANDASH_OPTS=
EOF
  chmod 0644 "$DEFAULTS"
fi
install -m 0644 "$ROOT/README.md" "$DOC_DIR/README.md"

# 6. Enable + (re)start. The binary backs up the DB and runs migrations on start.
systemctl daemon-reload
if [[ $UPGRADE -eq 1 ]]; then
  log "Upgrading: restarting service (database is backed up automatically before migrations)"
  systemctl enable lan-dash >/dev/null
  systemctl restart lan-dash
else
  log "Enabling and starting service"
  systemctl enable --now lan-dash
fi

sleep 2
if systemctl is-active --quiet lan-dash; then
  PORT="$(. "$DEFAULTS" 2>/dev/null; echo "${LANDASH_LISTEN:-:8080}" | sed 's/.*://')"
  IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
  log "LAN Dash is running: http://${IP:-<server-ip>}:${PORT}"
  log "Open it in a browser on your LAN to set the dashboard password."
  if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
    echo "    ufw is active — allow LAN access with: sudo ufw allow from 192.168.0.0/16 to any port ${PORT} proto tcp"
  fi
  echo "    Logs: journalctl -u lan-dash -f"
  echo "    IMPORTANT: back up $KEY_FILE somewhere safe; it is not part of database backups."
else
  systemctl --no-pager status lan-dash || true
  die "service failed to start — check: journalctl -u lan-dash -n 50"
fi
