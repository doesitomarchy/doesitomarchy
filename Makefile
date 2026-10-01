VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
CATALOG_DATE ?= $(shell git log -1 --format=%cs -- data 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION) -X main.catalogDate=$(CATALOG_DATE)

.PHONY: build test check validate clean

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
