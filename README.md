# Another Sharperr NAS Software (ASNS)

ASNS is a self-hosted NAS management platform for existing Linux systems. Version **0.1.0** is an early foundation—not a replacement operating system. It currently provides a real-time system dashboard, physical disk discovery, and administrator session authentication.

## Requirements

- Debian 13 or another Linux host with procfs and sysfs
- Go 1.23 or newer
- Node.js 20.19 or newer (or 22.12 or newer) with npm

## Development

1. Configure the initial administrator and start the API:

   ```sh
   cd backend
   ASNS_ADMIN_USERNAME=admin ASNS_ADMIN_PASSWORD='use-a-long-unique-password' go run ./cmd/asns
   ```

   On PowerShell, run `$env:ASNS_ADMIN_USERNAME="admin"` and `$env:ASNS_ADMIN_PASSWORD="use-a-long-unique-password"` before `go run ./cmd/asns`. The default API address is `127.0.0.1:8080`; the SQLite database is `backend/data/asns.db` when started from this directory.

2. In another terminal, run the frontend:

   ```sh
   cd frontend
   npm install
   npm run dev
   ```

   Open the Vite URL (normally `http://127.0.0.1:5173`) and sign in with the administrator credentials. Vite proxies API requests to the local Go server.

3. Run backend tests with `cd backend && go test ./...`; build the frontend with `cd frontend && npm run build`.

## Production build

Build the frontend and backend, then run the backend from the deployment directory:

```sh
cd frontend && npm ci && npm run build
cd ../backend && go build -o ../asns ./cmd/asns
```

Set the following in the service environment file (replace the initial password with a unique secret):

```ini
ASNS_WEB_DIR=/opt/asns/frontend/dist
ASNS_DB_PATH=/var/lib/asns/asns.db
ASNS_ADMIN_USERNAME=admin
ASNS_ADMIN_PASSWORD=replace-with-a-unique-password-of-at-least-12-characters
ASNS_COOKIE_SECURE=true
```

The administrator variables are only used when creating the first account and can be removed after startup. Set `ASNS_COOKIE_SECURE=true` when serving through HTTPS. The initial service binds to loopback by default; use a TLS-terminating reverse proxy for remote access.

For systemd deployment, create a dedicated unprivileged `asns` user, install the executable and frontend build under `/opt/asns`, create `/var/lib/asns` owned by that account, configure `/etc/asns/asns.env`, and install [the service unit](./deployment/systemd/asns.service). Review the unit and set `ASNS_WEB_DIR=/opt/asns/frontend/dist` and `ASNS_DB_PATH=/var/lib/asns/asns.db` in the environment file before enabling it. Never run the web service as root.

## API

- `GET /api/v1/health` — unauthenticated process health
- `POST /api/v1/auth/login` — create a session
- `GET /api/v1/auth/me` — current administrator and CSRF token
- `POST /api/v1/auth/logout` — end session (requires `X-CSRF-Token`)
- `GET /api/v1/system` — live CPU, memory, network rates, and uptime
- `GET /api/v1/disks` — physical block devices discovered via sysfs

All endpoints other than health and login require an authenticated session. Non-GET requests require the session CSRF token.

## Current limitations

- Linux procfs/sysfs monitoring is unavailable on Windows and is not mocked.
- Disk temperatures, SMART health, filesystems, and mount management are not implemented.
- File management, SMB, Docker, monitoring history persistence, user administration, backups, and the privileged agent are not implemented. Their dashboard sections are marked unavailable rather than displaying fake data.
- Advanced mode exposes extra live host metrics; configurable system settings are not implemented.
- Dashboard updates use authenticated REST polling every three seconds; WebSocket streaming is not implemented yet.
- Use a TLS reverse proxy for any remote access. The API does not terminate TLS.
