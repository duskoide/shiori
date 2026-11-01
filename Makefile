GO ?= go
export GOTOOLCHAIN ?= auto

.PHONY: build run test vet fmt check dev package clean

build:
	$(GO) build -o herdr-mygo .

run: build
	./herdr-mygo

test:
	$(GO) test ./...

vet:
	$(GO) vet ./...

fmt:
	gofmt -l -w .

# What CI runs.
check: vet test
	@test -z "$$(gofmt -l .)" || (echo "gofmt needed:"; gofmt -l .; exit 1)

dev:
	$(GO) tool mygo dev

package:
	$(GO) tool mygo build

clean:
	rm -rf build herdr-mygo
