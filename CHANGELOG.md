# Changelog

Notable changes to ktms-admin. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[semantic versioning](https://semver.org); see [Compatibility](README.md#compatibility).

## [Unreleased]

### Added
- MCP servers page for KTMS's MCP gateway (KTMS 1.4 or later): register servers, set or remove
  their credential (write-only), list their tools. Keys get the `mcp` access and an *Allowed MCP*
  list in their policy, and teams an *Allowed MCP* list. The `ktms.v1` copy follows KTMS 1.4.0.

### Changed
- Tested against KTMS 1.4.0 and light-tokenmeter 1.2.0 (`scripts/e2e.refs`).

## [1.1.2] - 2026-10-27

### Changed
- Tested against KTMS 1.3.6 and light-tokenmeter 1.1.0 (`scripts/e2e.refs`); the `ktms.v1` copy
  follows KTMS 1.3.6, which adds `NodeRegistryService.PullNodeUsage` (unused by the console).

## [1.1.1] - 2026-10-26

### Security
- The image is built on Alpine 3.23 with `ca-certificates` 20261020 or newer, which no longer
  trusts the eMudhra intermediates whose keys leaked in October 2026, and with Go 1.27.3.

## [1.1.0] - 2026-10-24

### Added
- `KTMS_READ_KEY`: an optional Keystone key with the `admin:read` scope (Keystone 1.2+). Admin API
  reads and every call of a viewer go with it; changes keep `KTMS_ADMIN_KEY`. The sidebar's
  health shows whether each key is accepted for its job: the admin key may change Keystone, the
  read key may read and nothing more.

### Changed
- Tested against KTMS 1.2.0 and light-tokenmeter 1.1.0 (`scripts/e2e.refs`); the `ktms.v1` copy
  follows KTMS 1.2.0. Every KTMS 1.x release still works.

## [1.0.0] - 2026-10-23

The first stable release, paired with Keystone. The Model Switchboard. (KTMS) 1.0: ktms-admin
1.x works with every KTMS 1.x release (see [Compatibility](README.md#compatibility)).

### Added
- `TOKENMETER_TOKEN`: a light-tokenmeter 1.0 bearer token with the `read` scope, for its
  authentication. The usage status in the sidebar says "refused" and names the setting when the token
  is missing, rejected or lacks the scope; `examples/compose.yaml` wires tokens through the whole stack.
- Password sign-in: `ADMIN_USERS` or `ADMIN_USERS_FILE` hold `email:hash` entries made by the new
  `ktms-admin hash-password` command (argon2id; bcrypt from `htpasswd -nB` works too). Failed attempts
  back off per account and per client address.
- Any OpenID Connect provider by its generic settings (`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ISSUER`,
  `OIDC_NAME`), several at once through `OIDC_PROVIDERS`, and GitHub sign-in (`GITHUB_CLIENT_ID`,
  `GITHUB_CLIENT_SECRET`, `GITHUB_URL` for Enterprise Server).
- Sign-in through an access proxy: an e-mail header from `TRUSTED_PROXY_CIDRS` (oauth2-proxy, Authelia,
  Authentik, Pomerium, Tailscale) or a verified Cloudflare Access token (`CF_ACCESS_TEAM_DOMAIN`,
  `CF_ACCESS_AUD`).
- Key policies on the Keys page: allowed models and requests and tokens per minute, set when minting
  or later (team leads for their team's keys), and each key's spend against its caps.
- Prompt-cache prices (cached input, cache write) and the `prompt_cache` capability in the model
  editor.

### Changed
- Google sign-in is optional: configure any of the methods above, alone or together. Sign-in methods
  are pluggable adapters behind one core that owns sessions, roles and the sign-in page; Google works as
  before, with the same redirect URI.
- The console and sign-in page use the ai-process.dev palette and logo. The dark theme runs orange to
  yellow on warm near-black, and the light theme has orange accents. Warnings are yellow and errors red,
  so they no longer look like the accent; the third chart and rule colour is blue.
- The image is published only as `coyl/ktms-admin`. `coyl/llm-proxy-admin` stays at 0.4.0: switch
  your deployments to the new name.
- The binary and Go module are named `ktms-admin`; the console's internal CSRF header value
  changed with them (the server and its UI ship together).
- The README names the gateway Keystone. The Model Switchboard. (KTMS).
- The license is Apache 2.0, with a `NOTICE` file.

### Deprecated
- `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`: use `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET`. The old names
  keep working with a startup warning and are removed in 2.0.

### Fixed
- Sign-in error pages are styled again: the CSP blocked their inline styles.
- A profile picture the CSP blocks no longer shows as a broken image.

## [0.4.0] - 2026-10-22

Needs KTMS 0.14.0 or later for the tenant and team pages.

### Added
- Redesigned console (Preact and htm, no build step): visual rules builder, key wizard with
  spend caps, usage with team budgets, light and dark themes.
- Organizations and teams, a team workspace, and team-scoped access for team leads and members.
- Spend limits for keys and users.
- Images are signed with keyless cosign and carry an SBOM and provenance.

### Changed
- Settings use the KTMS names: `KTMS_ADDR`, `KTMS_ADMIN_KEY`, `KTMS_TLS`.
- The image is published as `coyl/ktms-admin`, and still as `coyl/llm-proxy-admin`. Images are
  built only for `v*` tags; `latest` follows only the newest stable release.

### Deprecated
- `LLM_PROXY_ADDR`, `LLM_PROXY_ADMIN_KEY`, `LLM_PROXY_TLS`: they keep working with a startup
  warning and are removed in 2.0.

Earlier releases (0.1.0 to 0.3.0) are described on the
[GitHub releases page](https://github.com/ai-process/ktms-admin/releases).
