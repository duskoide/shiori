GO ?= go
export GOTOOLCHAIN ?= auto
# MyGo needs no cgo; a pure-Go build loads the system GUI libraries at run time.
export CGO_ENABLED ?= 0
REV := $(shell git describe --always --dirty 2>/dev/null || echo unknown)
VERSION ?= 0.1.0+$(REV)
LDFLAGS := -s -w -X main.version=$(VERSION)
# The dev edition: a build of the working copy that runs beside the
# installed Shiori, with ~/.config/shiori-dev, the herdr-core session
# shiori-dev, the name Shiori Dev, and no updates.
DEV_LDFLAGS := -s -w -X main.version=0.1.0-dev+$(REV) -X main.edition=dev

.PHONY: build dev-build run dev-install dev-uninstall test vet fmt check dev package clean logo

build:
	$(GO) build -trimpath -ldflags '$(LDFLAGS)' -o shiori ./cmd/shiori

dev-build:
	$(GO) build -trimpath -ldflags '$(DEV_LDFLAGS)' -o shiori-dev ./cmd/shiori

# The dev edition, so it opens beside an installed Shiori.
run: dev-build
	./shiori-dev

# ~/.local/bin/shiori-dev, and a "Shiori Dev" launcher on Linux; again for
# newer changes.
dev-install: dev-build
	tools/dev-install.sh

dev-uninstall:
	tools/dev-install.sh --uninstall

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
	rm -rf build shiori shiori-dev
