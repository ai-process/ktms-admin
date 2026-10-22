# Changelog

Notable changes to ktms-admin. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[semantic versioning](https://semver.org); see [Compatibility](README.md#compatibility).

## [Unreleased]

### Added
- Any OpenID Connect provider by its generic settings (`OIDC_CLIENT_ID`, `OIDC_CLIENT_SECRET`, `OIDC_ISSUER`,
  `OIDC_NAME`), several at once through `OIDC_PROVIDERS`, and GitHub sign-in (`GITHUB_CLIENT_ID`,
  `GITHUB_CLIENT_SECRET`, `GITHUB_URL` for Enterprise Server).
- Password sign-in: `ADMIN_USERS` or `ADMIN_USERS_FILE` hold `email:hash` entries made by the new
  `ktms-admin hash-password` command (argon2id; bcrypt from `htpasswd -nB` works too). Failed attempts
  back off per account and per client address.
- Sign-in through an access proxy: an e-mail header from `TRUSTED_PROXY_CIDRS` (oauth2-proxy, Authelia,
  Authentik, Pomerium) or a verified Cloudflare Access token (`CF_ACCESS_TEAM_DOMAIN`, `CF_ACCESS_AUD`).
  Google sign-in is now optional when one of these is set.

### Changed
- Sign-in methods are pluggable adapters behind one core that owns sessions, roles and the
  sign-in page. Google sign-in works as before, with the same redirect URI.

### Deprecated
- `GOOGLE_CLIENT_ID` and `GOOGLE_CLIENT_SECRET`: use `OIDC_CLIENT_ID` and `OIDC_CLIENT_SECRET`. The old names
  keep working with a startup warning and are removed in 2.0.

### Fixed
- A profile picture the CSP blocks no longer shows as a broken image.
- Sign-in error pages are styled again: the CSP blocked their inline styles.

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
