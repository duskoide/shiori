GO ?= go
export GOTOOLCHAIN ?= auto
# MyGo needs no cgo; a pure-Go build loads the system GUI libraries at run time.
export CGO_ENABLED ?= 0
VERSION ?= 0.1.0+$(shell git describe --always --dirty 2>/dev/null || echo unknown)
LDFLAGS := -s -w -X main.version=$(VERSION)

.PHONY: build run test vet fmt check dev package clean logo

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o shiori ./cmd/shiori

run: build
	./shiori

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

# Redraw assets/{logo.svg,icon.png,mark.svg} from Noto Sans CJK JP.
logo:
	uv run --with fonttools --with resvg-py tools/logo/make_logo.py

clean:
	rm -rf build shiori
