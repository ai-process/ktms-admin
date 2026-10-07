# Security policy

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's
[private vulnerability reporting](https://github.com/ai-process/llm-proxy-admin/security/advisories/new)
for this repository. You will get a first answer within a few days.

## What this console protects

The console holds an **admin key for llm-proxy**, which can read and change routing and mint API
keys. Treat access to it accordingly:

- Only people matching `ALLOWED_EMAILS` (full access) or `VIEWER_EMAILS` (read-only) can sign in, and
  only with a Google-verified address. The lists are re-checked on every request.
- The admin key never reaches the browser; vendor API keys are write-only through the console.
- Mutating requests need the admin role, a custom `X-Requested-With` header and a same-origin `Origin`.
- Run it behind HTTPS (`PUBLIC_URL=https://…`); cookies are then `Secure`, `HttpOnly` and `SameSite=Lax`.
- `INSECURE_NO_AUTH` disables sign-in and is refused unless `PUBLIC_URL` is localhost.
