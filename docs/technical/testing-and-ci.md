# Testing and CI

## Rules

1. **Unit tests never reach real services or the network.** Outside
   services are replaced by in-process fakes (`httptest` servers, fake
   implementations of small interfaces, `testing/fstest` file systems);
   connection failures are simulated without dialling, never by dialling a
   closed port. SQLite in a temporary folder is used directly.
2. **PostgreSQL is tested in CI.** The store's tests run against SQLite
   always and against PostgreSQL when `EYRIE_TEST_POSTGRES_DSN` is set; CI
   sets it to a throwaway `postgres:17-alpine` service. Each test gets its
   own schema, dropped afterwards. Locally the PostgreSQL cases are skipped.
3. **Every Go package keeps at least 80% statement coverage from its own
   tests**, on SQLite alone too. `test/coverage.sh` enforces it per package.
4. **No issues:** zero golangci-lint issues (`.golangci.yml`), zero
   svelte-check errors or warnings, zero ShellCheck findings.
5. **No security finding** of any severity from SAST, secret detection,
   dependency scanning or container scanning, unless accepted with a reason
   (below).
6. **Every result reaches GitLab in a format it reads:** JUnit for tests,
   Cobertura for coverage, Code Quality for lint and scan findings.
7. **Node.js only in CI.** The web app's checks run in the `web` job, never
   on a developer's machine.

## Running the Go checks

```sh
gofmt -l $(git ls-files '*.go') && go vet ./... && golangci-lint run ./... && \
  go test -race -coverprofile=coverage.out ./... && test/coverage.sh coverage.out 80
```

`test/coverage.sh <profile> <minimum>` sums statements per package from a Go
cover profile (each block counted once), prints one line per package and
exits 1 if any is below the minimum.

## GitLab CI

`.gitlab-ci.yml`, stages `test`, `scan`, `release`, `publish`:

| Job | Stage | Image | What it does | Reports |
| --- | --- | --- | --- | --- |
| `test` | test | `golang:1.26` + `postgres:17-alpine` | gofmt check, `go vet`, `gotestsum` running `go test -race -coverprofile` against SQLite and PostgreSQL, Cobertura conversion, per-package 80% gate | `junit`, `coverage_report` (Cobertura), coverage regex |
| `code-quality` | test | `golang:1.26` | golangci-lint v2.14.0 | `codequality` |
| `web` | test | `node:24.22.1-alpine` | `ci/web.sh`, below; only when `web/package-lock.json` exists | `junit`, `coverage_report` (Cobertura), `codequality` (svelte-check) |
| `web-lock` | test | `node:24.22.1-alpine` | Only when `web/package-lock.json` is missing: `ci/web-lock.sh`, below | `web-lock/` (artifact) |
| `shellcheck` | test | `koalaman/shellcheck-alpine:v0.11.0` | ShellCheck on every tracked `*.sh`; fails on any finding | `codequality` |
| `leak-check` | test | `alpine:3` | `ci/leak-check.sh`: the files, and in a merge request its commits, title and description, must not name the internal GitLab server | |
| `semgrep-sast` | test | GitLab template | SAST, without the per-rule time limit; not allowed to fail | `sast` |
| `secret_detection` | test | GitLab template | Secret scanning; not allowed to fail | `secret_detection` |
| `dependency-scan` | test | `golang:1.26` | `ci/osv-report.sh`: osv-scanner (v2.6.0, built through the Go module proxy) on `go.mod` and `web/package-lock.json` | `gl-dependency-scanning-report.json` (artifact) |
| `build-image` | test | `docker:28` + dind | Builds the image as `eyrie.tar` | |
| `container_scanning` | scan | GitLab template | Scans `eyrie.tar`, including the Go modules in the binary | `container_scanning` |
| `security-gate` | scan | `alpine:3` | `ci/security-gate.sh` on the four scan reports | `codequality` |
| `smoke` | scan | `docker:28` + dind | After every test-stage job passed: `test/smoke/smoke.sh`, below | `junit` |
| `release-label` | test | `alpine:3` | Merge requests: exactly one label `major`, `minor` or `patch` | |
| `release` | release | `alpine:3` | After a merge: `ci/release.sh` | |
| `github-sync` | publish | `alpine:3` | After a merge: `ci/github-sync.sh`, below | |
| `github-image` | publish | `docker:28` + dind | After a release: the multi-arch image to ghcr.io, below | |
| `deps`, `deps-fix` | test | `golang:1.26` | Scheduled pipelines only: the weekly dependency check | `.deps/` (artifact) |

A merge request pipeline runs every job but `release`, `github-sync`,
`github-image`,
`deps` and `deps-fix`; a branch with an open merge request gets no branch pipeline.

### The web job

The app has five npm packages of its own (`svelte`, `vite`,
`@sveltejs/vite-plugin-svelte`, `typescript`, `svelte-check`); there is no
ESLint and no Vitest, to keep the dependencies few.

`ci/web.sh`, in `web/`:

1. `npm ci --ignore-scripts`: install exactly the lock file, running no
   package's install scripts (also set in `web/.npmrc`).
2. `npm audit signatures`: fails unless every package's registry signature,
   and provenance attestation where one is published, verifies.
3. `svelte-check --fail-on-warnings`: TypeScript in strict mode, with unused
   locals and parameters, missing returns and switch fallthrough as errors
   (`web/tsconfig.json`), plus Svelte's compiler and accessibility warnings.
   Also written as a Code Quality report.
4. `vite build`.
5. The tests (`src/**/*.test.ts`) with Node.js's own runner, which strips
   the TypeScript types: a JUnit report, and line coverage as LCOV, which
   `ci/lcov-cobertura.sh` converts to Cobertura and totals for the job's
   coverage figure. Test files are left out of the coverage. Frontend
   coverage is reported, not gated.

