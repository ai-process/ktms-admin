# Changelog

Notable changes to ktms-admin. The format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/) and versions follow
[semantic versioning](https://semver.org); see [Compatibility](README.md#compatibility).

## [Unreleased]

## [1.0.0] - 2026-10-22

The first stable release, paired with Keystone. The Model Switchboard. (KTMS) 1.0: ktms-admin
1.x works with every KTMS 1.x release (see [Compatibility](README.md#compatibility)).

### Changed
- The image is published only as `coyl/ktms-admin`. `coyl/llm-proxy-admin` stays at 0.4.0: switch
  your deployments to the new name.
- The binary and Go module are named `ktms-admin`; the console's internal CSRF header value
  changed with them (the server and its UI ship together).
- The README names the gateway Keystone. The Model Switchboard. (KTMS).

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
