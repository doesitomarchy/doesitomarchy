# DoesItOmarchy

The source of truth for which **Intel Macs (2006–2020)** run [Omarchy](https://omarchy.org) 4+, and which hardware components are still holding them back. Coverage is tracked per model identifier and per hardware configuration.

> **Status: early development.** The site is not live yet, and the catalog is being researched.

## How it works

- **Catalog** (`data/`): one YAML file per Mac model identifier (e.g. `MacBookPro5,1`). Each file lists that identifier's releases and hardware configurations. Changes come in as pull requests.
- **Test results:** stored in the site database, not in git. Every capability starts ⚪ Untested; a status only changes when a real test result is submitted.
- **Site:** a single Go binary, `doioma`, that serves server-rendered HTML with HTMX from SQLite.

## Development

Requires Go (the version is in `go.mod`).

```sh
make check      # gofmt, vet, tests, build, catalog validation (same as CI)
make build      # → bin/doioma
./bin/doioma validate
```

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

- Code: [MIT](LICENSE)
- Catalog data in `data/`: [CC BY-SA 4.0](data/LICENSE)
