# Contributing

Thanks for wanting to help. Echowa is open to issues and closed to pull
requests, for now.

## Issues are welcome

Open a GitHub issue for a bug, a rough edge or a feature you'd like. For a
bug, include:

- the Echowa version (Settings, Updates, or `echowa version`);
- the server's OS and CPU (for example Ubuntu 24.04 on arm64);
- how the UI is reached (HTTP, HTTPS or a Cloudflare Tunnel);
- what you did, what you expected and what happened;
- anything relevant from `journalctl -u echowa`, with secrets, domains and
  addresses you'd rather not share taken out.

For a feature, say what you're trying to do rather than how Echowa should
do it. The [README](README.md) lists what's deliberately out of scope; a
request for one of those is still worth making if you say why.

Security problems don't go in issues: see [SECURITY.md](SECURITY.md).

## Pull requests aren't accepted

Please don't open one, even for a typo. It isn't that contributions
aren't valued:

- Echowa is licensed under the FSL (see [LICENSE.md](LICENSE.md)), and
  some features may later be sold under a commercial licence. Accepting
  outside code would mean a contributor agreement for every change, which
  isn't set up.
- Echowa runs with control of Docker on its server, which is close to
  root. Every line in it is written and reviewed by its maintainers, so
  they can stand behind it.

If you've found a fix, describe it in an issue, with a diff in the issue
if that's clearest. It'll be credited in the release notes if it's used.

## Forks

You're free to fork Echowa and change it for your own use under the
licence's terms. A fork can't be offered as a commercial product or
service that competes with Echowa, and can't use the Echowa name or logo.
