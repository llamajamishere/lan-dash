# lan-dash

A self-hosted monitoring dashboard for a home or small-office LAN. It ships as **one static Go binary** with the React UI embedded, stores its data in SQLite, and runs as a hardened systemd service on an Ubuntu box.

> **Status: phases 1–3 of 8 are implemented.**
>
> | Phase | Scope | State |
> |---|---|---|
> | 1 | Auth, SQLite, device CRUD, UI shell, installer | ✅ |
> | 2 | Ubuntu SSH collector, live server cards, mock collectors | ✅ |
> | 3 | Alert engine (state machine, suppression, ack/mute) and UI | ✅ |
> | 4 | Ping/TCP/HTTP checks, Windows collector | planned (device type exists) |
> | 5 | Philips Hue, smart-plug stub | planned (UI placeholder) |
> | 6 | Auto-discovery, backup export/import | planned |
> | 7 | Blink cameras | planned (UI placeholder) |
> | 8 | Notifications (ntfy/Telegram/Discord/webhook), polish | planned (`Notifier` interface is wired in as a no-op) |

---

### Features (current)

- **Single binary**: CGO-free (`modernc.org/sqlite`), with the frontend embedded via `//go:embed all:dist`.
- **SQLite in WAL mode** at `/var/lib/lan-dash/landash.db`. A backup is taken automatically before migrations run on every start.
- **Auth**: one shared password (bcrypt) set on a first-run setup page. Sessions use HttpOnly, SameSite=Strict cookies, every mutating request needs a CSRF token, and logins are rate-limited to 5 attempts per 15 minutes per IP.
- **Credentials** (SSH passwords and keys) are encrypted with AES-256-GCM using the master key at `/etc/lan-dash/master.key`. The API is write-only: secrets are never sent back to the browser.
- **Ubuntu SSH collector**: keeps one persistent SSH connection per server and runs one batched command per poll (default 15 s). It collects CPU, load, RAM, swap, disks, network throughput, temperatures, uptime, top processes, running systemd services and Docker containers. Host keys are **trust-on-first-use**: a changed key blocks the connection and raises a critical alert.
- **Mock collectors**: realistic fake data for every device type. Use them to try the UI without any real hosts.
- **Live UI over Server-Sent Events**: Overview tiles, status chips, server cards with gauges and sparklines, and a detail page (1 h raw / 24 h 1-minute charts, processes, services, Docker, disks, network, temps).
- **Alert engine**: OK → Pending → Firing → Resolved, persisted across restarts.
  - Missing data counts as **UNKNOWN**, never a false OK.
  - Hysteresis resolve thresholds and per-rule cooldowns.
  - Alerts on a device are suppressed while its parent (switch/router) is down.
  - Alerts can be acknowledged, muted and unmuted.
- **Audit log** of control and alert actions, plus a **Health** page (collector errors, last poll per device, DB size, uptime).
- Dark, mobile-responsive UI.

---

### Requirements

| To build | To run |
|---|---|
| Go **1.22+** | Ubuntu 22.04/24.04 (any systemd Linux should work) |
| Node.js **18+** and npm | nothing else: the binary is static |
| `make` (optional) | |

You can build on a different machine and copy the binary over. To cross-compile, set `GOARCH`, e.g. `GOARCH=arm64 ./build.sh` for a Raspberry Pi 4/5.

---

### Quick start

```bash
# 1. Build (frontend first, then the Go binary) -> bin/lan-dash
./build.sh            # or: make build

# 2. Install as a service (creates user, dirs, master key, systemd unit)
sudo ./install.sh     # or: make install

# 3. Open the dashboard and set the admin password
#    http://<server-ip>:8080/   -> redirected to /setup on first run
```

`install.sh` does the following:

1. Creates the system user `landash`.
2. Creates `/var/lib/lan-dash` (0750), `/var/lib/lan-dash/backups` and `/etc/lan-dash`.
3. Generates `/etc/lan-dash/master.key` (64 hex chars, owner `landash`, mode 0600). It is **only generated if missing**, so re-running never rotates your key.
4. Installs the binary to `/usr/local/bin/lan-dash` and the unit to `/etc/systemd/system/lan-dash.service`.
5. Writes `/etc/default/lan-dash` if it doesn't exist.
6. Enables and starts the service. On upgrade it restarts the service instead.

