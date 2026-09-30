# Contributing to DoesItOmarchy

> This guide is an outline. Each section will be filled in once the catalog schema is finalized.

## Ways to help

1. **Fix or add catalog data:** correct a spec, add a missing configuration or model, or add a source.
2. **Submit a test result:** run the Omarchy test criteria on a real Mac. The submission process is still to come.
3. **Code:** improve the site itself.

## Catalog data (`data/`)

- One file per model identifier: `data/macs/<Identifier>.yaml`, using `-` in place of `,` (e.g. `MacBookPro5-1.yaml`).
- Shared hardware components live in `data/components/`.
- The test criteria live in `data/capabilities.yaml`.
- Every model needs at least one source (Apple Tech Specs, EveryMac, TheAppleWiki…).
- **Config IDs are permanent.** Test results refer to them, so a config ID is never reused or deleted. A renamed ID moves the old one to `aliases`.
- Run `make check` before opening a PR; CI runs the same checks.
- _Schema reference: to follow._

## Test results

Results are stored in the site database, not in this repo. _The submission process is still to come._

## Code

- Go standard library first; keep dependencies minimal.
- `make check` must pass.
- Keep pages working without JavaScript.

## License of contributions

- Code contributions are licensed under [MIT](LICENSE).
- Data contributions in `data/` are licensed under [CC BY-SA 4.0](data/LICENSE).
