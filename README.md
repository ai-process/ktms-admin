# Keystone. The Model Switchboard. (KTMS): admin console

[![Test](https://github.com/ai-process/ktms-admin/actions/workflows/test.yml/badge.svg)](https://github.com/ai-process/ktms-admin/actions/workflows/test.yml)
[![Docker image](https://img.shields.io/docker/v/coyl/ktms-admin?label=docker%20hub&sort=semver)](https://hub.docker.com/r/coyl/ktms-admin)
[![License: Apache 2.0](https://img.shields.io/badge/license-Apache_2.0-blue.svg)](LICENSE)

A web console for [**Keystone**](https://github.com/ai-process/ktms), the LLM gateway (KTMS, formerly llm-proxy), and
[**light-tokenmeter**](https://github.com/ai-process/light-tokenmeter) (the usage meter). Use it with both,
or with either one alone.

- **Spend:** who is spending your tokens, and are they worth it? Per project, user, model, action or vendor,
  with failure rates, latency, estimated cost and a *greedy / worthy* verdict.
- **Gateway:** manage models, routing rules, client API keys and vendor credentials, and try a prompt in the
  playground. No more `grpcurl`.
- **Sign-in** with a password, Google or any OpenID Connect provider, GitHub, or the access proxy you already
  run (oauth2-proxy, Authelia, Authentik, Pomerium, Cloudflare Access), limited to an allowlist with wildcards such as `*@example.com`. Optional
  read-only users.
- **One binary:** a static Go binary with the UI embedded: Preact and htm as vendored ES modules, no
  front-end build step, light and dark themes, usable from a phone.

## Contents

- [Quick start](#quick-start)
- [Password sign-in](#password-sign-in)
- [Google and other OpenID Connect providers](#google-and-other-openid-connect-providers)
- [GitHub sign-in](#github-sign-in)
- [Sign-in behind an access proxy](#sign-in-behind-an-access-proxy)
- [Who may sign in](#who-may-sign-in)
- [Compatibility](#compatibility)
- [Running with one backend or both](#running-with-one-backend-or-both)
- [Configuration reference](#configuration-reference)
- [Deployment](#deployment)
- [What the pages do](#what-the-pages-do)
- [Security model](#security-model)
- [Development](#development)

## Quick start

You need a sign-in method and, for the gateway pages, a KTMS admin key (`MintAPIKey` with scope `admin`).
The quickest method is a password ([more](#password-sign-in)). Google and other OpenID Connect providers
([how](#google-and-other-openid-connect-providers)), GitHub ([how](#github-sign-in)) and access proxies
([how](#sign-in-behind-an-access-proxy)) work too, alone or together.

```bash
# Prints you@example.com:$argon2id$… after asking for a password twice.
docker run --rm -it coyl/ktms-admin:latest hash-password you@example.com

docker run -d --name ktms-admin -p 8080:8080 \
  -e PUBLIC_URL=https://admin.example.com \
  -e ADMIN_USERS='you@example.com:$argon2id$…' \
  -e ALLOWED_EMAILS='you@example.com' \
  -e SESSION_SECRET="$(openssl rand -base64 36)" \
  -e KTMS_ADDR=ktms:9090 -e KTMS_ADMIN_KEY=ktms_… \
  -e TOKENMETER_URL=http://light-tokenmeter:8080 \
  coyl/ktms-admin:latest
```

Open `PUBLIC_URL`, sign in with that e-mail and password, and you are in.
[`examples/compose.yaml`](examples/compose.yaml) starts the whole family (KTMS, light-tokenmeter and
this console on one Postgres) with the first-run steps in its header.

**Just looking around on your machine?** Skip sign-in entirely, on localhost only:

```bash
docker run --rm -p 8081:8080 -e PUBLIC_URL=http://localhost:8081 -e INSECURE_NO_AUTH=true \
  -e TOKENMETER_URL=http://host.docker.internal:8080 coyl/ktms-admin:latest
```

`INSECURE_NO_AUTH` is refused unless `PUBLIC_URL` host is `localhost`, `127.0.0.1`, or `[::1]`.

## Password sign-in

Passwords live in the configuration as hashes. The console needs no database and no identity provider.

1. Hash a password: `ktms-admin hash-password you@example.com` (or the `docker run` line above). It asks
   twice without echo, or reads one line from a pipe, requires at least 12 characters, and prints an
   `email:hash` entry. The hash is argon2id; bcrypt hashes from `htpasswd -nB` work too.
2. Put the entries in `ADMIN_USERS`, separated by spaces or newlines, or one per line in a file named by
   `ADMIN_USERS_FILE` (lines starting with `#` are comments). Hashes contain `$`, so quote them: single
   quotes in a shell or a `.env` file, `$$` inside `compose.yaml`.
3. Add the same e-mails to `ALLOWED_EMAILS`, `VIEWER_EMAILS` or `TEAM_MEMBERSHIPS`. A password only proves
   who someone is. The allowlists decide whether they get in and with which role.

A malformed entry stops the console at start. To change a password or add someone, edit the entries and
restart. After 5 wrong passwords for one e-mail the console makes that account wait, doubling the wait with
each further failure up to 5 minutes. One client address gets 30 tries before a wait of up to a minute.
The counters are kept in memory per replica. Sign-in forms carry a token tied to a cookie, so another site
cannot post one.

## Google and other OpenID Connect providers

**Google:**

1. In the [Google Cloud console](https://console.cloud.google.com/apis/credentials) create an
   **OAuth client ID** of type **Web application** (configure the consent screen first; *Internal* works
   if everyone is in your Google Workspace, otherwise *External*).
2. Under **Authorized redirect URIs** add `PUBLIC_URL` + `/auth/callback`, for example
   `https://admin.example.com/auth/callback` (and `http://localhost:8080/auth/callback` for local tests).
3. Put the client id and secret into `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET`.

**Another provider** works the same way: register a confidential web client with the redirect URI
`PUBLIC_URL/auth/callback`, then set `OIDC_ISSUER` and, for the button, `OIDC_NAME`:

| Provider | `OIDC_ISSUER` |
| --- | --- |
| Keycloak | `https://keycloak.example.com/realms/<realm>` |
| Zitadel | `https://<instance>.zitadel.cloud` |
| GitLab | `https://gitlab.com` or your instance |
| Authentik | `https://authentik.example.com/application/o/<slug>/` (the `email` scope mapping must return `email_verified: true`) |
| Okta | `https://<org>.okta.com` |

**Several providers at once:** list names in `OIDC_PROVIDERS` (lower-case letters, digits and dashes) and
give each one `OIDC_<NAME>_ISSUER`, `OIDC_<NAME>_CLIENT_ID`, `OIDC_<NAME>_CLIENT_SECRET` and optionally
`OIDC_<NAME>_NAME`. A dash in the name becomes `_` in the variables. Their redirect URI is
`PUBLIC_URL/auth/oidc-<name>/callback`. For example, `OIDC_PROVIDERS=corp-sso` with `OIDC_CORP_SSO_ISSUER=…` and
the redirect URI `https://admin.example.com/auth/oidc-corp-sso/callback`. They can sit next to the unnamed
provider above.

The console uses the authorization-code flow with PKCE, state and nonce, requests only the
`openid email profile` scopes, and requires the provider to mark the e-mail **verified**
(`email_verified`). Microsoft Entra ID does not send that claim, so put it behind an
[access proxy](#sign-in-behind-an-access-proxy) such as oauth2-proxy instead. The older
`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` names still work with a startup warning until 2.0.

## GitHub sign-in

1. Under GitHub **Settings → Developer settings → OAuth Apps** (or your organization's), create an app with the
   **Authorization callback URL** `PUBLIC_URL/auth/github/callback`.
2. Generate a client secret and set `GITHUB_CLIENT_ID` and `GITHUB_CLIENT_SECRET`. For GitHub Enterprise
   Server also set `GITHUB_URL`, such as `https://github.example.com`.

The console asks for `read:user user:email` and uses the account's **primary, verified** e-mail. To limit
access to your organization, list its people or its domain in the allowlists.

## Sign-in behind an access proxy

If the console already sits behind an access proxy that signs people in, let the proxy name the user and
skip the OAuth client. The proxy checks identity on every request, so revoking someone there ends their
console access at once. The console's allowlists still decide who gets in and with which role.

**A proxy that sends the e-mail in a header** (oauth2-proxy, Authelia, Authentik, Pomerium, Tailscale
Serve, or Caddy `forward_auth` / nginx `auth_request` in front of one of them):

1. Make the proxy put the user's e-mail in a request header. Set `AUTH_HEADER_EMAIL` to its name if it is not
   `X-Forwarded-Email`:

   | Proxy | `AUTH_HEADER_EMAIL` |
   | --- | --- |
   | oauth2-proxy as the reverse proxy | `X-Forwarded-Email` (the default; `--pass-user-headers` is on by default) |
   | oauth2-proxy behind nginx `auth_request` | `X-Auth-Request-Email` (with `--set-xauthrequest`, copied by nginx) |
   | Authelia | `Remote-Email` |
   | Authentik proxy outpost | `X-authentik-email` |
   | Pomerium | `X-Pomerium-Claim-Email` (with `jwt_claims_headers: email`) |
   | Tailscale Serve | `Tailscale-User-Login` |

   `AUTH_HEADER_NAME` optionally names a display-name header (`Remote-Name`, `Tailscale-User-Name`, …).
2. Set `TRUSTED_PROXY_CIDRS` to the addresses the proxy connects from, such as `10.0.0.0/8` or `172.18.0.2`.
   The console ignores the header from any other address.
3. **The proxy must set that header on every request and overwrite any value the client sent**, and the
   console must be unreachable except through it. The proxies above do this for their own headers. A plain
   TLS terminator does not, so never put one in `TRUSTED_PROXY_CIDRS` unless it removes the header from
   incoming requests.

**Cloudflare Access:** set `CF_ACCESS_TEAM_DOMAIN` (`myteam` or `myteam.cloudflareaccess.com`) and
`CF_ACCESS_AUD` (the application's *Application Audience (AUD) Tag*). The console verifies the signed
`Cf-Access-Jwt-Assertion` token on every request (signature, issuer, audience, expiry), so it needs no trusted
network path. Service tokens carry no e-mail and are refused.

Proxy sign-in works alongside the other methods. Without the proxy, the sign-in page offers the other methods. Signing out
happens at the proxy, so the console hides its own *Sign out* button for these users.

## Who may sign in

`ALLOWED_EMAILS` is a comma- or space-separated list of patterns matched against the verified e-mail
address, case-insensitively. `*` matches any run of characters within the part before or after the `@`,
never across it.

| Pattern | Admits |
| --- | --- |
| `alice@example.com` | exactly that address |
| `*@example.com` | everyone at example.com (not `x@sub.example.com`) |
| `*@*.example.com` | everyone on any subdomain of example.com |
| `ops-*@example.com` | `ops-eu@example.com`, `ops-us@example.com`, … |
| `*@*` | everyone who can sign in (the explicit "open to all", logged at start) |

Patterns need exactly one `@`; a bare `*` or `example.com` is rejected at startup so a typo can never open
the door. An empty list admits nobody.

- **`ALLOWED_EMAILS`** get full access.
- **`VIEWER_EMAILS`** (optional, same syntax) get a read-only console: they see usage, models, rules and keys
  but every change is refused. An address in both lists is an admin.
- **`TEAM_MEMBERSHIPS`** (optional) gives named people a workspace for one KTMS team:
  `email:team_id[:teamlead|member[:org_id]]`, separated by commas, semicolons or spaces. They see only their
  team: its cabinet (budget, spend as the gateway counts it, allowed models) and its keys. A `teamlead` also
  mints and revokes the team's keys (always `generate` scope, bound to the team and its organization).
  Neither sees other tenants, global config or usage, nor uses Playground or Judge, which run on the
  console's own key outside the team's limits. An entry here overrides a matching `ALLOWED_EMAILS` pattern;
  entries without a team or with another role are skipped and logged.
- Lists are checked on **every request**, so removing someone ends their access immediately, not at
  session expiry. Changing a list needs a restart.

## Compatibility

The console and the gateway share a major version: ktms-admin 1.x works with every KTMS 1.x release,
through the gateway's stable `ktms.v1` admin API. Within 1.x the console's settings (the variables
below) keep their names and meaning; renamed ones keep working with a startup warning until 2.0.
The console's own `/api` serves its web UI only and is not a public API.

| ktms-admin | KTMS gateway |
| --- | --- |
| 1.x | 1.x |
| 0.4.x | 0.14.x |
| 0.3.x and earlier | 0.13.x |

Before 1.0 there is no compatibility promise: use the pairs above.

## Running with one backend or both

The console shows only the pages whose backend you configured.

| You set | You get |
| --- | --- |
| `TOKENMETER_URL` | **Usage** page. Nothing else. |
| `KTMS_ADDR` + `KTMS_ADMIN_KEY` | **Models, Rules, Keys, Nodes, Playground, Judge**. |
| both | Everything, plus **estimated cost** on the usage page (tokens × the prices on your models; estimates use standard in/out prices and do not account for peak or batch rates). |

The sidebar shows a green or red dot per backend, so a wrong address or a revoked key is visible at once.

## Configuration reference

All settings are environment variables; see [`env.example`](env.example).

| Variable | Default | Description |
| --- | --- | --- |
| `PUBLIC_URL` | – | **Required.** External base URL, e.g. `https://admin.example.com`. |
| `ADMIN_USERS` | empty | Turns on [password sign-in](#password-sign-in): `email:hash` entries from `hash-password`. |
| `ADMIN_USERS_FILE` | empty | A file with one `email:hash` entry per line, merged with `ADMIN_USERS`. |
| `OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET` | – | Turns on [OpenID Connect sign-in](#google-and-other-openid-connect-providers), Google unless `OIDC_ISSUER` says otherwise. At least one sign-in method is required unless `INSECURE_NO_AUTH` is set. (`GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET` still work, deprecated.) |
| `OIDC_ISSUER` | Google | Another OpenID Connect issuer that returns verified e-mails. |
| `OIDC_NAME` | Google or SSO | The provider's name on the sign-in button. |
| `OIDC_PROVIDERS` | empty | More providers by name, each with `OIDC_<NAME>_ISSUER`, `_CLIENT_ID`, `_CLIENT_SECRET` and optionally `_NAME`. |
| `GITHUB_CLIENT_ID`, `GITHUB_CLIENT_SECRET` | – | Turn on [GitHub sign-in](#github-sign-in). |
| `GITHUB_URL` | `https://github.com` | GitHub Enterprise Server address. |
| `TRUSTED_PROXY_CIDRS` | empty | Turns on [proxy sign-in](#sign-in-behind-an-access-proxy): the addresses or CIDRs of the access proxy. |
| `AUTH_HEADER_EMAIL` | `X-Forwarded-Email` | The header the proxy names the user's e-mail in. |
| `AUTH_HEADER_NAME` | empty | Optional header with the user's display name. |
| `CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD` | empty | Turn on [Cloudflare Access](#sign-in-behind-an-access-proxy) sign-in. |
| `ALLOWED_EMAILS` | – | Admin patterns. At least one of the two lists must be non-empty. |
| `VIEWER_EMAILS` | empty | Read-only patterns. |
| `TEAM_MEMBERSHIPS` | empty | Team workspaces: `email:team_id[:teamlead\|member[:org_id]]`, see [Who may sign in](#who-may-sign-in). |
| `DEV_USER_ROLE`, `DEV_USER_TEAM_ID`, `DEV_USER_ORG_ID` | admin | With `INSECURE_NO_AUTH`: the role and team of the local dev user. |
| `SESSION_SECRET` | random | 32+ characters; signs the session cookie. Set it, or sessions end at every restart and cannot be shared between replicas. |
| `SESSION_TTL` | `12h` | Session lifetime. |
| `KTMS_ADDR` | empty | `host:port` of the KTMS gateway's gRPC endpoint. (`LLM_PROXY_ADDR` still works, deprecated.) |
| `KTMS_ADMIN_KEY` | – | Key with the `admin` scope. Required with `KTMS_ADDR`. (`LLM_PROXY_ADMIN_KEY` still works, deprecated.) |
| `KTMS_TLS` | `false` | Use TLS to reach the gateway. (`LLM_PROXY_TLS` still works, deprecated.) |
| `TOKENMETER_URL` | empty | Base URL of light-tokenmeter's HTTP port. |
| `LISTEN_ADDR` | `:8080` | Listen address. |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn`, `error`. |
| `INSECURE_NO_AUTH` | `false` | Skip sign-in. Localhost `PUBLIC_URL` only. |

## Deployment

- **TLS.** The console speaks plain HTTP; terminate TLS in front of it (Caddy, nginx, Traefik, a cloud load
  balancer) and set `PUBLIC_URL` to the `https://` address. Cookies get the `Secure` flag from that scheme.
- **Replicas.** It is stateless. Run several if you like, with the same `SESSION_SECRET`.
- **Health.** `GET /healthz` returns `200` without authentication (used by the image's `HEALTHCHECK`).
- **Network.** The console must reach the KTMS gateway's gRPC port and light-tokenmeter's HTTP port; neither needs
  to be public. Prefer keeping them private and exposing only the console.

A minimal Caddy front:

```
admin.example.com {
    reverse_proxy ktms-admin:8080
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

Team budgets sit next to the table, read from the gateway's own spend counters.

**Models** (need KTMS). Add, edit and delete registry entries: vendor, efforts, capabilities, list prices
and per-key limits.

**Rules.** A visual builder: pick a rule card on the left (drag cards to change priority), then set the
effort and attribute conditions, the model chain (drag to reorder, cheapest first; later models are
fallbacks), an optional canary split with weights, and shadow traffic. The *Check it* panel says in words
where a request goes, and the editor flags what the gateway would refuse (no catch-all for an effort, a
model that does not serve the effort, a weight of 0) before you save. Saves still go through the gateway's
own validation.

**Limits.** Per-user rate limits and daily token budgets, and spend caps (USD per UTC day or month) for keys
and users.

**Keys.** A wizard mints client API keys: name and team, access, optional daily and monthly spend caps; the
secret is shown once with a copy button. Revoke keys, assign vendor credentials to clients (write-only:
stored encrypted by the gateway and never shown again) and re-encrypt all credentials after adding a new
encryption key version.

**Tenants** and **Team.** Organizations, teams, their monthly budgets and model allowlists; each team's page
shows its spend this month against the budget and its keys.

**Nodes.** Local GPU nodes with state, queue, VRAM and speed, refreshed every 15 seconds, and their keys.

**Playground.** Send a prompt or multi-turn conversation through the same routing your clients use, with SSE streaming, tool definitions, interactive tool result responses, effort selection, and attributes. The call is attributed to `console.playground` and your e-mail.

**Judge.** Evaluate state (dialogue, agent memory, or system state JSON) against calibrated NOUL (probability of condition being true), CHOICE (classification across options with confidence), and SCORE (ordered levels) criteria via the Judge RPC.

## Security model

- The console holds the gateway's admin key **on the server**; it never reaches the browser, and vendor keys are
  write-only.
- Session cookies are signed (HMAC-SHA256), `HttpOnly`, `SameSite=Lax` and `Secure` over HTTPS. They carry
  only name, e-mail, sign-in method and expiry; the role is recomputed from the allowlists on every request,
  and turning a sign-in method off ends its sessions.
- Passwords are stored only as argon2id (or bcrypt) hashes, compared in constant time; unknown e-mails take
  as long as known ones, and failures back off per account and per client address.
- Proxy sign-in trusts an identity header only from `TRUSTED_PROXY_CIDRS`, and only as far as that proxy
  overwrites the header; Cloudflare Access tokens are verified cryptographically on every request.
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
make dev     # run on :8080 without sign-in (set KTMS_* and/or TOKENMETER_URL)
make proto   # regenerate internal/gen from proto/ (needs buf)
```

`internal/web/static` is plain HTML, CSS and ES modules served as-is: `js/main.js` (shell and routing),
`js/lib.js` (shared components), `js/pages/*` (one module per page), and `vendor/` (Preact 10 and htm 3,
unmodified except that hooks import `./preact.mjs`). Restart the server after editing them: they are embedded
at build time. `proto/` is a copy of KTMS's
protocol, so the console talks to it over gRPC without importing its code.

Layout: `cmd/ktms-admin` (entrypoint), `internal/auth` (sessions, allowlists, sign-in page; one package per sign-in method below it),
`internal/api` (JSON API and guards), `internal/usage` (light-tokenmeter client and the greedy/worthy
analysis), `internal/config`, `internal/web` (embedded UI).

### Releasing

Pushing a `vX.Y.Z` tag publishes a multi-arch image to Docker Hub as `coyl/ktms-admin:<version>`.
`latest` moves only with the newest stable release. The old name `coyl/llm-proxy-admin` gets no new
releases after 0.4.0. The
image is signed with keyless cosign and carries an SBOM and provenance (verify like the gateway's
images, see [KTMS SECURITY.md](https://github.com/ai-process/ktms/blob/main/SECURITY.md#verifying-a-release),
with `docker-publish.yml` of `ai-process/ktms-admin`). The repository needs the secrets
`DOCKER_USERNAME` and `DOCKER_PASSWORD`.

```bash
git tag v1.0.0 && git push origin v1.0.0
```

Contributions are welcome: see [CONTRIBUTING.md](CONTRIBUTING.md). Licensed under the [Apache License 2.0](LICENSE).
