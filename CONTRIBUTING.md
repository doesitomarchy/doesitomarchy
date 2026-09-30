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

Rules the validator enforces:

- **One configuration per distinct Linux-relevant hardware set.** Split a release into configurations only when components (GPU, Wi‑Fi, Ethernet chip…) or the applicable test criteria (ports, features) differ. CPU speed, RAM and disk size are attributes, not new configurations.
- **Configuration IDs are permanent.** They look like `<identifier-slug>-<release>-<letter>` (e.g. `macmini3-1-late-2009-a`). To rename one, move the old ID into `aliases`. Never delete or reuse an ID. After adding configurations, run `doioma lock`.
- **Every model and component needs a source.** Prefer Apple Tech Specs, then EveryMac, then hardware probe data (linuxhw).
- **Don't guess.** When sources disagree or are silent, record your best value and add an `uncertain` entry (`field` + `note`) explaining why.
- **Unknown fields and values are errors.** Add new ports or features to `vocabulary.yaml` first.

Commands:

```sh
make check                                   # everything CI runs
./bin/doioma validate                        # check the catalog
./bin/doioma lock                            # record new configuration IDs
./bin/doioma report -line mac-mini -o r.html # review page for one product line
```

## Test results

Results are stored in the site database, not in this repo. _The submission process is still to come._

## Code

- Go standard library first; keep dependencies minimal.
- `make check` must pass.
- Keep pages working without JavaScript.

## License of contributions

- Code contributions are licensed under [MIT](LICENSE).
- Data contributions in `data/` are licensed under [CC BY-SA 4.0](data/LICENSE).
