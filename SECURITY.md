# Security policy

## Supported versions

| Version | Supported |
| ------- | --------- |
| `1.x`, latest minor release | :white_check_mark: |
| `0.x` | :x: upgrade to 1.0 |

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/ai-process/ktms-admin/security/advisories/new)
for this repository. You will get a first answer within a few days.

## Verifying a release

From 1.0.0 on, images are signed with keyless [Sigstore cosign](https://docs.sigstore.dev/) and carry an
SBOM and SLSA provenance. Use cosign 3 and pin the identity to the tag you verify:

```bash
V=v1.1.0
cosign verify coyl/ktms-admin:${V#v} \
  --certificate-identity "https://github.com/ai-process/ktms-admin/.github/workflows/docker-publish.yml@refs/tags/$V" \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com
docker buildx imagetools inspect coyl/ktms-admin:${V#v} --format '{{ json .SBOM }}'
```

## What this console protects

The console holds an **admin key for the KTMS gateway**, which can read and change routing and mint API
keys. Treat access to it accordingly:

- Only verified e-mails that match `ALLOWED_EMAILS` (admins), `VIEWER_EMAILS` (read-only) or
  `TEAM_MEMBERSHIPS` (one team's keys and spend) can sign in, and the lists are re-checked on every
  request. Sign-in is a password (argon2id or bcrypt hashes, with back-off per account and client
  address), OpenID Connect, GitHub, or an access proxy.
- An access proxy's e-mail header is trusted only from `TRUSTED_PROXY_CIDRS`; Cloudflare Access tokens
  are verified against the team's keys. Never expose the console directly when relying on a header.
- The admin key never reaches the browser; vendor API keys are write-only through the console.
- Mutating requests need a role allowed to make them, a custom `X-Requested-With` header and a
  same-origin `Origin`.
- Run it behind HTTPS (`PUBLIC_URL=https://…`); cookies are then `Secure`, `HttpOnly` and `SameSite=Lax`.
- `INSECURE_NO_AUTH` disables sign-in and is refused unless `PUBLIC_URL` is localhost.
