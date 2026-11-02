# Contributing to Opskest

Thank you for helping. Bug reports, ideas, docs fixes and pull requests are all welcome.

## Before you start

- **Bugs and ideas:** open an issue using the templates. For a larger change, open an issue or discussion first so we can agree on the approach before you spend time on it.
- **Questions:** use Discussions rather than issues.
- **Security problems:** report privately, see [SECURITY.md](SECURITY.md).
- Be kind. See the [code of conduct](CODE_OF_CONDUCT.md).

## Set up

You need Go (the version in `go.mod`) and Node 24.

```bash
cd web && npm ci && npm run build && cd ..   # builds the UI that Go embeds
go build -o bin/opskest ./cmd/opskest
./bin/opskest serve                           # http://localhost:8080
```

Run the checks before you push:

```bash
go vet ./...
go test ./...
cd web && npm run build && npm run test:theme
```

The browser end-to-end suite needs Chrome or Edge and takes a while: `cd web && npm run test:e2e` (one file: `node --experimental-strip-types --test ../tests/web/e2e/<file>.test.mjs`). Run it when you change something a user can see.

## Rules the code follows

These are easy to break and are checked in review. [Docs.md](../Docs.md) explains the product; the short version:

- **One writer to SQLite.** Everything that writes goes through the single writer or a store method that already serialises. Do not add a second write path.
- **Never log secrets.** No passwords, tokens, webhook URLs, request bodies, monitor settings or headers in logs or failure reasons. Use the logging helper that logs only the error string.
- **Public status pages never expose targets, failure reasons, runbooks or tags.** New public fields go through the sanitising layer, and the leak tests must cover them.
- **404, never 403,** for anything private or unknown on public-facing routes.
- **Bounded lists.** List endpoints keep the default and maximum limits; no infinite scrolling in the UI.
- **Namespacing.** Cookies, storage keys, headers, the compose project and container all carry the `opskest` prefix, and the stack binds only `127.0.0.1:8085`.
- **Third-party provider feeds are alert-only:** never outages, uptime, incidents or status-page components.
- **Incidents are created by people; outages are automatic.**
- **No new dependencies without discussion.** The Go and npm dependency lists are deliberately short. A pull request that adds one needs an issue that explains why first.
- **UI:** reuse the shared components, keep spacing and sizing uniform, use sentence-case labels, and support light and dark themes and phone widths.

## Pull requests

1. Fork, branch from `main`, keep the change focused.
2. Add or update tests, and update `Docs.md` or `README.md` if behaviour changes.
3. **Sign off every commit** (`git commit -s`). That adds a `Signed-off-by:` line and confirms you wrote the change or have the right to submit it under the Apache-2.0 licence (the [Developer Certificate of Origin](https://developercertificate.org/)).
4. Fill in the pull request template. CI runs the tests, the web build and the security scans; it must pass.

By contributing you agree that your contribution is licensed under the Apache License 2.0, the same as the project.

## Maintainers: cutting a release

Releases are built and signed by GitHub Actions, so nobody handles the signing key by hand.

One-time setup:

1. Create a key pair once, on a trusted machine: `go run ./cmd/releasetool keygen`. It writes `release.key` (private) and `release.pub` (public).
2. In the repository settings, add the **secret** `RELEASE_SIGNING_KEY` with the contents of `release.key`, and the **variable** `RELEASE_PUBLIC_KEY` with the contents of `release.pub`. Then move `release.key` out of the repository, for example into a password manager; it is git-ignored and must never be committed.
3. Copy `release.pub` to `deploy/release.pub` and commit it (it is the public key, so it is safe to publish). Source builds of a release tag embed it so they can verify signed updates.
4. Turn on two-factor authentication for every maintainer, and protect the `v*` tags and the `main` branch.

Each release: update `CHANGELOG.md`, then tag and push (`git tag v1.2.0 && git push origin v1.2.0`). The workflow builds all platforms, signs the manifest, attaches everything to a GitHub release and records build provenance. Installations check `releases/latest/download/manifest.json` and verify the signature with the public key built into the binary.
