# Another Sharperr NAS Software (ASNS)

ASNS is a self-hosted NAS management platform for existing Linux systems—not a replacement operating system. Version **0.1.0** provides a Go REST API, SQLite-backed authentication and history, a React dashboard, real Linux monitoring, `smartctl` disk health, read-only mdadm RAID status, SMB share management, scheduled backups, per-user file storage limits, and a small command-line client. A separate restricted Agent handles selected operations that require elevated privileges. Docker applications are intentionally deferred.

## One-line install

On a Debian 13 system with network access, run this from a root shell (`root@asns:~#`):

```sh
set -o pipefail && curl -fsSL https://raw.githubusercontent.com/sharperr/another-sharperr-nas-software/main/deployment/scripts/install.sh | bash
```

Do not use `sudo` when already logged in as root. If you are a non-root user and `sudo` is installed, use `... | sudo bash` instead. The installer installs Git and the build dependencies (Go and Node.js/npm), clones the repository's `main` branch into `/opt/asns/source` on first install, and runs `git pull --ff-only origin main` on subsequent installs. It then downloads Go modules, installs npm dependencies, builds the frontend and Go API/CLI, installs the frontend and API under `/opt/asns`, installs the `asns` CLI in `/usr/local/bin`, creates a dedicated unprivileged service account, and enables the systemd service. Updates stop rather than overwrite a source checkout with local modifications. Review the [installer script](https://github.com/sharperr/another-sharperr-nas-software/blob/main/deployment/scripts/install.sh) before running it as root. On first install, the script securely prompts you to set and confirm an administrator password of 12-72 supported characters; it does not generate or print a password. For unattended installs, set `ASNS_ADMIN_PASSWORD` in the environment. The password is temporarily stored in the root-owned service configuration for first startup and removed after successful setup. Reinstalling preserves the existing configuration and database.

The installer configures the web service to listen on all network interfaces at port 8080. Open `http://<nas-ip>:8080` from a computer on your trusted LAN; the installer prints the NAS addresses it detects. To check it on the NAS, use `curl -i http://127.0.0.1:8080/` or `asns health`; `ping` does not test TCP ports. Existing installs using the previous default `127.0.0.1:8080` are migrated to the LAN-accessible default on update; custom bind addresses are preserved.

The web UI uses plain HTTP, so only use it on a trusted LAN. Allow TCP port 8080 from your LAN in any active firewall, and do not forward it from your router to the public Internet. For remote access, use a VPN or a TLS-terminating reverse proxy.

### SMB file shares

In the ASNS **Sharing** page, create an SMB account and share, then grant that account read or write access. SMB credentials are separate from the ASNS web login. Connect from a device on your LAN using:

- **Windows:** enter `\\<nas-ip>\<share-name>` in File Explorer. When prompted, use the SMB username and password you created in ASNS.
- **macOS:** in Finder choose **Go → Connect to Server**, then enter `smb://<nas-ip>/<share-name>`.
- **Linux:** use `smb://<nas-ip>/<share-name>` in a file manager or `smbclient //<nas-ip>/<share-name> -U <smb-username>`.

Allow TCP port 445 from your trusted LAN in any active firewall. Direct IP connections do not require SSH or SMB network discovery.

### CLI

- `asns help` — show available commands.
- `asns health` — check the ASNS API and print its version.
- `asns launch` — start the systemd service if it is stopped, wait for the API, and open the dashboard when a desktop browser is available.
- Set `ASNS_URL` to use a different HTTP(S) base URL. The default is `http://127.0.0.1:8080`.

### One-line uninstall

Remove the application, Core/Agent services, Samba integration, and CLI while preserving ASNS data (including backups and share files):

```sh
bash /opt/asns/source/deployment/scripts/uninstall.sh
```

To delete ASNS configuration, database, backups, Agent state, and share files as well, run `bash /opt/asns/source/deployment/scripts/uninstall.sh --purge` and type `DELETE` at the confirmation prompt. Neither uninstall form removes Debian packages or the Samba service.

## Requirements

- Debian 13 on the NAS host, with Linux procfs and sysfs
- Go 1.23 or newer
- Node.js 20.19 or newer, or 22.12 or newer, with npm

The installer checks these versions and installs/enables Samba, mdadm, and smartmontools. It does not configure disk mounts, partitions, firewall rules, or network-facing TLS.

## Full-stack development

1. From the frontend directory, install dependencies and start both the Go API and frontend together:

   ```sh
   cd frontend
   npm ci
   ASNS_ADMIN_USERNAME=admin ASNS_ADMIN_PASSWORD='use-a-unique-password-of-at-least-12-characters' npm run dev:all
   ```

   The initial API startup requires administrator credentials. Open `http://127.0.0.1:5173`; Vite proxies `/api` requests to Go. `Ctrl+C` stops both processes. For PowerShell, set `$env:ASNS_ADMIN_USERNAME` and `$env:ASNS_ADMIN_PASSWORD`, then run `npm.cmd run dev:all`. The API listens on `127.0.0.1:8080` and stores its development database at `backend/data/asns.db`.

