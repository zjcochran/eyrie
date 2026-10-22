# Development

## Building

Go 1.26 is all a developer's machine needs:

```sh
go build ./cmd/eyrie      # without the web app
go test -race ./...
```

The web app is built only by CI and the Dockerfile (Node.js is never
installed locally). To run Eyrie with it, build the image:

```sh
docker build -t eyrie .
```

or, without Docker, take the built app from the CI `web` job's artifact
(`internal/ui/dist/`, kept 7 days) and build the binary with it:

```sh
glab ci artifact <branch> web   # unpacks internal/ui/dist
go build -tags ui ./cmd/eyrie
```

## Layout

```
cmd/eyrie/            the binary
internal/<package>/   Go packages (see Architecture)
internal/store/migrations/{sqlite,postgres}/
web/                  the Svelte app (package.json, vite.config.ts, src/)
ci/                   pipeline scripts
test/coverage.sh      per-package coverage gate
test/smoke/           smoke test of the real image
docs/                 user and technical documentation
```

## Conventions

- Lint with `.golangci.yml`; exported names need doc comments, functions
  stay under a complexity of 20. Suppress a finding only with
  `//nolint:<linter> // reason`.
- Errors that a user sees name the setting or thing at fault.
- Time is injected (function fields or parameters), never read from the
  clock inside logic that tests need to control.
- Docs state exact behaviour and are changed with the code.

## Adding a migration

Add the next numbered file to **both** `internal/store/migrations/sqlite`
and `internal/store/migrations/postgres`, with the same name, producing the
same tables, columns and indexes. Use the shared types in the
[data model](data-model.md). Never edit a migration that has been merged.
`TestMigrationsInStep` and `TestSchemasMatch` check this.

## Adding an npm package

Don't, without asking the user first: the app keeps to five packages
(`svelte`, `vite`, `@sveltejs/vite-plugin-svelte`, `typescript`,
`svelte-check`), and small things are written here instead. If one is
agreed, it must come from a well-known project and be pinned exactly, and
the merge request says why it is needed and how many packages it brings in.
Change `web/package.json` only; CI makes the lock file:

1. Delete `web/package-lock.json` and push.
2. The `web-lock` job makes the new lock file (versions published at least
   14 days ago, no install scripts), prints how many packages it locks,
   saves it as its artifact `web-lock/package-lock.json`, and fails.
3. Commit that file and push again.

## Writing frontend tests

Tests are `web/src/**/*.test.ts`, run by Node.js's own test runner with
`node:test` and `node:assert/strict`. Node.js strips the types without
compiling, so imports name the `.ts` file (`./api.ts`), and only syntax that
can simply be erased is allowed (no `enum`, no parameter properties;
`erasableSyntaxOnly` in `tsconfig.json` enforces it). The types of the
runner's functions the tests use are in `web/src/node-test.d.ts`; add to it
when a test needs another one. Test plain TypeScript modules; Svelte
components are not run in tests.

## Releasing

Merge a merge request with one of the labels `major`, `minor` or `patch`;
see [Testing and CI](testing-and-ci.md#releases).
