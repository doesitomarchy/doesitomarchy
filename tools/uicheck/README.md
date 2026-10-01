# uicheck

Accessibility and layout checks for the DoesItOmarchy web UI (PLAN.md §17.5), run in CI by the `ui` job.

- **axe-core** WCAG 2.1 A/AA on the key pages at 360 px and 1440 px
- **colour contrast** on two pages in every theme (the default pair and all Omarchy themes)
- **layout**: no horizontal page scroll; slanted matrix header links stay clickable

It runs against a live server in `-demo` mode, so every verdict state is on the page.

```sh
make build && ./bin/doiomad serve -demo -addr 127.0.0.1:8080 &
cd tools/uicheck && npm ci && BASE=http://127.0.0.1:8080 CHROME=/usr/bin/chromium node check.mjs
```

Or `make uicheck`, which does both.
