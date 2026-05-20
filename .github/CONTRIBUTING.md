# Contributing

Thanks for helping improve `iroha-cli`.

## Before you start

- Read the README and the existing `AGENTS.md` notes in the repository.
- Open an issue or discussion for larger changes before starting work.

## Local development

```bash
go test ./...
go build ./cmd/agent-cli
```

## Pull request checklist

- Keep changes focused.
- Run `go test ./...` before opening a PR.
- Include context for user-facing changes.
- Update documentation when behavior changes.

## Reporting bugs

Please include:

- the command you ran
- the expected result
- the actual result
- relevant logs or screenshots
