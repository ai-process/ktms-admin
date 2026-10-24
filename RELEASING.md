# Releasing ktms-admin

For maintainers. The console shares its major version with KTMS (see
[Compatibility](README.md#compatibility)): a fix is a patch, a feature is a minor, renaming or
dropping a setting waits for a new major.

## Steps

1. In a pull request (`chore(release): X.Y.Z`):
   - move the `[Unreleased]` entries of [`CHANGELOG.md`](CHANGELOG.md) under
     `## [X.Y.Z] - YYYY-MM-DD`;
   - when the release needs a newer KTMS or light-tokenmeter, move the pair in
     [`scripts/e2e.refs`](scripts/e2e.refs) to their release tags, copy KTMS's `proto/ktms/v1` at
     that tag over `proto/ktms/v1`, run `make proto`, and update the table in
     [Compatibility](README.md#compatibility);
   - set `V=` in [`SECURITY.md`](SECURITY.md) to the new tag.
2. After it is merged, tag main, push the tag, and publish the GitHub Release with the
   changelog section as its notes:

   ```bash
   git tag vX.Y.Z && git push origin vX.Y.Z
   gh release create vX.Y.Z --title "ktms-admin X.Y.Z" --notes-file notes.md --verify-tag
   ```

## What the tag publishes

[`docker-publish.yml`](.github/workflows/docker-publish.yml): the signed multi-arch image
`coyl/ktms-admin:X.Y.Z` with an SBOM and provenance. `X.Y` moves only for the newest release of
its line and `latest` only for the newest of all, so a backport moves neither. The old name
`coyl/llm-proxy-admin` gets no releases after 0.4.0.

## Secrets

`DOCKER_USERNAME` and `DOCKER_PASSWORD` (an access token) for Docker Hub. Signing needs no
secret: cosign signs keylessly with the workflow's OIDC identity, which
[`SECURITY.md`](SECURITY.md) pins.

## Checks

Every pull request runs `scripts/proto-check.sh` (the proto copy equals KTMS's at the ref in
`scripts/e2e.refs`) and `scripts/e2e.sh` against that KTMS and light-tokenmeter pair;
[`e2e-main.yml`](.github/workflows/e2e-main.yml) runs the e2e test daily against their `main`, so
a break shows up before the pair is moved.
