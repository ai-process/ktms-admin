# llm-proxy-admin

[![Test](https://github.com/ai-process/llm-proxy-admin/actions/workflows/test.yml/badge.svg)](https://github.com/ai-process/llm-proxy-admin/actions/workflows/test.yml)
[![Docker image](https://img.shields.io/docker/v/coyl/llm-proxy-admin?label=docker%20hub&sort=semver)](https://hub.docker.com/r/coyl/llm-proxy-admin)
[![License: MIT](https://img.shields.io/badge/license-MIT-blue.svg)](LICENSE)

A small web console for [**llm-proxy**](https://github.com/ai-process/llm-proxy) (the LLM gateway) and
[**light-tokenmeter**](https://github.com/ai-process/light-tokenmeter) (the usage meter). Use it with both,
or with either one alone.

- **Spend:** who is spending your tokens, and are they worth it? Per project, user, model, action or vendor,
  with failure rates, latency, estimated cost and a *greedy / worthy* verdict.
- **Gateway:** manage models, routing rules, client API keys and vendor credentials, and try a prompt in the
  playground. No more `grpcurl`.
- **Sign-in with Google**, limited to an allowlist with wildcards such as `*@example.com`. Optional read-only users.
- **Tiny:** one static Go binary, the UI embedded (no front-end build, no framework), small image.

## Contents

- [Quick start](#quick-start)
- [Setting up Google sign-in](#setting-up-google-sign-in)
- [Who may sign in](#who-may-sign-in)
- [Running with one backend or both](#running-with-one-backend-or-both)
- [Configuration reference](#configuration-reference)
- [Deployment](#deployment)
- [What the pages do](#what-the-pages-do)
- [Security model](#security-model)
- [Development](#development)

## Quick start

You need a Google OAuth client ([how](#setting-up-google-sign-in)) and, for the gateway pages, an llm-proxy
admin key (`MintAPIKey` with scope `admin`).

```bash
docker run -d --name llm-proxy-admin -p 8080:8080 \
  -e PUBLIC_URL=https://admin.example.com \
  -e GOOGLE_CLIENT_ID=… -e GOOGLE_CLIENT_SECRET=… \
  -e ALLOWED_EMAILS='*@example.com' \
  -e SESSION_SECRET="$(openssl rand -base64 36)" \
  -e LLM_PROXY_ADDR=llm-proxy:9090 -e LLM_PROXY_ADMIN_KEY=llm_… \
  -e TOKENMETER_URL=http://light-tokenmeter:8080 \
  coyl/llm-proxy-admin:latest
```

Open `PUBLIC_URL`, sign in with a Google account that matches `ALLOWED_EMAILS`, and you are in.
[`examples/compose.yaml`](examples/compose.yaml) starts the whole family (llm-proxy, light-tokenmeter and
this console on one Postgres) with the first-run steps in its header.

**Just looking around on your machine?** Skip Google entirely, on localhost only:

```bash
docker run --rm -p 8081:8080 -e PUBLIC_URL=http://localhost:8081 -e INSECURE_NO_AUTH=true \
  -e TOKENMETER_URL=http://host.docker.internal:8080 coyl/llm-proxy-admin:latest
```

`INSECURE_NO_AUTH` is refused unless `PUBLIC_URL` host is `localhost`, `127.0.0.1`, or `[::1]`.

## Setting up Google sign-in

1. In the [Google Cloud console](https://console.cloud.google.com/apis/credentials) create an
   **OAuth client ID** of type **Web application** (configure the consent screen first; *Internal* works
   if everyone is in your Google Workspace, otherwise *External*).
2. Under **Authorized redirect URIs** add `PUBLIC_URL` + `/auth/callback`, for example
   `https://admin.example.com/auth/callback` (and `http://localhost:8080/auth/callback` for local tests).
3. Put the client id and secret into `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`.

The console uses the authorization-code flow with PKCE, state and nonce, and requires a **verified** e-mail.
Only the `openid email profile` scopes are requested.

## Who may sign in

`ALLOWED_EMAILS` is a comma- or space-separated list of patterns matched against the verified Google
address, case-insensitively. `*` matches any run of characters within the part before or after the `@`,
never across it.

| Pattern | Admits |
| --- | --- |
| `alice@example.com` | exactly that address |
| `*@example.com` | everyone at example.com (not `x@sub.example.com`) |
| `*@*.example.com` | everyone on any subdomain of example.com |
| `ops-*@example.com` | `ops-eu@example.com`, `ops-us@example.com`, … |
| `*@*` | every verified Google account (the explicit "open to all", logged at start) |

Patterns need exactly one `@`; a bare `*` or `example.com` is rejected at startup so a typo can never open
the door. An empty list admits nobody.

- **`ALLOWED_EMAILS`** get full access.
- **`VIEWER_EMAILS`** (optional, same syntax) get a read-only console: they see usage, models, rules and keys
  but every change is refused. An address in both lists is an admin.
- Lists are checked on **every request**, so removing someone ends their access immediately, not at
  session expiry. Changing a list needs a restart.

## Running with one backend or both

The console shows only the pages whose backend you configured.

| You set | You get |
| --- | --- |
| `TOKENMETER_URL` | **Usage** page. Nothing else. |
| `LLM_PROXY_ADDR` + `LLM_PROXY_ADMIN_KEY` | **Models, Rules, Keys, Playground**. |
| both | Everything, plus **estimated cost** on the usage page (tokens × the prices on your models; estimates use standard in/out prices and do not account for peak or batch rates). |

The sidebar shows a green or red dot per backend, so a wrong address or a revoked key is visible at once.

## Configuration reference

All settings are environment variables; see [`env.example`](env.example).

| Variable | Default | Description |
| --- | --- | --- |
| `PUBLIC_URL` | – | **Required.** External base URL, e.g. `https://admin.example.com`. |
| `GOOGLE_CLIENT_ID`, `GOOGLE_CLIENT_SECRET` | – | **Required** (unless `INSECURE_NO_AUTH`). |
| `ALLOWED_EMAILS` | – | Admin patterns. At least one of the two lists must be non-empty. |
| `VIEWER_EMAILS` | empty | Read-only patterns. |
| `SESSION_SECRET` | random | 32+ characters; signs the session cookie. Set it, or sessions end at every restart and cannot be shared between replicas. |
| `SESSION_TTL` | `12h` | Session lifetime. |
| `LLM_PROXY_ADDR` | empty | `host:port` of llm-proxy's gRPC endpoint. |
| `LLM_PROXY_ADMIN_KEY` | – | Key with the `admin` scope. Required with `LLM_PROXY_ADDR`. |
| `LLM_PROXY_TLS` | `false` | Use TLS to reach llm-proxy. |
| `TOKENMETER_URL` | empty | Base URL of light-tokenmeter's HTTP port. |
| `LISTEN_ADDR` | `:8080` | Listen address. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `INSECURE_NO_AUTH` | `false` | Skip sign-in. Localhost `PUBLIC_URL` only. |
| `OIDC_ISSUER` | Google | Advanced: another OpenID Connect issuer that returns verified e-mails. |

## Deployment

- **TLS.** The console speaks plain HTTP; terminate TLS in front of it (Caddy, nginx, Traefik, a cloud load
  balancer) and set `PUBLIC_URL` to the `https://` address. Cookies get the `Secure` flag from that scheme.
- **Replicas.** It is stateless. Run several if you like, with the same `SESSION_SECRET`.
- **Health.** `GET /healthz` returns `200` without authentication (used by the image's `HEALTHCHECK`).
- **Network.** The console must reach llm-proxy's gRPC port and light-tokenmeter's HTTP port; neither needs
  to be public. Prefer keeping them private and exposing only the console.

A minimal Caddy front:

```
admin.example.com {
    reverse_proxy llm-proxy-admin:8080
}
```

## What the pages do

**Usage** (needs light-tokenmeter). Pick a range (7/30/90 days or custom) and a grouping (project, model,
user, action, vendor or total). You get tiles for tokens, requests, failure rate, latency and estimated
cost, a per-day chart, and a table per group with share of all tokens, tokens per request, failure rate,
latency and cost.

Rows are labelled:

- **greedy**: at least 10% of all tokens at 1.5× the average tokens per request or more, *or* at least 5%
  of all tokens with a fifth or more of them spent on failed requests.
- **worthy**: at least 1% of all tokens, at or below the average per request, with 5% failures or fewer.

These are heuristics for spotting where to look, not accounting. They need two or more groups and at least
five requests in the group.

**Models** and **Rules** (need llm-proxy). Add, edit and delete the registry entries and the routing rules
that choose between them. Saves go through llm-proxy's own validation, so an invalid change (unknown model,
missing catch-all rule, duplicate priority) is rejected with its message and nothing changes.

**Keys.** Mint client API keys (the secret is shown once), revoke them, assign vendor credentials to clients
(write-only: stored encrypted by llm-proxy and never shown again) and re-encrypt all credentials after
adding a new encryption key version.

**Playground.** Send a prompt through the same routing your clients use, with a chosen effort, model and
attributes. The call is attributed to `console.playground` and your e-mail.

## Security model

- The console holds llm-proxy's admin key **on the server**; it never reaches the browser, and vendor keys are
  write-only.
- Session cookies are signed (HMAC-SHA256), `HttpOnly`, `SameSite=Lax` and `Secure` over HTTPS. They carry
  only name, e-mail and expiry; the role is recomputed from the allowlists on every request.
- Anything that changes state needs the admin role, a custom `X-Requested-With` header (which a cross-site
  page cannot send without a CORS preflight this server never grants) and a same-origin `Origin` when one is
  sent.
- A strict Content-Security-Policy (`default-src 'self'`, no inline script or style), no framing, and all UI
  text is inserted as text, never as HTML.
- There are no CORS headers and no third-party scripts, fonts or analytics.
- Report problems privately, see [SECURITY.md](SECURITY.md).

## Development

```bash
make test    # go test -race ./...
make dev     # run on :8080 without Google sign-in (set LLM_PROXY_* and/or TOKENMETER_URL)
make proto   # regenerate internal/gen from proto/ (needs buf)
```

`internal/web/static` is plain HTML, CSS and ES modules served as-is. `proto/` is a copy of llm-proxy's
protocol, so the console talks to it over gRPC without importing its code.

Layout: `cmd/llm-proxy-admin` (entrypoint), `internal/auth` (Google sign-in, sessions, allowlists),
`internal/api` (JSON API and guards), `internal/usage` (light-tokenmeter client and the greedy/worthy
analysis), `internal/config`, `internal/web` (embedded UI).

### Releasing

Pushing a git tag publishes a multi-arch image to Docker Hub as `coyl/llm-proxy-admin:<tag>` (and `latest`
for semver tags). The repository needs the secrets `DOCKER_USERNAME` and `DOCKER_PASSWORD`.

```bash
git tag 0.1.0 && git push origin 0.1.0
```

Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under the [MIT License](LICENSE).