To uninstall, run `sudo ./install.sh --uninstall`. This removes the binary and the unit. **Data and key are kept**; delete `/var/lib/lan-dash` and `/etc/lan-dash` yourself if you want them gone.

If you use `ufw`, allow the port from your LAN only:

```bash
sudo ufw allow from 192.168.1.0/24 to any port 8080 proto tcp
```

#### Configuration

Runtime options live in `/etc/default/lan-dash`:

```bash
LANDASH_LISTEN=:8080
LANDASH_OPTS=""            # extra flags, e.g. "-log-level debug -secure-cookies"
```

Then run `sudo systemctl restart lan-dash`.

| Flag | Default | Purpose |
|---|---|---|
| `-listen` | `:8080` | HTTP listen address |
| `-db` | `/var/lib/lan-dash/landash.db` | SQLite database |
| `-key` | `/etc/lan-dash/master.key` | Master key file |
| `-backup-dir` | `<db dir>/backups` | Pre-migration backups (newest 10 kept) |
| `-mock` | off | Force mock collectors on |
| `-seed-demo` | off | Insert demo devices if the device table is empty |
| `-log-level` | `info` | `debug`, `info`, `warn` or `error` |
| `-log-json` | off | JSON logs instead of logfmt |
| `-secure-cookies` | off | Mark cookies `Secure`. Enable this when serving over HTTPS via a reverse proxy |
| `-trust-proxy` | off | Use `X-Forwarded-For` for client IPs (rate limit and audit). Only enable it behind a proxy |
| `-init-key` | off | Generate the master key if missing (**development only**) |
| `-version` | | Print the version and exit |

Poll intervals are set per device in the UI. Mock mode can also be toggled under **Settings → General**.

#### Development

```bash
make dev
```

This starts:

- **The backend** on `127.0.0.1:8080` with mock collectors and demo devices. Its DB and key go in `./dev-data/`.
- **The Vite dev server** on `http://localhost:5173`, with hot reload. It proxies `/api` (including SSE) to the backend.

Ctrl-C stops both. Other targets:

- `make dev-backend` / `make dev-frontend` run one side only.
- `make test` runs `go test` and a TypeScript type-check.
- `make vet` runs `go vet`.
- `make clean` deletes build output.

---

### Adding an Ubuntu server (read-only SSH user)

lan-dash only needs to **read** system information. Create a dedicated, unprivileged user on each monitored server. Never use root.

On the **monitored server**:

```bash
# 1. A locked-down user with no password login
sudo adduser --disabled-password --gecos "lan-dash monitor" landash-ro

# 2. Optional extras
sudo apt install lm-sensors        # CPU/board temperatures (then: sudo sensors-detect --auto)
sudo usermod -aG docker landash-ro # ONLY if you want Docker container status (see note)

# 3. Authorise the dashboard's public key
sudo -u landash-ro mkdir -p ~landash-ro/.ssh
sudo -u landash-ro chmod 700 ~landash-ro/.ssh
echo 'ssh-ed25519 AAAA... lan-dash' | sudo -u landash-ro tee -a ~landash-ro/.ssh/authorized_keys
sudo chmod 600 ~landash-ro/.ssh/authorized_keys
```

Generate the key pair anywhere (e.g. on your workstation). The private key is pasted into lan-dash and the public key goes on the server:

```bash
ssh-keygen -t ed25519 -N "" -C lan-dash -f ./landash_ed25519
```

You can lock the key down further in `authorized_keys` by prefixing it with
`no-port-forwarding,no-X11-forwarding,no-agent-forwarding,no-pty `.

> **Docker note:** the `docker` group is effectively root-equivalent. Only add `landash-ro` to it if you accept that. Without it, the Docker tab stays empty and everything else works.

