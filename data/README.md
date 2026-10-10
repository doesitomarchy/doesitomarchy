# Catalog data

The data in this directory is licensed under [CC BY-SA 4.0](LICENSE).

| Path | Contents |
|---|---|
| `capabilities.yaml` | The test criteria, with rules for which configurations each one applies to |
| `components/` | Shared hardware components (GPU, Wi‑Fi, audio codec, camera…) |
| `macs/` | One file per Mac model identifier, containing its releases and configurations |
| `sources/` | The test tools that submit reports, and how a tool's own format maps to the criteria |
| `fixes.yaml` | OmaBoot? fixes a report may list, each linked to its source |
| `plumbing.yaml` | Chipset PCI IDs that never decide a test criterion, folded away when reviewing shared IDs (generated; see CONTRIBUTING.md) |

Validate with `doiomad validate` (or `make check`).
