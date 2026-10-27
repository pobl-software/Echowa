# Echowa

A small self-hosted deploy tool for one server. Echowa pulls a pre-built
image from a registry (usually GHCR), runs it as a Docker Swarm service
behind a domain with TLS via Traefik, streams logs, and rolls back by image
digest. It's one Go binary and one SQLite file, next to Docker and Traefik.

Echowa is young (0.x) and changes quickly, so expect rough edges.

## What it does

- **Deploys images you've already built**, from GHCR, Docker Hub or any
  registry you sign in to. Echowa never builds; your CI does.
- **Redeploys without downtime**, and rolls back on its own when the new
  version fails its healthcheck. Every deploy records its image digest, so
  you can go back to any earlier one in a click.
- **HTTPS by default** through Traefik and Let's Encrypt, with checks that
  a domain's DNS points at the server and its certificate is valid.
- **Live logs and deploy progress** in the browser, for the current deploy
  or the ones before it.
- **Server and app metrics**: CPU, memory, disk and network, with charts.
- **PostgreSQL, MySQL and Redis from templates**, with persistent volumes,
  generated passwords, health figures, slow queries and optional external
  access over TLS.
- **Encrypted environment variables**, revealed one at a time.
- **Scheduled tasks** that run in the app's own container, on a cron
  schedule in your time zone.
- **CPU and memory limits** per app, with an alert when one runs out of
  memory.
- **Signed releases**, updated from the UI with automatic rollback, and a
  backup of Echowa's own data before every update (daily too, with
  `--with-backups`).

Not built yet: a signed webhook for CI to trigger deploys (deploys are from
the UI for now), several users, and several servers.

Deliberately out of scope: building images (CI builds and pushes; Echowa
only pulls), connecting Git providers, Compose stacks of several
containers, preview deploys per branch, and backing up your apps' data.
[deploy/README.md](deploy/README.md) shows how to back up the PostgreSQL,
MySQL and Redis templates yourself.

## Install

On a fresh Ubuntu server (24.04 is the tested one), as a user with sudo:

```bash
curl -fsSL https://get.echowa.dev/install.sh | sudo bash
```

It asks how the UI should be reached (HTTP on port 8192, HTTPS on a domain
on port 8192, or a Cloudflare Tunnel), installs Docker, Swarm, Traefik and
Echowa, and ends with a one-time link to the page that creates the admin
account. Echowa owns the server: Traefik takes ports 80 and 443.

Read [deploy/README.md](deploy/README.md) before you install on anything
that matters. It covers the firewall, DNS, flags for unattended installs,
backups and upgrades.

### On a server

| What | Where |
|---|---|
| The binary, and the one before the last update | `/usr/local/bin/echowa`, `/usr/local/bin/echowa.previous` |
| Artisan as Echowa's user | `sudo echowa-artisan <command>` (`/usr/local/bin/echowa-artisan`) |
| Settings, including `APP_KEY` | `/etc/echowa/env`. Back up `APP_KEY` apart from the database: encrypted values can't be read without it. |
| Database, sessions, logs | `/var/lib/echowa` (`echowa.sqlite`, `storage/`). Logs also in `journalctl -u echowa`. |
| Backups | `/var/backups/echowa`: daily with `--with-backups` (kept 14 days), and one before every update |
| Traefik route for the UI (HTTPS access) | `/etc/echowa/traefik/echowa.yml` |
| Services | `echowa.service`, `echowa-update.path` and `.service` (the root updater), `echowa-backup.timer` |
| Update requests and status | `/var/lib/echowa/update/` |

## Licence

Echowa is source-available under the
[Functional Source License, Version 1.1, ALv2 Future License](LICENSE.md)
(FSL-1.1-ALv2). In plain terms:

- **You can** use it, read it, change it and run it on your own servers,
  including for your business and for clients you do work for.
- **You can't** offer it, or something built from it, as a commercial
  product or service that competes with Echowa, such as a paid hosted
  version.
- **Each release becomes Apache 2.0** two years after it's published, with
  no restrictions beyond Apache's.

This summary isn't the licence; [LICENSE.md](LICENSE.md) is. The Echowa
name and logo aren't licensed for other uses. Some features may later be
offered under a separate commercial licence. [NOTICE](NOTICE) lists the
code Echowa started from and its licence, and `echowa licenses` prints the
licences of all the third-party software a release includes.

## Contributing and support

Bug reports and feature requests are welcome as GitHub issues. Pull
requests aren't accepted for now: [CONTRIBUTING.md](CONTRIBUTING.md) says
why and how to report a bug well.

Found a security problem? Don't open an issue. [SECURITY.md](SECURITY.md)
says how to report it privately.

## Development

Needs Go, Node and [OrbStack](https://orbstack.dev). Echowa runs in an
OrbStack Ubuntu machine with its own Docker and Swarm, set up by
`deploy/dev-vm.sh`.

```bash
cp .env.example .env
go run . artisan key:generate
npm install
deploy/dev-vm.sh up
printf '%s\n' 'choose-a-password' | deploy/dev-vm.sh artisan user:create --email you@example.com --password-stdin
npm run dev                    # in one terminal
air                            # in another: http://localhost:3000
```

Deployed apps are reached at `http://<app>.localhost:8090`. `make check`
runs every check, and `make build` makes a release binary for this machine.

Built on [Goravel](https://www.goravel.dev) with an Inertia + React frontend.