`ci/web-lock.sh` (job `web-lock`) runs instead when the lock file is
missing: `npm install --package-lock-only --ignore-scripts --before=<14
days ago>` makes it, the job saves it as the artifact
`web-lock/package-lock.json` and fails. Commit that file.

### Security findings

GitLab Free runs the scanners but neither shows their findings in a merge
request nor blocks a merge on them. `security-gate` does both: it prints
every finding of `gl-sast-report.json`, `gl-secret-detection-report.json`,
`gl-dependency-scanning-report.json` and `gl-container-scanning-report.json`,
fails on any of any severity, and writes the open ones to the Code Quality
widget. A missing or unreadable report fails it too.

GitLab's own Dependency Scanning needs Ultimate, so `ci/osv-report.sh`
writes the dependency report from osv-scanner's output: one finding per
vulnerability (its GO-, GHSA- and CVE- IDs grouped), with the severity from
its CVSS score (9+ Critical, 7+ High, 4+ Medium, above 0 Low, else Unknown).

To accept a finding: in our code, mark the line (`// nosemgrep` or
`//nolint:<linter>`, with the reason); for a dependency or image finding
that does not apply, add `<identifier> <package or file> <reason>` to
`ci/security-allowlist.txt`.

### Smoke test

`test/smoke/smoke.sh` builds the image and starts it twice
(`test/smoke/compose.yaml`): on SQLite, and on PostgreSQL 17. For each it
checks that `/healthz` answers `ok`, `/api/v1/info` reports the version
built in, the CSP header is set, an app route serves the embedded app and
its script is cached as immutable, the container runs as user 65532, and
setup creates the admin `alice` and signs her in, and a push monitor goes
up when its URL is called. Then it restarts both,
waits for them to answer, checks that `alice` can sign in with her
password, and waits up to 2 minutes for Docker to report both healthy. `SMOKE_HOST`, `SMOKE_BIND`,
`SMOKE_VERSION` and `SMOKE_JUNIT` adapt it to Docker-in-Docker.

## Releases

Every merge request has exactly one label, `major`, `minor` or `patch`
(semantic versioning). After a merge, `release` (`ci/release.sh`) finds the
merge request, bumps the latest `vX.Y.Z` tag accordingly (the first release
is v0.1.0), and creates the tag and a GitLab release with the merge
request's description and commits. It needs the CI/CD variable
`RELEASE_TOKEN`, a project access token with the `api` scope. It passes the
new tag to the publish jobs as `RELEASE_TAG` (a dotenv report); when nothing
was released, `RELEASE_TAG` is empty and `github-image` does nothing.

Then `github-sync` (`ci/github-sync.sh`) copies the default branch, the
`vX.Y.Z` tags and the releases to GitHub; nothing runs on GitHub. It pushes
a rewritten copy of the history, made with git-filter-repo in a scratch
repository:

- the GitLab project's URL, host and path become the GitHub repository's,
  in every file version, commit message and release note;
- every author and committer becomes `GHCR_USER` with GitHub's no-reply
  address, and `Claude-Session:` trailers are removed;
- `.gitlab-ci.yml`, `ci/` and `CLAUDE.md` are left out of every commit.

The rewrite is deterministic, so every sync is a fast-forward; it never
force-pushes, and a GitHub repository that has diverged fails the job.
Before the push, `ci/leak-check.sh history` checks every commit of the copy,
and each release note is checked before it is created. Releases GitHub
does not have yet are created oldest first. It needs the CI/CD variables
`GHCR_REPO` (`owner/name`), `GHCR_USER` and `GHCR_TOKEN` (a classic token
with the `repo` and `write:packages` scopes), and `RELEASE_TOKEN` to read
the GitLab releases.

`github-image` builds the image for `linux/amd64` and `linux/arm64` with
buildx (arm64 through QEMU, `tonistiigi/binfmt`) and pushes it to
`ghcr.io/<GHCR_REPO>` as `X.Y.Z`, `X.Y` and `latest`, with
`VERSION=vX.Y.Z` and `SOURCE_URL=https://github.com/<GHCR_REPO>` built in. The
index carries the `org.opencontainers.image.licenses` (`AGPL-3.0-or-later`) and
`org.opencontainers.image.source` annotations, the latter pointing at the GitHub
repository, which links the package to it; provenance and SBOM
attestations are left out, because GitHub lists each as an extra
`unknown/unknown` platform. `ci/leak-check.sh tree` runs first, since the
image is built from these files.

## Weekly dependency check

A pipeline schedule on the default branch runs only `deps` and `deps-fix`:

- `deps` (`ci/deps.sh`) reports Go vulnerabilities Eyrie's code reaches
  (govulncheck), npm vulnerabilities in the lock file (osv-scanner), and
  newer versions of direct Go modules, Go (patch releases; a new minor
  version only when the one in use is unsupported), Node.js (within its
  major version), the distroless base image and the npm packages. An npm
  version counts only once it has been published for 14 days; new major
  versions are listed but never taken. Vulnerabilities fail the job;
  newer versions exit 3, a warning.
- `deps-fix` (`ci/deps-fix.sh`) installs Node.js (`ci/install-node.sh`,
  checked against the release's SHA-256 list), has Claude Code make the
  updates (`ci/deps-fix-prompt.md`), runs every check, and opens a merge
  request labelled `patch` (a draft if the checks fail) with a table of the
  Go advisories fixed (`ci/deps-advisories.sh`). It needs the CI/CD
  variables `CLAUDE_CODE_OAUTH_TOKEN` and `DEPS_BOT_TOKEN`.
