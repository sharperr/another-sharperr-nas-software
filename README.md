# Another Sharperr NAS Software (ASNS)

ASNS is a self-hosted NAS management platform for existing Linux systems—not a replacement operating system. Version **0.1.0** provides a Go REST API, SQLite-backed administrator authentication, live Linux system monitoring, physical disk discovery, a React dashboard, and a small command-line client.

## One-line install

On a Debian 13 system with network access, run this from a root shell (`root@asns:~#`):

```sh
set -o pipefail && curl -fsSL https://raw.githubusercontent.com/sharperr/another-sharperr-nas-software/main/deployment/scripts/install.sh | bash
```

Do not use `sudo` when already logged in as root. If you are a non-root user and `sudo` is installed, use `... | sudo bash` instead. The installer installs Git and the build dependencies (Go, Node.js/npm, and OpenSSL), clones the repository's `main` branch into `/opt/asns/source` on first install, and runs `git pull --ff-only origin main` on subsequent installs. It then downloads Go modules, installs npm dependencies, builds the frontend and Go API/CLI, installs the frontend and API under `/opt/asns`, installs the `asns` CLI in `/usr/local/bin`, creates a dedicated unprivileged service account, and enables the systemd service. Updates stop rather than overwrite a source checkout with local modifications. Review the [installer script](https://github.com/sharperr/another-sharperr-nas-software/blob/main/deployment/scripts/install.sh) before running it as root. On a new install it generates a random initial administrator password and prints it before building; save it. After the first successful startup, the bootstrap password is removed from its configuration. Reinstalling preserves the existing configuration and database.

The service listens on `127.0.0.1:8080`. Use a TLS-terminating reverse proxy for remote access; do not expose the HTTP service directly to an untrusted network.

### CLI

- `asns help` — show available commands.
- `asns health` — check the ASNS API and print its version.
- `asns launch` — start the systemd service if it is stopped, wait for the API, and open the dashboard when a desktop browser is available.
- Set `ASNS_URL` to use a different HTTP(S) base URL. The default is `http://127.0.0.1:8080`.

### One-line uninstall

Remove the application, service, and CLI while preserving the database and configuration:

```sh
bash /opt/asns/source/deployment/scripts/uninstall.sh
```

To delete ASNS configuration and database as well, run `bash /opt/asns/source/deployment/scripts/uninstall.sh --purge` and type `DELETE` at the confirmation prompt. Neither uninstall form removes Debian packages installed for building ASNS.

## Requirements

- Debian 13 on the NAS host, with Linux procfs and sysfs
- Go 1.23 or newer
- Node.js 20.19 or newer, or 22.12 or newer, with npm

The installer checks these versions. It does not configure disk mounts, partitions, SMART, or network-facing TLS.

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

The server serves `frontend/dist` when `ASNS_WEB_DIR` points to that directory. Configure `ASNS_DB_PATH` to a persistent SQLite file. The server defaults to `127.0.0.1:8080`; set `ASNS_COOKIE_SECURE=true` when HTTPS is terminated in front of it. To create the initial administrator, provide `ASNS_ADMIN_USERNAME` and `ASNS_ADMIN_PASSWORD` on first startup; the password must be 12-72 supported characters. Remove those variables after the first account is created.

The service unit is [deployment/systemd/asns.service](./deployment/systemd/asns.service). It runs as a dedicated `asns` user with systemd filesystem protections. Never run the web process as root.

## API

- `GET /api/v1/health` — unauthenticated process health.
- `POST /api/v1/auth/login` — create an administrator session.
- `GET /api/v1/auth/me` — current administrator and CSRF token.
- `POST /api/v1/auth/logout` — end session; requires `X-CSRF-Token`.
- `GET /api/v1/system` — live CPU, memory, network rates, and uptime.
- `GET /api/v1/disks` — physical block devices discovered through sysfs.

All endpoints other than health and login require an authenticated session. Non-GET requests require the session CSRF token.

## Security and scope

Passwords use bcrypt; only hashes of high-entropy session tokens are stored. Sessions expire after 12 hours. The API sets security headers, rate-limits login attempts, applies CSRF checks, and records authentication audit events. The web service is unprivileged and does not expose shell commands or the Docker socket.

Dashboard metrics are read directly from Linux procfs/sysfs; unsupported host information is shown as unavailable, not replaced with sample values. The Advanced mode shows additional host details; Simple mode explains the displayed measurements. Theme and mode preferences are saved in the browser.

Not implemented in this release: SMART health/temperatures, filesystem and mount management, file browsing, SMB, Docker management, persistent metric history, administrative user management, backups, privileged agent operations, and WebSockets. The corresponding dashboard sections are marked as planned and do not claim these features work. Monitoring refresh currently uses authenticated REST polling every three seconds.

This development workspace may be used on Windows, but the live monitor reads Linux procfs/sysfs and therefore requires a Linux host to display system metrics. No Windows demonstration values are generated.
