# Server setup

Everything a server needs to run Echowa: Docker Engine, a single-node
Swarm, the `echowa-public` network, Traefik with Let's Encrypt, and Echowa
itself as a systemd service. One command sets it all up on a fresh Ubuntu
or Debian server:

```bash
curl -fsSL https://get.echowa.dev/install.sh | sudo bash
```

`install.sh` downloads the newest release for the server's CPU, checks it
against the checksum in `latest.json`, and runs `echowa setup`, which does
the rest. Everything it writes (the units, the env template, the Traefik
stack) is compiled into the binary, in `app/setup/files/`.

| File | What it does |
|---|---|
| `install.sh` | The one-line installer, published to the release channel by `make publish`. |
| `traefik/stack.local.yml` | Traefik for local development and the dev machine: `:80` only, no TLS. |
| `dev-vm.sh` | The OrbStack dev machine for working on Echowa. |

## Production server

You need:

- a fresh Ubuntu 24.04 or 26.04, Debian 12 or Debian 13 server with a public
  IPv4 address and root or sudo. Other Ubuntu releases, and distributions built on
  Ubuntu, install with a warning that they aren't tested.
  Echowa owns the server: Traefik takes ports 80 and 443, and setup refuses
  to start if anything already listens there;
- a domain whose DNS you control.

### 1. Firewall

Only SSH, HTTP and HTTPS should be reachable from the internet, plus 8192
for the UI with HTTP or HTTPS access (step 3). Swarm's own ports (2377,
7946, 4789) must not be. On the server:

```bash
sudo ufw default deny incoming
sudo ufw default allow outgoing
sudo ufw allow OpenSSH
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 8192/tcp     # the UI, with HTTP access
sudo ufw enable
```

