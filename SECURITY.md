# Security

Echowa controls Docker on the server it runs on, which is close to root
access. Security reports are taken seriously and handled before anything
else.

## Reporting a vulnerability

Report it privately through GitHub:
[Report a vulnerability](https://github.com/pobl-software/Echowa/security/advisories/new).
Please don't open a public issue, discussion or pull request.

Include what you can of:

- the Echowa version, and how the UI is reached (HTTP, HTTPS or a
  Cloudflare Tunnel);
- what an attacker needs first (network access, a signed-in session, a
  deployed app, a shell on the server);
- steps to reproduce, or a proof of concept;
- what they could do with it.

You'll get a reply within 3 working days. Once a fix is released, an
advisory is published with credit to you, unless you'd rather not be
named. Please give us 90 days, or until a fix is out if that's sooner,
before you publish anything yourself.

## Supported versions

Only the newest release gets fixes. Servers update from Settings, Updates,
or with `sudo echowa update`.

## What's in scope

- Echowa itself: the UI and its routes, sessions, the installer
  (`install.sh` and `echowa setup`), the updater, and how releases are
  signed and checked.
- What Echowa sets up on the server: its systemd units, the Traefik
  configuration, the Swarm services and volumes it creates, and the
  templates' databases.

Out of scope: the apps you deploy with Echowa, Docker, Traefik and the
templates' database images themselves (report those to their projects),
and findings that need root on the server already.

## Hardening a server

[deploy/README.md](deploy/README.md) covers the firewall and how the UI is
reached. Two things worth knowing:

- Ports Docker publishes skip ufw's rules. That's Traefik's 80 and 443,
  8192 with HTTPS access, and any database with External access on. Use
  your provider's firewall for those.
- With HTTP access, the UI is plain HTTP on port 8192. Use HTTPS access or
  a Cloudflare Tunnel for anything reachable from the internet.
