# Contributing to mockllm

## Development

```bash
make build
make test
make lint
```

## Adding a Provider

1. Create `providers/<name>/<name>.json` following the [spec format](README.md#provider-spec-format)
2. Run `make test` to verify it loads correctly
3. Submit a PR

## Auto-generating from models.dev

```bash
make generate                    # overwrites ./providers from models.dev
make generate-all                # generates all 118+ providers to ./providers-all
```

## Running Tests

```bash
make test                        # unit tests with race detector
go test -coverprofile=coverage.out ./... && go tool cover -func=coverage.out
```

## Pull Requests

- Keep changes focused
- Add tests for new functionality
- Run `make test` and `make lint` before pushing
- One PR per feature/fix
