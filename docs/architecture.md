# ASNS 0.1.0 architecture

ASNS is a modular monolith: one unprivileged Go process serves the REST API and (when built) the React application. SQLite stores administrator accounts, hashed session identifiers, and authentication audit events.

## Initial system integration

The monitoring module reads Linux procfs and sysfs directly. CPU and network rates are calculated from periodic counter samples; memory and uptime are read from procfs; physical block devices are discovered from sysfs. Unsupported or unavailable host data is reported as unavailable, never replaced with demonstration values.

The web process does not run as root. The systemd unit uses a dedicated `asns` account and filesystem protections. No privileged agent or storage-changing operation is included in this foundation. A future ASNS Agent must expose only an allow-listed, validated Unix-socket API.

## Authentication

On an interactive first install, the installer prompts for and confirms an administrator password using `/dev/tty`, so the prompt works while the script itself is piped from curl. `ASNS_ADMIN_PASSWORD` supports unattended setup. The bootstrap password is kept in the root-owned service environment until the first healthy startup, then removed. Passwords use bcrypt; only SHA-256 hashes of high-entropy session tokens are stored. Sessions expire after 12 hours and use HttpOnly, SameSite cookies. Unsafe requests require the per-session CSRF token. Login attempts are limited per source IP, and authentication events are recorded in SQLite.

## Frontend

Vite runs independently in development and proxies `/api` to the Go service. A production frontend build can be served by the Go process by setting `ASNS_WEB_DIR` to the build output directory.

The Debian online installer installs Git and the frontend/backend build dependencies, keeps the repository checkout at `/opt/asns/source`, clones `main` on first install, and runs `git pull --ff-only origin main` on updates. It downloads Go modules and installs npm packages before building the UI and API, then installs a systemd unit running as the dedicated unprivileged `asns` account and installs the `asns` CLI. `asns health` checks the API; `asns launch` starts the service if needed and optionally opens a local browser. The service binds to loopback by default; remote browser access can use an SSH local port-forward or a TLS-terminating reverse proxy. Uninstall preserves `/var/lib/asns` and `/etc/asns` unless the operator explicitly requests and confirms `--purge`.

Administrators can create standard accounts, review measured usage, and assign byte-based quotas through the user-management API and UI. A zero quota means unlimited. ASNS-managed storage lives in `/var/lib/asns/users/<user-id>`; each account is confined to its own root. The file manager supports folder creation, upload, download, and removal of files or empty folders. Uploads are serialized with quota changes and rejected before writing if they would exceed the user's configured limit. Quotas cover ASNS-managed files only; they are not Linux filesystem quotas and do not include external SMB or operating-system files.
