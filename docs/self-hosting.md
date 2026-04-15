# Self-hosting Krimi

## Overview
Krimi now self-hosts with:
- a Vue frontend built into `dist/`
- a Go backend with SQLite persistence in `.self_host/data/krimi.sqlite`
- Caddy serving the SPA and proxying `/api*`, `/ws*`, and `/healthz`
- a tmux-managed backend session started by `self_host.zsh`

The default public origin is `https://krimi.pinky.lilf.ir`. When the public origin uses HTTPS, the script also provisions an HTTP fallback for the same host.

## Prerequisites
- `tmux`
- `caddy`
- `go`
- `cc` (for the SQLite CGO build, e.g. `gcc` or `clang`)
- `pnpm`
- `python3`
- `ss`
- `nvm-load` available in login zsh shells

Optional proxy variables are passed through when present (`ALL_PROXY`, `all_proxy`, `http_proxy`, `https_proxy`, `HTTP_PROXY`, `HTTPS_PROXY`). The script does not hardcode a proxy.

## Commands
```zsh
./self_host.zsh setup [public_url]
./self_host.zsh redeploy [public_url]
./self_host.zsh start [public_url]
./self_host.zsh stop
```

- `setup`: stops the current backend, persists config, installs pnpm deps if needed, builds frontend/backend, updates `~/Caddyfile`, reloads Caddy, and starts the backend.
- `redeploy`: rebuilds from the current working tree and restarts the app.
- `start`: starts from saved artifacts/config and refreshes the managed Caddy block.
- `stop`: stops the tmux session.

If `public_url` omits a scheme, the script assumes `https://`.

## Runtime layout
- Config: `.self_host/config.env`
- Backend binary: `.self_host/bin/krimi-server`
- SQLite DB: `.self_host/data/krimi.sqlite`
- Logs: `.self_host/logs/api.log`
- Backend tmux session: `krimi-api`

## Caddy behavior
The script manages a `# BEGIN krimi self-host` / `# END krimi self-host` block in `~/Caddyfile`.

For HTTPS origins it creates:
- `https://<host>` with `tls internal`
- `http://<host>` as a plain HTTP fallback

Both entries:
- serve the built SPA from `dist/`
- route `/api*`, `/ws*`, and `/healthz` to `127.0.0.1:18082`
- use SPA fallback via `try_files {path} /index.html`

## Adaptive WebSocket behavior
The frontend never hardcodes `ws://` or `wss://`.
It derives the WebSocket URL from the current page origin:
- `http:` pages connect with `ws:`
- `https:` pages connect with `wss:`

That means the same frontend build works behind both the HTTPS site and the HTTP fallback.

## HTTPS trust
The HTTPS site uses Caddy `tls internal`, so browsers may require the local Caddy root CA to be trusted.
If needed, trust the local Caddy CA on the host or import Caddy’s generated root certificate into client devices.

## Notes
- Clipboard copy prefers `navigator.clipboard` on secure contexts and falls back to `document.execCommand('copy')` on HTTP.
- All required runtime assets are bundled locally; the app does not depend on Firebase, Google Fonts, or CDN-hosted icon fonts.
- Room data expires automatically after 7 days of inactivity.