Everything the collector reads (`/proc`, `df`, `ps`, `systemctl list-units`, `sensors -j`, `docker ps`) works without sudo. lan-dash **never runs sudo**.

Then, in the **dashboard**:

1. Go to **Settings → Devices → Add device**.
2. Set type `ubuntu`, the host/IP and the SSH port, then save.
3. Click **Credentials**, enter username `landash-ro` and paste the private key (or a password). Credentials are encrypted at rest and cannot be read back.
4. The first poll records the server's **host key fingerprint (TOFU)**.

If you later reinstall that server, its host key changes. lan-dash then refuses to connect and raises *SSH host key changed*. Once you've confirmed the change is legitimate, open the server's detail page and click **Reset host key**.

---

### Windows OpenSSH setup (for the phase 4 collector)

The Windows collector arrives in phase 4. You can prepare the hosts now. In an **elevated PowerShell**:

```powershell
# Install and start the OpenSSH server
Add-WindowsCapability -Online -Name OpenSSH.Server~~~~0.0.1.0
Start-Service sshd
Set-Service -Name sshd -StartupType Automatic

# Firewall: allow SSH from the LAN only (adjust subnet)
New-NetFirewallRule -Name "OpenSSH-LAN" -DisplayName "OpenSSH (LAN)" -Enabled True `
  -Direction Inbound -Protocol TCP -LocalPort 22 -RemoteAddress 192.168.1.0/24 -Action Allow

# Optional: make PowerShell the default SSH shell
New-ItemProperty -Path "HKLM:\SOFTWARE\OpenSSH" -Name DefaultShell `
  -Value "C:\Windows\System32\WindowsPowerShell\v1.0\powershell.exe" -PropertyType String -Force
```

Create a **standard (non-admin)** local user for monitoring, e.g. `landash-ro`. Add it to the built-in group **Performance Monitor Users**, which allows reading counters without admin rights.

Then put the public key in `C:\Users\landash-ro\.ssh\authorized_keys`. Watch out for one quirk: for users in the *Administrators* group, OpenSSH reads `C:\ProgramData\ssh\administrators_authorized_keys` instead. This is another reason to use a non-admin account.

To check it works, run `ssh landash-ro@<windows-ip> hostname` from the lan-dash server.

---

### Philips Hue pairing (phase 5)

Hue support arrives in phase 5. The pairing flow will be:

1. Give the bridge a fixed address (see the next section).
2. In lan-dash, add a device with type `hue` and the bridge IP. Or let discovery (phase 6) find it.
3. When prompted, **press the round link button on the bridge**, then click *Pair* within 30 seconds.
4. lan-dash receives an application key from the bridge and stores it encrypted, like other credentials.

Until then you can add the bridge as a device so it shows on the Overview. The *Hue bridge offline* rule ships disabled; enable it in **Alerts → Rules** once the collector exists.

---

### Static IP / DHCP reservation (recommended)

Collectors address devices by IP or hostname, and the dashboard itself needs a stable URL. Give fixed addresses to:

- **The lan-dash server.** Otherwise bookmarks break and firewall rules on monitored hosts (which allow SSH from it) stop matching.
- **Every monitored device**, especially the Hue bridge, servers and cameras.

**Prefer DHCP reservations on your router** (often called "Address reservation" or "Static lease"). You map each MAC address to an IP once, and devices keep using DHCP. This is easier to manage than per-device static configuration, and survives OS reinstalls.

If you must configure a static IP on Ubuntu, use netplan (`/etc/netplan/*.yaml`):

```yaml
network:
  version: 2
  ethernets:
    eno1:
      dhcp4: false
      addresses: [192.168.1.10/24]
      routes: [{ to: default, via: 192.168.1.1 }]
      nameservers: { addresses: [192.168.1.1] }
```

Then run `sudo netplan apply`. Pick addresses **outside** the router's DHCP pool.

Set each device's **parent** (e.g. the switch it hangs off) in its settings. If the switch dies, you then get one alert for the switch instead of a flood from everything behind it.

---

### Upgrading

