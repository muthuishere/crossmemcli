# Releasing

One git tag produces everything: the GitHub release (binaries + checksums), the
Homebrew cask, and the npm packages.

```sh
task release -- 0.2.2
```

That task refuses a dirty tree or an existing tag, then runs `task ci`, writes
`0.2.2` into every npm `package.json` (`scripts/set-version.sh` — the root
launcher, its `optionalDependencies` pins, and the five platform packages),
commits, tags `v0.2.2`, and pushes the branch and the tag.

The Go binary's version is not stored in a file: GoReleaser derives it from the
tag and injects it with `-ldflags -X …/internal/version.Version`.

## What the tag triggers

`.github/workflows/npm-publish.yml` runs on `v*` and does, in order:

1. `go test ./...` and `go vet ./...`
2. `goreleaser release --clean` — builds every platform, **publishes the GitHub
   release**, pushes the Homebrew cask, and copies each binary into
   `npm/platforms/<os>-<arch>/bin/` via the build post-hooks
3. `scripts/npm-publish-oidc.sh` — publishes the five platform packages first,
   `@muthuishere/crossmem` last, so the launcher never resolves a version of a
   platform package that does not exist yet

The cask push needs a `HOMEBREW_TAP_GITHUB_TOKEN` secret with write access to
`muthuishere/homebrew-tap`. Without it, `skip_upload` in `.goreleaser.yaml`
turns the push off and the cask is only generated under `dist/` — a local
`task snapshot` therefore needs no token.

## npm trusted publishing

Publishing uses GitHub Actions OIDC, not `NODE_AUTH_TOKEN`; npm exchanges the
OIDC token during `npm publish --provenance`. The trust record names the
workflow **file**, so `npm-publish.yml` must keep its name.

Each package must already exist on npm before it can be trusted:

```sh
for package in \
  @muthuishere/crossmem-darwin-arm64 \
  @muthuishere/crossmem-darwin-x64 \
  @muthuishere/crossmem-linux-arm64 \
  @muthuishere/crossmem-linux-x64 \
  @muthuishere/crossmem-windows-x64 \
  @muthuishere/crossmem
do
  npm trust github "$package" \
    --repo muthuishere/crossmemcli \
    --file npm-publish.yml \
    --allow-publish \
    --yes
  sleep 2
done
```

A republish of a version already on npm is skipped rather than failed, so a
re-run of the workflow is safe.
