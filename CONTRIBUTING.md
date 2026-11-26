# Contributing

Thanks for helping. The project is deliberately small: one Go binary, an embedded vanilla-JS
page, no front-end build step. Please keep it that way.

## Setup

```bash
make test      # go test -race ./...
make dev       # run on :8080 without Google sign-in (localhost only)
```

`make dev` needs `KTMS_ADDR` + `KTMS_ADMIN_KEY` and/or `TOKENMETER_URL` in the environment.
The UI lives in `internal/web/static` and is served as-is: edit, restart, refresh.

## Rules

- Comments stay short (three lines at most, ideally one) and explain *why*, not *what*.
- UI code builds DOM with `h()`; never assign API data to `innerHTML`.
- `ktms.v1` is [KTMS](https://github.com/ai-process/ktms)'s: the console imports
  `github.com/ai-process/ktms/gen/ktms/v1` at the version in `go.mod`, and a change to the
  protocol is made in KTMS first.
- Add tests for auth, roles and anything that changes state.
- Open a pull request against `main`; CI must be green.
