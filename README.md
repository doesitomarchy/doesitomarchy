# DoesItOmarchy

The source of truth for which **Intel Macs (2006–2020)** run [Omarchy](https://omarchy.org) 4+, and which hardware components are still holding them back. Coverage is tracked per model identifier and per hardware configuration.

> **Status: early development.** The site is not live yet. The catalog covers every Intel Mac identifier, with search and the web UI built; deployment comes next. Test result submission opens with the API (a later phase).

## How it works

- **Catalog** (`data/`): one YAML file per Mac model identifier (e.g. `MacBookPro5,1`). Each file lists that identifier's releases and hardware configurations. Changes come in as pull requests.
- **Test results:** will be stored in the site database, not in git, once the submission API ships. Every capability starts ⚪ Untested; a status only changes when a real test result is accepted.
- **Site:** a single Go binary, `doiomad` (the server; the name `doioma` is reserved for the future test client), that serves server-rendered HTML with HTMX from SQLite. The catalog is built into the binary and loaded into SQLite on start, so a deploy ships code and data together.

## Development

Requires Go (the version is in `go.mod`).

```sh
make check      # gofmt, vet, tests, build, catalog validation (same as CI)
make build      # → bin/doiomad
./bin/doiomad validate
./bin/doiomad serve -data data   # http://127.0.0.1:8080, catalog read from data/ (restart to reload)
./bin/doiomad sync               # create or update ./doesitomarchy.db without serving
./bin/doiomad serve -demo        # design review only: a few configs carry made-up results
make uicheck                     # accessibility and layout checks (needs Node and Chromium)
```

`-db FILE` (or `$DOIOMAD_DB`) picks the database; the default is `./doesitomarchy.db`. Without `-data`, `serve` and `sync` use the catalog built into the binary.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

- Code: [MIT](LICENSE)
- Catalog data in `data/`: [CC BY-SA 4.0](data/LICENSE)
