# openapi-bundle

`openapi-bundle` turns a multi-file OpenAPI YAML document into one self-contained
YAML document for downstream generators and published packages.

## Usage

```sh
go run . --root openapi/root.yaml --output openapi/bundled.yaml
```

As a pinned Go tool in another module:

```sh
go tool openapi-bundle --root openapi/root.yaml --output openapi/bundled.yaml
```

The bundler:

- resolves relative local-file `$ref` references and RFC 6901 JSON pointers;
- preserves references to root re-exported components as
  `#/components/<category>/<name>` references, keeping generated types deduplicated;
- inlines other external references; and
- rejects missing targets, malformed pointers, reference cycles, remote URLs,
  and absolute file references.

It does not fetch network resources or lint OpenAPI semantics. JSON input is
accepted through the YAML parser, but output is YAML. Source files are never
modified.

## Development

```sh
make test
make lint
```

The test suite covers component deduplication, nested references, deterministic
output, malformed inputs, local-only reference handling, and cycle rejection.

## Releases

This is a standalone public repository, not an export from a private source repo.
CI runs lint, tests and a build for pushes to `main`, all pull requests to `main`
(including forks and Dependabot), and version tags.

From a clean `main` checkout, prepare an annotated release tag:

```sh
make release VERSION=patch DRY_RUN=1  # preview
make release VERSION=patch            # create tag locally
```

`VERSION` also accepts `minor`, `major`, or an explicit `X.Y.Z`. With no prior
release tags, `patch` starts at `v0.0.1`. Run `make release-list` to see local
tags (fetch tags first to include remote releases). Tests run by default;
`SKIP_TESTS=1` skips them. Push the tag and main using the printed command, or
set `AUTOPUSH=1` to push both atomically to `origin`. A dry run never pushes.

After the tag is pushed and CI checks pass, the workflow creates a GitHub Release
with generated release notes and GitHub's source archives.
Protect `main` and the `v*` tags so only maintainers can authorize releases.

Releases use the built-in `GITHUB_TOKEN`; no GitHub App, private dependencies,
`ENABLE_PUBLIC_PUBLISH` variable or release-environment secrets are needed.
There are no prebuilt binaries or separate package-publishing steps. Install a
released version directly with Go, for example after `v0.0.1` is published:

```sh
go install github.com/naughtbot/openapi-bundle@v0.0.1
```

## License

MIT. See [LICENSE](LICENSE).