Ports that Docker publishes skip ufw's rules. Here that's Traefik's 80 and
443, its 8192 with HTTPS access, and the port of any database you turn External access on for
(["External access to databases"](#external-access-to-databases)). If your provider has a cloud firewall (on
Lightsail, the instance's Networking tab), apply the same rules there.

### 2. Point DNS at the server

For each app domain, create an `A` record pointing at the server's IPv4
address, plus an `AAAA` record if the server has IPv6. For example:

| Name | Type | Value |
|---|---|---|
| `blog.example.com` | A | `203.0.113.10` |
| `*.apps.example.com` | A | `203.0.113.10` |

The wildcard is optional. It lets every `<name>.apps.example.com` reach the
server without a new record per app.

Let's Encrypt checks a domain by fetching
`http://<domain>/.well-known/acme-challenge/...` from the internet. So before
an app with TLS is deployed, its name must resolve to this server and port
80 must be open. Check from your own machine:

```bash
dig +short blog.example.com
```

If you use Cloudflare, set records to "DNS only" (grey cloud) at least until
the first certificate is issued.

The UI's own domain, with HTTPS access, must stay "DNS only" for good.
Cloudflare's proxy only passes certain ports on, and 8192 isn't one, so a
proxied record makes the UI time out. Setup warns when it sees one. If you
want Cloudflare in front of the UI, choose Cloudflare Tunnel instead.

### 3. Install

On the server:

```bash
curl -fsSL https://get.echowa.dev/install.sh | sudo bash
```

First it asks how you'll reach the UI:

| Choice | URL | What it takes | Trade-off |
|---|---|---|---|
| 1. HTTP (default) | `http://<server>:8192` | Port 8192 open in your firewall | Works at once, but your password and session cross the network unencrypted. For trying Echowa out. |
| 2. HTTPS | `https://<domain>:8192` | A domain pointing at the server (with Cloudflare, "DNS only": its proxy doesn't pass port 8192 on), and port 8192 open in your provider's firewall | Traefik serves it with a Let's Encrypt certificate, on a port of its own: allow 8192 from your own addresses only and the login page is off the internet, while apps keep 80 and 443. |
| 3. Cloudflare Tunnel | `https://<hostname>` | A Cloudflare account, and the tunnel's token (step 5) | No open port; Cloudflare Access can check who you are before the login page loads. The safest. |

Answer in advance, for an unattended install, with flags:

```bash
curl -fsSL https://get.echowa.dev/install.sh | sudo bash -s -- --access https --domain echowa.example.com --with-backups
```

`--access` takes `http`, `https` or `tunnel`; `--tunnel-token <token>`
means the tunnel. Without a terminal and without flags (cloud-init, say),
it picks HTTP. In a terminal the choice is a menu (arrow keys and enter,
or the number), a domain or token that won't do is asked again, and
Ctrl-C (or Esc, in the menu) stops before anything is installed.

Then it shows each step, with a spinner while it runs and a ✓ with what
it did once it's done. `NO_COLOR` drops the colour. Without a terminal it
writes plain lines: one when a step starts something slow, and one when
each step is done. In order, `echowa setup`:

1. checks this is a fresh Ubuntu or Debian server, with nothing on ports 80
   and 443;
2. installs Docker Engine from Docker's apt repository for that OS;
3. creates a single-node Swarm and the `echowa-public` network;
4. deploys Traefik with Let's Encrypt (no contact email needed), and with
   HTTPS, port 8192 and a route from your domain on it to Echowa;
5. creates the `echowa` user, whose only privilege is the docker group;
6. writes `/etc/echowa/env` with a new `APP_KEY`, and where Echowa listens
   and `APP_URL` for the access you chose;
7. installs `/usr/local/bin/echowa`, the `echowa-artisan` wrapper and the
   systemd units, including the daily backup (`--with-backups`), and with a
   tunnel, cloudflared;
8. migrates the database, starts Echowa, checks that it answers, and says
   what's left to do.

Running it again changes nothing that's already right. That's also how you
upgrade. `sudo echowa setup --help` lists every option, including
`--advertise-addr` for a server with several addresses.

Prefer to check the installer's signature before running it? Download it
and its signature, check them with the public key from the repository, then
run it:

```bash
curl -fsSLO https://get.echowa.dev/install.sh
curl -fsSLO https://get.echowa.dev/install.sh.minisig
minisign -V -p minisign.pub -m install.sh     # app/release/minisign.pub
sudo bash install.sh --with-backups
```

The installer ends with a one-time link to the setup page, for example
`http://203.0.113.10:8192/setup?token=…`. Open it, and enter a name, email
and password for the admin account. Until the admin exists, the UI shows
the setup page to everyone, but only the link opens the form.
Lost it? Print it again:

```bash
sudo echowa-artisan setup:link
```

Forgot the password later? `sudo echowa-artisan user:create` resets it, and
signs every browser out.

**Back up `APP_KEY` now.** It's in `/etc/echowa/env`. Registry tokens and
env values are encrypted with it. A database without its key can't be
decrypted, and a lost key can't be recovered. Keep it in a password manager,
not next to the database backups.

Echowa needs only `/etc/echowa/env` and `/var/lib/echowa`. It migrates its
database when it starts, and the frontend is built in. Logs go to the
journal (`journalctl -u echowa -f`) and to `/var/lib/echowa/storage/logs/`.

### 4. Smoke test with whoami

Check Traefik and DNS with a throwaway service, routed the way Echowa routes
apps, on one of your domains:

```bash
sudo docker service create --name whoami --network echowa-public \
  --label traefik.enable=true \
  --label 'traefik.http.routers.whoami.rule=Host(`whoami.example.com`)' \
  --label traefik.http.routers.whoami.entrypoints=websecure \
  --label traefik.http.routers.whoami.tls.certresolver=le \
  --label traefik.http.services.whoami.loadbalancer.server.port=80 \
  --label traefik.swarm.lbswarm=true \
  traefik/whoami:v1.12.0
```

Within a minute `sudo docker service logs traefik_traefik` should show no
ACME errors, and `curl https://whoami.example.com` should print whoami's
request dump, with a trusted certificate. Then remove it:

```bash
sudo docker service rm whoami
```

Check DNS first (step 2): each failed certificate attempt counts against
Let's Encrypt's rate limits, which allow only a few failures an hour per
domain.

### 5. Reach the UI

Whoever can sign in controls Docker, which is root on the server. What's
left depends on what you chose in step 3.

**HTTP.** Open `http://<server's public address>:8192` once port 8192 is
open in your firewall. Use it to try Echowa, then move to HTTPS or a
tunnel ("Changing how the UI is reached", below).

**HTTPS.** Point the domain at the server with an `A` record (step 2),
allow TCP port 8192 in your provider's cloud firewall, then open
`https://<domain>:8192`. Docker publishes the port, so ufw's rules don't
apply to it (step 1): limit it to your own addresses in the provider's
firewall, and only the UI is closed to everyone else. Traefik requests the
certificate on the first visit after the name resolves, through port 80.
Only port 8192 serves the UI: `https://<domain>` on 443 answers 404.
Cookies are `Secure` and responses carry HSTS.

**Cloudflare Tunnel.** cloudflared on the server dials out to
Cloudflare, so nothing listens publicly. In the Cloudflare dashboard, under
Zero Trust:

1. **Put Access in front first,** so the login page is never public:
   Access, Applications, Add an application, Self-hosted, **Public DNS**.
   Enter the hostname (e.g. `echowa.example.com`), and add a policy:
   Allow, Include, Emails, your email. With no other login method,
   Cloudflare emails you a one-time code; Settings, Authentication can add
   Google or GitHub for one click. A longer session duration (a month)
   means you see it rarely.
2. **Create the tunnel:** Networks, Tunnels, Create a tunnel, Cloudflared.
   Its install page shows `cloudflared service install <token>`. The long
   string is the token setup asks for (or `--tunnel-token`). Setup installs
   cloudflared and runs that command for you. Keep the token to yourself:
   it lets whoever has it run your tunnel.
3. **Add a route** to the tunnel: a published application, your hostname,
   service HTTP `127.0.0.1:3000`. Cloudflare creates the DNS record.
4. Open `https://<hostname>`. If your browser says the server can't be
   found right after adding the route, your network has cached the
   failed lookup from before the record existed. A phone on mobile data
   shows it at once; otherwise it clears within 30 minutes.

Echowa takes the client address from `CF-Connecting-IP`, only on requests
from loopback (cloudflared), so the login limit applies per real client.

With the tunnel, Echowa listens on `127.0.0.1:3000`, so Tailscale
(`sudo tailscale serve --bg 3000`) or an SSH tunnel
(`ssh -L 3000:127.0.0.1:3000 you@server`, then http://localhost:3000) work
too. Set `APP_URL` to the URL you open.

### Changing how the UI is reached

Setup only chooses on a first install; reruns and updates keep
`/etc/echowa/env` as it is. To change it by hand, edit these lines in
`/etc/echowa/env`, then `sudo systemctl restart echowa`:

| To | `APP_URL` | `APP_HOST` | `APP_PORT` | Also |
|---|---|---|---|---|
| HTTP | `http://<server>:8192` | `0.0.0.0` | `8192` | Open port 8192 |
| Tunnel | `https://<hostname>` | `127.0.0.1` | `3000` | `sudo apt-get install cloudflared` from Cloudflare's repository, then `sudo cloudflared service install <token>`, and the dashboard steps above |
| HTTPS | `https://<domain>:8192` | the bridge gateway (`docker network inspect docker_gwbridge`) | `3000` | `ECHOWA_CLIENT_IP_HEADER=X-Real-IP`, `ECHOWA_TRUSTED_PROXIES=<the bridge subnet>`, a route in `/etc/echowa/traefik/echowa.yml` like the one setup writes (`app/setup/access.go`, `renderRoute`), then `sudo echowa setup` so Traefik publishes port 8192 |

Leaving HTTPS, delete `/etc/echowa/traefik/echowa.yml` and run
`sudo echowa setup`, which drops Traefik's port 8192 (do that before
moving to HTTP, whose Echowa needs the port). Leaving the tunnel,
`sudo cloudflared service uninstall`. Leaving HTTP or HTTPS for the
tunnel, close port 8192.

Webhooks from CI (build step 8) will get their own listener, mapped to a
second tunnel hostname without Access. The UI's port stays private.

### Backups

A consistent copy, safe while Echowa runs (SQLite's `VACUUM INTO`; no
sqlite3 needed):

```bash
sudo echowa-artisan backup /var/backups/echowa/echowa-$(date -u +%F).sqlite
```

`--with-backups` adds `echowa-backup.timer`, which does this daily and keeps
14 days in `/var/backups/echowa`. Copy that folder off the server (restic,
rsync, a provider snapshot). The copies on the server only protect against
mistakes, not a lost disk.

Again: **the backups don't contain `APP_KEY`**. Without it, every encrypted
column in them is noise. Keep the key with your other secrets.

To restore: `sudo systemctl stop echowa`, copy the backup over
`/var/lib/echowa/echowa.sqlite` as `echowa:echowa` with mode 0600, delete
`echowa.sqlite-wal` and `echowa.sqlite-shm` next to it, then start Echowa.
Deployed apps keep running throughout; Echowa only manages them.

### Backing up template apps

Echowa's own backup holds its database, not your apps' data. A
PostgreSQL, MySQL or Redis app made from a template keeps its data in a
Docker volume, `echowa-<app>-data`, which survives deploys but not
deleting the app.

**PostgreSQL and MySQL apps** have a **Backups** section that dumps the
app's database (the one its connection URL names) to S3-compatible
storage: AWS S3, Cloudflare R2, Backblaze B2, Wasabi, Hetzner or your
own. Other databases you made by hand in the same server aren't
included. Redis apps have no Backups section; use the commands further
down.

1. **Add a storage location.** In **Settings, S3 storage**, click **Add
   location** and fill in the endpoint, region, bucket, an optional
   folder (the server's host name is a good one), and an access key.
   For R2 the region is `auto`. Turn on path-style addressing only for
   a provider that needs it (most self-hosted servers). Click **Test**,
   which writes, reads, lists and deletes a small object, and names the
   step that failed.
2. **Back up now** from the app's Backups section, or **Add schedule**:
   a schedule, a time zone and how many successful backups to keep (7
   by default). After each success, the schedule deletes its oldest
   backups past that number. Backups made by hand, and the ones taken
   before a restore, are never deleted for you. If a scheduled backup
   fails or is skipped, the app's Overview says so until you dismiss it.
3. **Restore** from a backup's menu in the History, or from **Restore
   from the bucket**, which lists the backups in the location's folder
   for this app, including ones another server made. You type the app's
   name to confirm. By default, Echowa first backs up the current data,
   so you can go back. PostgreSQL restores all or nothing; MySQL restores
   one table at a time and stops at the first error, which is when that
   first backup matters. A backup never restores into an older server.
   **Into a new app** creates a new app on the tag you choose (the same
   major version or newer) and restores into it once it's running.

Each backup is two objects in the bucket,
`<folder>/<app>/<app>-<time>-<id>.dump` (`.sql.gz` for MySQL) and a
`.json` manifest beside it with the app, template, server version, size
and SHA-256. A restore checks the dump against its manifest as it reads
it, and stops before the end if they differ. Echowa needs nothing else to find them again:
after losing the server, install Echowa on a new one, add a location
with the same bucket and folder, then **New app, From a backup**.

Set the bucket up so a leak or a mistake costs little:

- **A key for this bucket only,** allowed `s3:PutObject`,
  `s3:GetObject`, `s3:DeleteObject`, `s3:ListBucket`,
  `s3:ListBucketMultipartUploads` and `s3:AbortMultipartUpload`. On R2,
  an API token with Object Read & Write on the one bucket. The key is
  stored encrypted with `APP_KEY`, and never shown again.
- **A lifecycle rule that aborts incomplete multipart uploads after a
  day.** Echowa aborts its own when a backup fails or it restarts, but
  not if the server is gone.
- **Server-side encryption** from the provider. Echowa doesn't encrypt
  backups itself, so anyone with the bucket's keys can read them.
- **Versioning**, if you want backups that a stolen key can't delete,
  with a lifecycle rule that expires noncurrent versions after a few
  weeks. On a versioned bucket a schedule's deletes only add delete
  markers: the schedule still keeps to its number, but every dump it
  prunes stays as an older version, and is billed, until the rule
  expires it. The key above has no `s3:DeleteObjectVersion`, so it
  can't remove them.
- **No default retention from Object Lock.** With one, AWS wants a
  checksum on every upload, which Echowa doesn't send: Test fails at
  "Write a test object" with "Content-MD5 OR x-amz-checksum- HTTP
  header is required for Put Object requests with Object Lock
  parameters", and so does every backup.

**Export** downloads the database straight to your browser, with nothing
kept on the server or in S3: PostgreSQL's custom format (for
`pg_restore` or pgAdmin) or plain SQL, and gzipped SQL for MySQL.
Deploys of the app wait until the download ends, so for a big database
on a slow connection, back up to S3 and download that instead.
**Restore from a file** takes a file Export or a backup's download made
and restores it into the app, with the same checks as a restore from
the bucket. The upload needs its size plus 1 GiB free beside Echowa's
database (`/var/lib/echowa`), and is deleted when the restore ends.

**When Echowa is down,** or for a Redis app, use these. Each command
runs in the app's container. Find it with:

```bash
c=$(sudo docker ps -q -f label=com.docker.swarm.service.name=echowa-<app>)
```

**PostgreSQL.** `pg_dump` gives a consistent copy while the app runs:

```bash
sudo docker exec "$c" sh -c 'pg_dump -U "$POSTGRES_USER" -d "$POSTGRES_DB" -Fc' > <app>-$(date -u +%F).dump
```

To restore it into the same app, or into a new PostgreSQL app (to move to
a new major version, say):

```bash
sudo docker exec -i "$c" sh -c 'pg_restore -U "$POSTGRES_USER" -d "$POSTGRES_DB" --clean --if-exists --no-owner' < <app>-2026-10-26.dump
```

**MySQL.** `mysqldump` as root, with `--single-transaction`, gives a
consistent copy of the app's database while it runs. MySQL keeps GTIDs,
and a dump that carries them won't restore into a server that has its own,
so leave them out:

```bash
sudo docker exec "$c" sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysqldump -u root --single-transaction --routines --triggers --set-gtid-purged=OFF "$MYSQL_DATABASE"' > <app>-$(date -u +%F).sql
```

To restore it into the same app, or into a new MySQL app:

```bash
sudo docker exec -i "$c" sh -c 'MYSQL_PWD="$MYSQL_ROOT_PASSWORD" mysql -u root "$MYSQL_DATABASE"' < <app>-2026-10-26.sql
```

**Redis.** It saves every write to its append-only file in `/data`. For a
copy, ask it for a snapshot:

```bash
sudo docker exec "$c" sh -c 'redis-cli -a "$REDIS_PASSWORD" --no-auth-warning --rdb /tmp/backup.rdb >/dev/null && cat /tmp/backup.rdb && rm /tmp/backup.rdb' > <app>-$(date -u +%F).rdb
```

With the append-only file on, Redis ignores a `dump.rdb` at startup, so a
restore puts the snapshot in as the append-only file's base, with the app
stopped:

```bash
sudo docker service scale --detach=false echowa-<app>=0
sudo docker run --rm -i -v echowa-<app>-data:/data redis:8 sh -c 'rm -rf /data/appendonlydir && mkdir /data/appendonlydir && cat > /data/appendonlydir/appendonly.aof.1.base.rdb && echo "file appendonly.aof.1.base.rdb seq 1 type b" > /data/appendonlydir/appendonly.aof.manifest && chown -R redis:redis /data/appendonlydir' < <app>-2026-10-26.rdb
sudo docker service scale --detach=false echowa-<app>=1
```

As with Echowa's own backups, copy these off the server.

### External access to databases

A template database is reachable only by apps on the server until you
turn on External access on its overview. Pick a port from 1024 up, other
than the database's default (5432, 3306, 6379): say 4040. Echowa
redeploys the database with that port published on the server's public
IP, and pgAdmin, TablePlus or `psql` connect to `<server IP>:4040` with
the user and password from the app's Environment tab.

**ufw doesn't protect it.** Docker opens published ports in iptables ahead
of ufw's rules, so `ufw allow from <your IP> to any port 4040` leaves the
port open to everyone. Restrict it in your cloud provider's firewall
instead (on Lightsail, the instance's Networking tab): allow the port
from your own IP only. To check, from another network (a phone hotspot,
say), `nc -vz <server IP> 4040` should time out.

**Connections are encrypted.** While external access is on, the database
serves TLS with a certificate from the app's own certificate authority.
Download the CA from the External access panel and have your client
verify the server with it:

| Database | Client settings |
|---|---|
| PostgreSQL | pgAdmin: SSL mode `verify-ca`, Root certificate the CA file. psql: `"host=<server IP> port=4040 user=<user> dbname=<db> sslmode=verify-ca sslrootcert=<app>-ca.crt"` |
| MySQL | `mysql -h <server IP> -P 4040 -u app -p --ssl-mode=VERIFY_CA --ssl-ca=<app>-ca.crt <db>` |
| Redis | `redis-cli -h <server IP> -p 4040 --tls --cacert <app>-ca.crt -a <password>`. The port only speaks TLS. |

Use `verify-ca`, not `verify-full`: the certificate names the app's
internal host, `echowa-<app>`, because Echowa doesn't know your server's
public address. PostgreSQL and MySQL also accept clients that don't ask
for TLS, so set the client to require it as above.

Turning it off, or changing the port, redeploys the database too: a few
seconds without it.

### Connection pooling for PostgreSQL

A PostgreSQL app can run PgBouncer beside it, so many app processes
share a few database connections. On the app's Connection tab, click
**Enable connection pooling**. Echowa starts `echowa-<app>-pooler` on
port 6432 (0.5 CPU, 128 MiB) and shows its connection details under
Connect's: the same user, password and database, on the pooler's host.
Copy the pooled URL into the apps that should use it. Nothing changes
for an app until you do, and the direct URL keeps working.

The pooler runs in transaction mode: a client holds a connection to
PostgreSQL only while a transaction runs, which is how up to 1,000
clients share 50 connections. Anything that relies on a session can
fail through it, so test an app on the pooled URL before you move it
over:

| Feature | Through the pooler |
|---|---|
| Migrations | Run them on the direct URL. |
| `LISTEN` and `NOTIFY` | Notifications are lost. Point listeners at the direct URL. For PostgREST, set `PGRST_DB_CHANNEL_ENABLED=false`. |
| Advisory locks | A session lock stays with whichever connection took it. Use `pg_advisory_xact_lock`, which ends with the transaction. |
| `SET` | Can reach other clients. Use `SET LOCAL` inside a transaction, or `ALTER ROLE <user> SET …`. `search_path` is kept per client. |
| Prepared statements | Work with drivers that prepare in the protocol (libpq, pgx, PostgREST). Laravel on PHP 8.3, or on an image built against libpq older than 17, fails with `prepared statement "pdo_stmt_…" does not exist`: set `PDO::ATTR_EMULATE_PREPARES => true` in the `pgsql` connection's `options`. |

The pooler opens at most 50 connections to PostgreSQL, out of its 100,
which leaves the rest for direct connections, migrations and backups.
It's only reachable from apps on the server; External access still
publishes the database itself.

A redeploy of the database interrupts queries for a few seconds through
the pooler too, about as long as on the direct URL, and apps reconnect
as they would to PostgreSQL. The pooler itself isn't restarted.

If an app can't connect, open the database's Logs tab and pick
**Pooler**: failed logins show there, such as `password authentication
failed`. Connections that succeed aren't logged.

**Turn off** removes the pooler, and apps still on the pooled URL lose
their connection, so move them to the direct URL first.

### Upgrading Echowa

Echowa checks for a new release every day. When there is one, **Settings,
Updates** shows it with its release notes. Click **Update to vX.Y.Z**. Or
from a shell:

```bash
sudo echowa update            # --check only shows what's available
```

Echowa can't replace its own binary, so the button only writes a request
(`/var/lib/echowa/update/request`). A root service, `echowa-update`, picks
it up and:

1. downloads the release and checks its signature against the key built
   into the running Echowa, then its SHA-256;
2. backs up the database to
   `/var/backups/echowa/echowa-before-<version>-<time>.sqlite`;
3. swaps the binary in with a rename, keeping the old one as
   `/usr/local/bin/echowa.previous`;
4. runs the new release's `echowa setup`, which updates the units and
   Traefik, restarts Echowa and waits for it to answer as the new version;
5. if it doesn't within a minute, puts the old binary and the database
   back and starts the old version again.

The Settings page follows along and reloads itself on the new version. The
updater's log is in Settings and in `journalctl -u echowa-update`. Updates
only ever move forward, and the button refuses while a deployment is
queued or running.

Running the installer again (`curl -fsSL https://get.echowa.dev/install.sh
| sudo bash`) also upgrades, with the same backup and swap but without the
automatic rollback.

Deployments in progress are safe:

- A stop lets them finish first, up to `ECHOWA_DEPLOY_TIMEOUT` (10 minutes
  by default), so the restart can take that long.
- Queued deployments wait in the database and run when the new version
  starts.
- Swarm carries on with any service update it started, whatever Echowa
  does.
- If Echowa is killed mid-deploy anyway (a crash, `kill -9`), it marks
  that deployment failed on start. Check the app and deploy again.

To go back to an older release on purpose, from a shell only:

```bash
sudo echowa update --version v1.2.0 --allow-downgrade
```

If the newer release migrated the database, restore the backup taken
before it (above, "Backups"). Every update leaves a backup in
`/var/backups/echowa`; with `--with-backups`, the daily timer deletes them
after 14 days, otherwise delete old ones yourself.

### Upgrading Traefik

Releases of Echowa carry Traefik's version (`app/setup/files/traefik.yml`),
and upgrading Echowa redeploys Traefik when it changed. Traefik's ports are
bound on the host, so the old task stops before the new one starts. Expect a
few seconds without the proxy.

## Labels Traefik reads

Traefik only routes services on `echowa-public` that set
`traefik.enable=true`. Echowa will set these labels on each app's service.
Use the same ones when testing by hand.

| Label | Value | Why |
|---|---|---|
| `traefik.enable` | `true` | Services are ignored without it (`exposedByDefault=false`). |
| `traefik.http.routers.<name>.rule` | ``Host(`<domain>`)`` | Which requests go to this app. |
| `traefik.http.routers.<name>.entrypoints` | `websecure` (TLS on) or `web` (TLS off) | Always set it. A router without entrypoints listens on all of them, so it would also answer plain HTTP. |
| `traefik.http.routers.<name>.tls.certresolver` | `le` | TLS on only. Gets and renews the certificate. |
| `traefik.http.services.<name>.loadbalancer.server.port` | the container port | Swarm services don't expose ports to Traefik on their own. |
| `traefik.swarm.lbswarm` | `true` | Traefik sends traffic to the service's Swarm VIP instead of task IPs. Swarm then removes stopped tasks from rotation at once, which a `start-first` update needs for zero downtime. Without it Traefik re-reads task IPs every 5s and can hit a task that just stopped. |

On a server, `web` redirects every request to HTTPS except routers that are
explicitly on `web`. An app with TLS off is therefore served over plain HTTP.
That's only useful behind another TLS-terminating proxy.

## Local development (macOS)

The usual setup is the OrbStack dev machine from `deploy/dev-vm.sh up`,
which does everything in this section inside an Ubuntu machine. It puts
Traefik on port 8090 and leaves your Mac's Docker alone.

To use your Mac's own Docker instead: Docker Desktop or OrbStack, a
one-node Swarm, and Traefik on `:80` without TLS. Apps are reached at `http://<name>.localhost`, which browsers and curl
resolve to your Mac without any DNS setup.

### 1. Free port 80

Laravel Valet and Herd run nginx on `127.0.0.1:80`. Docker can't publish
Traefik's port while that's running, and `whoami.localhost` would hit
nginx's 404 page instead. Check:

```bash
lsof -nP -iTCP:80 -sTCP:LISTEN
```

If nginx shows up, stop it while you work on Echowa (`valet stop`, or quit
Herd), or use another port, as described in step 3.

### 2. Create the Swarm and network

From the repository root:

```bash
docker swarm init
docker network create --driver overlay --attachable echowa-public
```

If `docker swarm init` says it can't choose an address, add
`--advertise-addr 127.0.0.1`.

### 3. Deploy local Traefik

```bash
docker stack deploy --detach=false -c deploy/traefik/stack.local.yml traefik
```

To keep Valet or Herd running, publish Traefik on another port instead, and
add that port to every URL (`http://whoami.localhost:8080`):

```bash
TRAEFIK_HTTP_PORT=8080 docker stack deploy --detach=false -c deploy/traefik/stack.local.yml traefik
```

### 4. Try it with whoami

```bash
docker service create --detach=false --name whoami --network echowa-public \
  --label traefik.enable=true \
  --label 'traefik.http.routers.whoami.rule=Host(`whoami.localhost`)' \
  --label traefik.http.routers.whoami.entrypoints=web \
  --label traefik.http.services.whoami.loadbalancer.server.port=80 \
  --label traefik.swarm.lbswarm=true \
  traefik/whoami:v1.12.0

curl http://whoami.localhost
```

The first lines of the output are `Hostname: <container id>` and the
container's IPs. Open http://whoami.localhost in a browser too. Traefik
picks up new services within 5 seconds. A `404 page not found` in plain text
right after creating the service means it hasn't refreshed yet.

### Tear down

```bash
docker service rm whoami
docker stack rm traefik
docker network rm echowa-public      # after the stack has gone
docker swarm leave --force           # only if you don't want Swarm any more
```

## Troubleshooting

| Symptom | Likely cause |
|---|---|
| Traefik's plain-text `404 page not found` for your domain | The service is missing `traefik.enable=true`, isn't on `echowa-public`, or the `Host` rule doesn't match. Check with `docker service inspect <name> --format '{{json .Spec.Labels}}'`. Labels must be service labels (`--label`), not container labels (`--container-label`). |
| `Gateway Timeout` or `Bad Gateway` | The `server.port` label doesn't match the port the app listens on, or the app isn't ready yet. |
| ACME `connection refused`, `timeout` or `unauthorized` | DNS doesn't point at this server yet, or port 80 is blocked by a firewall or CDN proxy. |
| ACME `rateLimited` | Too many failed attempts. Fix DNS or the firewall (steps 1 and 2), then wait an hour before deploying the app again. |
| `echowa setup` stops with "the Swarm is in state 'pending'" or "error" | A previous join or init failed half way. Run `docker swarm leave --force` and rerun. |
| Locally, `whoami.localhost` shows a Valet or Herd 404 page | nginx owns port 80. See local step 1. |
