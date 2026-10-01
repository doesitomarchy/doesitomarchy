VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

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