2. To run only the API, use `cd backend` followed by `ASNS_ADMIN_USERNAME=admin ASNS_ADMIN_PASSWORD='use-a-unique-password-of-at-least-12-characters' go run ./cmd/asns`.

3. Validate changes:

   ```sh
   cd backend && go test ./... && go vet ./...
   cd frontend && npm ci && npm audit && npm run build
   ```

## Manual production build

The installer is preferred. To build without installing system files:

```sh
cd frontend && npm ci && npm run build
cd ../backend && GOWORK=off go build -trimpath -o ../asns-server ./cmd/asns
cd ../backend && GOWORK=off go build -trimpath -o ../asns ./cmd/asnsctl
```

The server serves `frontend/dist` when `ASNS_WEB_DIR` points to that directory. Configure `ASNS_DB_PATH` to a persistent SQLite file. The online installer sets `ASNS_ADDR=0.0.0.0:8080` for LAN access; manually installed services retain the backend's loopback default unless configured. Set `ASNS_COOKIE_SECURE=true` when HTTPS is terminated in front of it. To create the initial administrator, provide `ASNS_ADMIN_USERNAME` and `ASNS_ADMIN_PASSWORD` on first startup; the password must be 12-72 supported characters. Remove those variables after the first account is created.

The service unit is [deployment/systemd/asns.service](./deployment/systemd/asns.service). It runs as a dedicated `asns` user with systemd filesystem protections. Never run the web process as root.

## API

- `GET /api/v1/health` — unauthenticated process health.
- `POST /api/v1/auth/login` — create an administrator session.
- `GET /api/v1/auth/me` — current administrator and CSRF token.
- `POST /api/v1/auth/logout` — end session; requires `X-CSRF-Token`.
- `GET /api/v1/system` — live CPU, memory, network rates, and uptime.
- `GET /api/v1/disks` — physical block devices discovered through sysfs.
- `GET /api/v1/disks/smart` — SMART health and temperature when supported by the device.
- `GET /api/v1/raid` — current Linux md RAID health, members, degraded count, and sync progress; read-only.
- `GET /api/v1/monitoring/history?hours=24` — persisted CPU, memory, and network samples (up to 168 hours).
- `/api/v1/backups` — administrator backup listing, scheduling, manual creation/deletion, and staged, confirmed restore.
- `/api/v1/shares` and `/api/v1/smb-users` — administrator SMB share and separate SMB-account management through the restricted Agent.
- `/api/v1/system/settings` — administrator hostname and IANA timezone read/update.
- `GET /api/v1/services` and `POST /api/v1/services/{name}/restart` — allowlisted service status and restart for ASNS Core and Samba.
- `GET/POST /api/v1/users` — administrator-only account listing and creation, including measured storage usage and quota.
- `PUT /api/v1/users/{id}/quota` — administrator-only storage limit update; `0` means unlimited.
- `/api/v1/files` — authenticated per-user file listing, upload, download, folder creation, and deletion. Uploads are rejected if they exceed the account quota.

All endpoints other than health and login require an authenticated session. Non-GET requests require the session CSRF token.

## Security and scope

Passwords use bcrypt; only hashes of high-entropy session tokens are stored. Sessions expire after 12 hours. The API sets security headers, rate-limits login attempts, applies CSRF checks, and records authentication audit events. The web service is unprivileged and does not expose shell commands or the Docker socket.

Dashboard metrics are read directly from Linux procfs/sysfs; unsupported host information is shown as unavailable, not replaced with sample values. The Advanced mode shows additional host details; Simple mode explains the displayed measurements. Theme and mode preferences are saved in the browser.

Per-user quotas apply to files stored in ASNS-managed account folders under `/var/lib/asns/users`; usage is measured from regular file sizes, and uploads through ASNS are blocked at the configured limit. This does not impose Linux filesystem quotas or include files outside ASNS-managed storage.

Monitoring history is sampled every 10 seconds and retained for seven days. Backups contain the SQLite database and ASNS private user files; they do not include SMB share files or Agent state. Backup intervals range from 1 to 720 hours, archives are retained until manually deleted, and external destinations must be mounted under `/var/lib/asns/backups`. Restore verifies archive contents and database integrity, then restarts Core and attempts rollback if the restored service is unhealthy.

SMB accounts use separate credentials from ASNS web users. Shares expose only ASNS-created folders under `/srv/asns/shares`, and removing a share preserves its files. SMART health uses `smartctl` and availability depends on the storage controller and disk. mdadm RAID status reports arrays already assembled by Linux; ASNS does not create, assemble, reshape, or otherwise modify RAID arrays.

System settings currently support hostname, installed IANA timezone, and status/restart for ASNS Core and Samba. Network, firewall, SSH, and arbitrary systemd unit changes are intentionally not exposed.

Not implemented in this release: SFTP user management, remote CIFS mounting, rsync jobs, Kubernetes, Docker applications/plugins, filesystem and mount management, RAID creation/management, ZFS/Btrfs, account roles beyond administrator and standard user, and WebSockets. Monitoring refresh uses authenticated REST polling.

This development workspace may be used on Windows, but the live monitor reads Linux procfs/sysfs and therefore requires a Linux host to display system metrics. No Windows demonstration values are generated.
