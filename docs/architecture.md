# ASNS 0.1.0 architecture

ASNS is a modular monolith with a separate privileged helper:

- **Core** is an unprivileged Go process. It serves the REST API and production React application, authenticates users, stores application data in SQLite, and coordinates monitoring and backups.
- **Agent** is a root-owned Go service with a restricted Unix socket at `/run/asns-agent/asns-agent.sock`. It exposes fixed operations for SMART reads, Samba account/share management, and confirmed restore application. It accepts no shell commands or arbitrary filesystem paths from Core.

The Core systemd service runs as the dedicated `asns` account. The Agent is sandboxed by systemd and is not reachable over TCP. Its writable paths include `/etc` because local account tools use atomic sibling-file replacements; this is broader than a file-only allow-list. The Agent's Unix-socket API is therefore deliberately limited to validated account names, fixed Samba operations, known ASNS directories, and fixed service operations.

## Monitoring

CPU, memory, uptime, network counters, and block-device discovery come from Linux procfs/sysfs. The monitoring sampler persists real system samples to SQLite every 10 seconds and retains seven days. The authenticated history endpoint supports up to 168 hours. Unsupported data is returned as unavailable rather than replaced with demonstration values.

SMART status and temperature are queried from discovered physical block devices through a fixed `smartctl --json -H -A /dev/<validated-device>` invocation. If SMART is unavailable or the device reports no supported health/temperature attributes, the API reports that limitation. The installer installs `smartmontools`; actual disk support depends on the controller and device.

Software RAID status is read from `/proc/mdstat`; `mdadm --detail --export` supplies read-only array details for arrays the Linux kernel already exposes. The API reports RAID level, state, member devices, active/expected count, degraded state, and synchronization progress when available. The installer installs `mdadm`. ASNS does not create, assemble, reshape, stop, or modify arrays, and does not partition or format disks.

## Backup and restore

Backups contain a consistent SQLite snapshot and ASNS-managed private user files. Archives are gzip-compressed tar files with a SHA-256 manifest and restrictive permissions. Before staging restore, archive paths and checksums are validated and the SQLite database is integrity-checked. Applying a staged restore requires administrator confirmation, stops Core, preserves a rollback copy, replaces the database and user files, restarts Core, and checks its health; failure triggers a rollback attempt.

The administrator configures a destination relative to `/var/lib/asns/backups` and a schedule interval from 1 to 720 hours. Mount external storage under that directory before selecting it, and ensure the `asns` service account can write there. Backups are retained indefinitely until an administrator deletes them.

Backups currently **exclude** SMB share contents under `/srv/asns/shares`, Agent state/account configuration under `/var/lib/asns-agent`, system configuration, and operating-system files. Restoring therefore does not roll back SMB users or shares.

## SMB sharing and Agent boundary

ASNS-managed shares expose only generated directories below `/srv/asns/shares`. Samba configuration is rendered into `/etc/samba/asns-shares.conf` and included from Samba's global section after `testparm` validation. Shares deny guest access and default to read-only; explicit per-account permissions determine write access.

SMB credentials are separate from ASNS web accounts. Managed SMB usernames use the `asns_` prefix, and their passwords are stored in Samba's account database, not SQLite. Removing a share preserves its files. The Agent manages only fixed Samba operations and a restricted set of SMART/restore actions over its Unix socket.

## Authentication and storage

On interactive first install, the installer prompts for and confirms an administrator password through `/dev/tty`; `ASNS_ADMIN_PASSWORD` supports unattended setup. The bootstrap password is held temporarily in the root-owned service environment and removed after successful initialization. Passwords use bcrypt; only hashes of high-entropy session tokens are stored. Sessions expire after 12 hours and use HttpOnly, SameSite cookies. Unsafe requests require the per-session CSRF token. Login attempts are rate-limited per source IP, and authentication events are recorded in SQLite.

Administrators can create standard users, review measured usage, and assign byte-based quotas. A zero quota means unlimited. ASNS-managed private storage lives in `/var/lib/asns/users/<user-id>` and each account is confined to its own root. Uploads through ASNS are rejected before writing if they exceed the configured limit. These quotas are not Linux filesystem quotas and do not account for SMB or operating-system files.

## System settings

The administrator-only Settings page reads the host hostname, IANA timezone, and the active/substate of the fixed ASNS Core, Agent, and Samba systemd units. Hostname changes use `hostnamectl`; timezone changes are restricted to installed zoneinfo entries and use `timedatectl`. Restart is limited to ASNS Core and Samba. No arbitrary systemd unit, SSH configuration, network, or firewall changes are exposed.

## Frontend, install, and scope

Vite runs independently in development and proxies `/api` to Core. Production assets are served by Core. The Debian installer clones or fast-forward updates the GitHub `main` branch, installs Go, Node.js/npm, Samba, mdadm, and `smartmontools`, builds the frontend/Core/Agent/CLI, and installs systemd services. The `asns` CLI supports `help`, `health`, and `launch`. The installer binds Core to `0.0.0.0:8080` for direct access from the trusted LAN; manually launched Core remains loopback-bound by default. The installer enables Samba for direct SMB connections on TCP 445. Administrators must allow ports 8080 and 445 from their trusted LAN in any active firewall and must not expose plain HTTP or SMB directly to the public Internet; use a VPN or a TLS-terminating reverse proxy for remote access.

Normal uninstall disables and removes Core and Agent, removes the ASNS Samba configuration, and preserves ASNS data, backup archives, share files, Agent state, and configuration. Confirmed `--purge` additionally removes these ASNS data directories. It does not uninstall Debian packages or the Samba service.

## Desktop sync client

The separate Electron client in `desktop/` supports Windows and Linux builds. It stores profile configuration and hashed synchronization baselines in the local app data directory; ASNS passwords are not persisted, and authentication uses a private Electron cookie session. Files are scanned by SHA-256, with local symbolic links skipped. The authenticated recursive manifest and atomic replacement-upload endpoints keep storage inside the user's ASNS root and enforce that user's quota. Sync uploads and deletions can carry the scanned SHA-256 as a precondition to avoid overwriting/removing a NAS file changed since the scan. Large transfers are streamed and use extended request deadlines.

Profiles map one local folder to one ASNS storage folder and can apply include/exclude glob patterns. New and modified files sync both ways. If both copies differ from their last common baseline, the NAS version wins; overwritten local files are copied to the profile's recovery directory first. Deletions can be propagated both ways only after a confirmation bound to the exact reviewed deletion paths and hashes; scheduled sync never approves deletions. The client must be running for schedules to execute. Directories are created on both ends; directory deletions are not propagated. Transport over HTTP is unencrypted and intended only for a trusted LAN; use HTTPS for other networks.

Build Windows NSIS or Linux AppImage/deb packages from the client platform with `npm ci` and `npm run dist:windows` or `npm run dist:linux` in `desktop/`. The NAS installer intentionally does not install the desktop GUI on the NAS host.

SFTP user management, remote CIFS mounts, rsync jobs, Kubernetes, Docker applications/plugins, filesystem and mount management, RAID creation/management, ZFS/Btrfs configuration, and WebSockets are not implemented yet. Kubernetes was deferred by request. No app catalog, transfer service, or container controls are presented as functional.
