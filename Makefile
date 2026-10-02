VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CATALOG_DATE ?= $(shell TZ=UTC git log -1 --date=format-local:%Y-%m-%d --format=%cd -- data 2>/dev/null || echo unknown)
CATALOG_COMMIT ?= $(shell git log -1 --format=%H -- data 2>/dev/null)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.catalogDate=$(CATALOG_DATE) -X main.catalogCommit=$(CATALOG_COMMIT)

.PHONY: build test check validate clean uicheck

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/doiomad ./cmd/doiomad

test:
	go test -race ./...
	go test -count=1 -run TestLatency ./internal/search   # p99 budget, without the race detector's overhead

validate:
	go run ./cmd/doiomad validate

# Everything CI runs, in the same order.
check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	$(MAKE) test
	$(MAKE) build
	./bin/doiomad validate

clean:
	rm -rf bin

# Accessibility and layout checks (needs Node and Chrome/Chromium); CI runs them in the ui job.
uicheck: build
	@./bin/doiomad serve -demo -admin-insecure -db "$${TMPDIR:-/tmp}/doiomad-uicheck.db" -addr 127.0.0.1:18999 & pid=$$!; \
	trap "kill $$pid" EXIT; sleep 1; \
	cd tools/uicheck && npm ci --no-audit --no-fund --silent && BASE=http://127.0.0.1:18999 CHROME=$${CHROME:-$$(command -v chromium || command -v google-chrome)} node check.mjs
