# Contributing to Gump

Thanks for your interest in contributing. Gump is in early alpha — contributions are welcome but the surface area is still moving fast.

## Getting Started

```
git clone https://github.com/isomorphx/gump.git
cd gump
go build .
go test ./...
```

Requires Go 1.22+ and at least one supported agent CLI for integration testing.

## Before You Contribute

- Open an issue first for anything non-trivial. This avoids wasted effort if the direction doesn't align.
- Small, focused PRs are easier to review than large ones.

## Development

Before submitting a PR, make sure everything passes:

```
go build .
go test ./...
go test ./internal/smoke/... -tags=smoke
```

- `go build .` must compile cleanly.
- `go test ./...` covers unit tests.
- Smoke tests (`-tags=smoke`) run the full cycle with agent stub: parsing → engine → state → ledger → report. They catch regressions that unit tests miss.
- Follow existing code style. No linter config wars.

## Pull Requests

1. Fork the repo and create a branch from `main`.
2. Make your changes.
3. Run all three checks above.
4. Open a PR with a clear description of what changed and why.

## Reporting Bugs

Open an issue with: what you ran, what you expected, what happened, and the output of `gump doctor`.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.