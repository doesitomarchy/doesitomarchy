# Contributing to DoesItOmarchy

## Ways to help

1. **Fix or add catalog data:** correct a spec, add a missing configuration or model, or add a source.
2. **Submit a test result:** run the Omarchy test criteria on a real Mac. The submission process is still to come.
3. **Code:** improve the site itself.

## Catalog data (`data/`)

| File | What it holds |
|---|---|
| `vocabulary.yaml` | Every allowed product line, port class, feature, component kind and CPU codename |
| `capabilities.yaml` | The test criteria, each with a `when` rule saying which configurations it applies to |
| `components/<kind>.yaml` | Shared hardware components with their PCI/USB IDs and the Linux driver seen on real machines |
| `macs/<Identifier>.yaml` | One file per model identifier, using `-` in place of `,` (e.g. `Macmini3-1.yaml`), holding its releases and configurations |
| `config-ids.lock` | Every configuration ID ever issued |
| `aliases.yaml` | Search nicknames: phrases such as `mbp` or `trash can` and the query they stand for. `doiomad validate` checks each one parses and finds something |
| `changelog.yaml` | The public catalog changelog shown at `/changelog`, newest first. Add an entry for notable additions or corrections, especially where the catalog differs from Apple, EveryMac or OpenCore |
| `coverage.yaml` | Rules for configurations that are out of scope for the coverage metrics (e.g. released before 2009). They are still listed and testable; they just aren't counted. This is not the same as a Mac's `hard_blocker`, which marks it Not compatible |

Rules the validator enforces:

- **One configuration per distinct Linux-relevant hardware set.** Split a release into configurations only when components (GPU, Wi‑Fi, Ethernet chip…) or the applicable test criteria (ports, features) differ. CPU speed, RAM and disk size are attributes, not new configurations.
- **Configuration IDs are permanent.** They look like `<identifier-slug>-<release>-<letter>` (e.g. `macmini3-1-late-2009-a`). To rename one, move the old ID into `aliases`. Never delete or reuse an ID. After adding configurations, run `doiomad lock`.
- **Every model and component needs a source.** Apple Tech Specs and EveryMac are the sources for releases, order numbers, specs and ports.
- **Real hardware reports count as evidence.** Chips, hardware IDs and features observed on real machines (e.g. linuxhw probes) are recorded as known facts, even when Apple's pages don't list them. Flag them `uncertain` only when another source contradicts them.
- **Test results belong to a configuration**, never just to a model identifier, because one identifier can span several releases.
- **Don't guess.** When sources disagree or are silent, record your best value and add an `uncertain` entry (`field` + `note`) explaining why.
- **Unknown fields and values are errors.** Add new ports or features to `vocabulary.yaml` first.

Commands:

```sh
make check                                   # everything CI runs
./bin/doiomad validate                        # check the catalog
./bin/doiomad lock                            # record new configuration IDs
./bin/doiomad report -line mac-mini -o r.html # review page for one product line
```

## Test results

Results are stored in the site database, not in this repo. They use the DoesItOmarchy result schema, `doesitomarchy/result/v1`: one file per test run, for one configuration, with a status for each capability tested. [`internal/results/fixtures/mbp152-synthetic.yaml`](internal/results/fixtures/mbp152-synthetic.yaml) is a complete (made-up) example.

Every result starts pending, and a maintainer reviews it before it counts. For now maintainers import results by hand; the submission API for test tools such as OmacDiag comes next. Personal data (serial numbers, MAC and IP addresses, host and user names) is removed before anything is stored.

## Code

- Go standard library first; keep dependencies minimal.
- `make check` must pass.
- Keep pages working without JavaScript.

## License of contributions

- Code contributions are licensed under [MIT](LICENSE).
- Data contributions in `data/` are licensed under [CC BY-SA 4.0](data/LICENSE).
