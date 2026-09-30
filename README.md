# DoesItOmarchy

The source of truth for which **Intel Macs (2006–2020)** run [Omarchy](https://omarchy.org) 4+, and which hardware components are still holding them back. Coverage is tracked per model identifier and per hardware configuration.

> **Status: early development.** The site is not live yet. The catalog covers every Intel Mac identifier; the web UI and search are being built. Test result submission opens with the API (a later phase).

## How it works

- **Catalog** (`data/`): one YAML file per Mac model identifier (e.g. `MacBookPro5,1`). Each file lists that identifier's releases and hardware configurations. Changes come in as pull requests.
- **Test results:** will be stored in the site database, not in git, once the submission API ships. Every capability starts ⚪ Untested; a status only changes when a real test result is accepted.
- **Site:** a single Go binary, `doioma`, that serves server-rendered HTML with HTMX from SQLite. The catalog is built into the binary and loaded into SQLite on start, so a deploy ships code and data together.

## Development

Requires Go (the version is in `go.mod`).

```sh
make check      # gofmt, vet, tests, build, catalog validation (same as CI)
make build      # → bin/doioma
./bin/doioma validate
./bin/doioma serve -data data   # http://127.0.0.1:8080, catalog read from data/ (restart to reload)
./bin/doioma sync               # create or update ./doioma.db without serving
```

`-db FILE` (or `$DOIOMA_DB`) picks the database; the default is `./doioma.db`. Without `-data`, `serve` and `sync` use the catalog built into the binary.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

- Code: [MIT](LICENSE)
- Catalog data in `data/`: [CC BY-SA 4.0](data/LICENSE)
