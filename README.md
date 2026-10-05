# DoesItOmarchy

The source of truth for which **Intel Macs (2006–2020)** run [Omarchy](https://omarchy.org) 4+, and which hardware components are still holding them back. Coverage is tracked per model identifier and per hardware configuration.

> **Status:** live at [doesitomarchy.com](https://doesitomarchy.com). The catalog covers every Intel Mac identifier. Test tools can submit diagnostic reports through the [open API](https://doesitomarchy.com/api); no real test results are in yet.

## How it works

- **Catalog** (`data/`): one YAML file per Mac model identifier (e.g. `MacBookPro5,1`). Each file lists that identifier's releases and hardware configurations. Changes come in as pull requests.
- **Test results:** stored in the site database, not in git. Every capability starts ⚪ Untested; a status only changes when a maintainer accepts a test result. Each test run is a **diagnostic report** in the open DoesItOmarchy schema (`doesitomarchy/report/v1`); test tools submit them through the API (`POST /api/v1/reports`, with a source key), or maintainers import them, and maintainers review each one in `/admin` or the CLI.
- **Open data:** the catalog and results are readable as JSON at `/api/v1` (documented at [/api](https://doesitomarchy.com/api), described for tools and AI agents at `/api/v1/openapi.json` and `/llms.txt`). [Identify my Mac](https://doesitomarchy.com/identify) identifies a Mac and its configuration from one command's output.
- **Site:** a single Go binary, `doiomad` (the server; the name `doioma` is reserved for the future test client), that serves server-rendered HTML with HTMX from SQLite. The catalog is built into the binary and loaded into SQLite on start, so a deploy ships code and data together.

## Development

Requires Go (the version is in `go.mod`).

```sh
make check      # gofmt, vet, tests, build, catalog validation (same as CI)
make build      # → bin/doiomad
./bin/doiomad validate
./bin/doiomad serve -data data   # http://127.0.0.1:8080, catalog read from data/ (restart to reload)
./bin/doiomad sync               # create or update ./doesitomarchy.db without serving
./bin/doiomad serve -demo        # design review: a throwaway database with made-up results for every verdict
./bin/doiomad reports help       # moderate diagnostic reports: import, list, show, accept, reject, retract
./bin/doiomad sources help       # register test tools and their API keys
./bin/doiomad serve -demo -admin-insecure -addr 127.0.0.1:8081   # with /admin open locally, no sign-in
make uicheck                     # accessibility and layout checks (needs Node and Chromium)
```

`-db FILE` (or `$DOIOMAD_DB`) picks the database; the default is `./doesitomarchy.db`. Without `-data`, `serve` and `sync` use the catalog built into the binary.

## Contributing

See [CONTRIBUTING.md](CONTRIBUTING.md).

## License

- Code: [MIT](LICENSE)
- Catalog data in `data/`: [CC BY-SA 4.0](data/LICENSE)