```bash
git pull                # or unpack the new source
./build.sh
sudo ./install.sh       # replaces the binary atomically, keeps key/config/data, restarts
```

On start-up the new binary first **backs up the database** to `/var/lib/lan-dash/backups/`, then runs any pending **migrations**. If a migration fails, the service stops and the pre-upgrade backup is left in place. To roll back, reinstall the old binary and restore that backup (see below).

Run `lan-dash -version` to check which version is installed.

---

### Backups

**What to back up**

| File | Why |
|---|---|
| `/var/lib/lan-dash/landash.db` | Devices, rules, alert state, settings, encrypted credentials |
| `/etc/lan-dash/master.key` | Required to decrypt the stored credentials |

> Store the master key **separately** from database backups, e.g. in your password manager. Anyone holding both can decrypt your SSH credentials. lan-dash never puts the key in its own backups.
>
> If you lose the key, the DB is still usable, but every device's credentials must be re-entered.

**Automatic backups**: every service start writes `landash-<timestamp>.db` into `/var/lib/lan-dash/backups/` (a consistent `VACUUM INTO` copy). The newest 10 are kept.

**Manual / scheduled backup**: this is safe while the service is running, because of WAL.

```bash
sudo apt install sqlite3
sudo -u landash sqlite3 /var/lib/lan-dash/landash.db ".backup '/var/lib/lan-dash/backups/manual-$(date +%F).db'"
```

Alternatively, run `sudo systemctl stop lan-dash`, copy `landash.db*`, then start the service again.

**Restore**:

```bash
sudo systemctl stop lan-dash
sudo cp /path/to/backup.db /var/lib/lan-dash/landash.db
sudo rm -f /var/lib/lan-dash/landash.db-wal /var/lib/lan-dash/landash.db-shm
sudo chown landash:landash /var/lib/lan-dash/landash.db
sudo systemctl start lan-dash
```

Metrics history is deliberately short, so a backup is mostly configuration:

- raw samples: 1 h;
- 1-minute averages: 24 h;
- resolved alert history: pruned automatically.

UI export/import, with optional passphrase-encrypted credentials, arrives in phase 6.

---

### Default alert rules

These are installed on first start. Edit them in **Alerts → Rules**.

| Rule | Condition | Resolve | Severity | Enabled |
|---|---|---|---|---|
| Device offline | offline/no data for 60 s | back online | critical | ✅ |
| Disk usage | fullest mount > 85 % | < 80 % | warning | ✅ |
| RAM | > 90 % | < 85 % | warning | ✅ |
| CPU | > 95 % for 5 min | < 85 % | warning | ✅ |
| High temperature | hottest sensor > 80 °C | < 75 °C | critical | ✅ |
| SSH host key changed | key differs from TOFU record | key reset | critical | ✅ |
| Hue bridge offline | type `hue`, offline 2 min | | warning | ❌ (phase 5) |
| Camera offline | type `blink`, offline 5 min | | warning | ❌ (phase 7) |
| Low battery | < 20 % | > 30 % | warning | ❌ |

Rule semantics:

- **Duration** is how long the condition must hold (Pending) before the alert fires.
- **Cooldown** blocks re-firing for the same rule and device after a resolve.
- A **target** is a single device, a group, a device type, or all devices.
- **No data counts as UNKNOWN.** Metric rules hold their state instead of resolving. The *Device offline* rule treats UNKNOWN for longer than its duration as offline.

---

### Security notes

- Serve lan-dash on your LAN only. To expose it more widely, put it behind a TLS reverse proxy (Caddy or nginx) and start it with `-secure-cookies -trust-proxy`.
- The systemd unit runs as `landash` with `ProtectSystem=strict`, `NoNewPrivileges` and related hardening. Its only extra capability is `CAP_NET_RAW`, for ICMP ping (phase 4).
- The login limit is 5 attempts per IP per 15 minutes. Sessions are server-side; changing the password invalidates all other sessions.
- SSH host keys are never ignored. `InsecureIgnoreHostKey` is not used anywhere.

---

### Troubleshooting

