# Contributing to Rex

Thanks for helping. Bug reports, fixes and small features are welcome; for anything large, open an issue first so we can agree on the approach.

## Setup

Follow [Develop](README.md#develop) in the README. Before opening a pull request, run:

```bash
go test ./...
pnpm test
pnpm build
```

## Pull requests

- Keep each pull request to one change, with a test for new logic.
- Title it as a [Conventional Commit](https://www.conventionalcommits.org) (`fix: …`, `feat: …`); releases are versioned from these, see [docs/RELEASING.md](docs/RELEASING.md).
- Don't commit secrets, signing material or local data. Report vulnerabilities privately, see [SECURITY.md](SECURITY.md).

## License

Contributions are accepted under the [Apache License 2.0](LICENSE), as set out in section 5 of the license. There is no separate contributor agreement.
