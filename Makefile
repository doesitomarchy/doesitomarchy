VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build test check validate clean

build:
	CGO_ENABLED=0 go build -trimpath -ldflags "$(LDFLAGS)" -o bin/doioma ./cmd/doioma

test:
	go test -race ./...

validate:
	go run ./cmd/doioma validate

# Everything CI runs, in the same order.
check:
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)
	go vet ./...
	$(MAKE) test
	$(MAKE) build
	./bin/doioma validate

clean:
	rm -rf bin