```bash
systemctl status lan-dash
journalctl -u lan-dash -f                 # live logs (structured slog -> journald)
journalctl -u lan-dash --since "1 hour ago" -p warning
```

| Symptom | Check |
|---|---|
| Service won't start: "master key" | `/etc/lan-dash/master.key` exists, is owned by `landash` and has mode 0600 |
| Server card shows *offline* | From the lan-dash host, `ssh -i key landash-ro@host true` succeeds. Also check the **Health** page for the collector error |
| *SSH host key changed* | Verify the host was reinstalled, then use **Reset host key** on the server's detail page |
| No temperatures | Install `lm-sensors` on the target and run `sensors-detect`. Some VMs have no sensors |
| Docker tab empty | The SSH user isn't in the `docker` group (intended by default) |
| Locked out of login | Wait 15 minutes (rate limit), or restart the service to clear the in-memory limiter |

---

### Architecture

```
lan-dash/
├── build.sh, install.sh, Makefile
├── deploy/lan-dash.service        systemd unit (CAP_NET_RAW, hardening)
├── frontend/                      React 18 + Vite 5 + Tailwind 3 + Recharts + Zustand
│   └── src/{pages,components,hooks,lib}
└── backend/
    ├── cmd/landash/main.go        flags, wiring, graceful shutdown
    ├── frontend.go                //go:embed all:dist
    └── internal/
        ├── db/                    open (WAL), backup, migrations, queries, retention
        ├── secrets/               master key, AES-256-GCM box, credential store
        ├── auth/                  bcrypt, sessions, CSRF, login rate limiter
        ├── sse/                   event hub
        ├── collector/             Collector interface, registry, manager, ring buffer,
        │                          ubuntu (SSH), mock, stubs for later phases
        ├── alert/                 rule evaluation state machine, Notifier interface
        └── api/                   REST handlers + SPA fallback
```

**Adding a collector (phases 4–7).**

1. Implement `collector.Collector` (`Type()`, `Discover()`, `Poll()`, `Control()`). Return a `*Snapshot`, and wrap connectivity errors with `collector.Unreachable(err)` so the device shows offline instead of erroring.
2. Register it in `main.go` in place of the stub.

The manager, ring buffer, 1-minute aggregation, SSE fan-out and alert evaluation then work automatically. Expose any new metric names in the alert engine's metric list.

**Adding a notifier (phase 8).** Implement `alert.Notifier` (`Notify(ctx, AlertState) error`) and pass it to the engine. Muted alerts are skipped automatically.

#### REST API (all under `/api`, JSON)

Every route except the auth routes and `/api/version` needs a session cookie. Mutating requests also need the `X-CSRF-Token` header, whose value comes from the `landash_csrf` cookie.

| Method & path | Purpose |
|---|---|
| `GET /auth/status`, `POST /auth/setup`, `POST /auth/login`, `POST /auth/logout`, `POST /auth/password` | Auth |
| `GET/POST /devices`, `GET/PUT/DELETE /devices/{id}` | Device CRUD |
| `GET/PUT/DELETE /devices/{id}/credentials` | Write-only credentials (GET returns only metadata) |
| `POST /devices/{id}/hostkey/reset` | Forget the TOFU host key |
| `POST /devices/{id}/control` | Control action (audited; phases 5+) |
| `GET /devices/{id}/snapshots`, `GET /devices/{id}/metrics` | 1 h raw ring buffer, 24 h 1-minute aggregates |
| `GET /status` | Latest status of every device |
| `GET /events` | Server-Sent Events stream |
| `GET /alerts`, `GET /alerts/history` | Active alerts, resolved alerts (paginated) |
| `POST /alerts/{id}/ack`, `/mute` (`{"duration_s":3600}`), `/unmute` | Alert handling |
| `GET/POST /alert-rules`, `PUT/DELETE /alert-rules/{id}`, `GET /alert-metrics` | Rules |
| `GET/PUT /settings`, `GET /health`, `GET /audit` | Settings, health, audit log |

All timestamps are stored and returned as UTC Unix seconds and displayed in browser-local time.
