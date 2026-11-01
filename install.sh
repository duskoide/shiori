#!/bin/sh
# Shiori installer.
#
#   curl -fsSL https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | sh
#
# Downloads the release binary for this OS and CPU (falling back to a source
# build with Go when there is none), puts it in ~/.local/bin and, on Linux,
# adds a desktop entry. Run with --help for the options.
#
# While the repository is private, pass a GitHub token that can read it:
#   curl -fsSL -H "Authorization: Bearer $GITHUB_TOKEN" \
#     https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | GITHUB_TOKEN=$GITHUB_TOKEN sh
set -eu

REPO="${SHIORI_REPO:-duskoide/shiori}"
TOKEN="${GITHUB_TOKEN:-${GH_TOKEN:-}}"
VERSION="${SHIORI_VERSION:-latest}"
BIN_DIR="${SHIORI_INSTALL_DIR:-$HOME/.local/bin}"
FROM_SOURCE=0
DESKTOP=1
UNINSTALL=0

say() { printf '\033[1;35mshiori:\033[0m %s\n' "$*"; }
warn() { printf '\033[1;33mshiori:\033[0m %s\n' "$*" >&2; }
die() { printf '\033[1;31mshiori:\033[0m %s\n' "$*" >&2; exit 1; }
has() { command -v "$1" >/dev/null 2>&1; }

usage() {
	cat <<'USAGE'
Install Shiori.

  curl -fsSL https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | sh
  curl -fsSL https://raw.githubusercontent.com/duskoide/shiori/main/install.sh | sh -s -- [options]

Options:
  --version <v>     release to install, e.g. v0.1.0 (default: latest)
  --dir <path>      where the binary goes (default: ~/.local/bin)
  --from-source     build with Go instead of downloading a release
  --no-desktop      on Linux, skip the .desktop entry and icon
  --uninstall       remove what this script installed
  -h, --help        this help

Environment:
  SHIORI_VERSION, SHIORI_INSTALL_DIR   same as --version, --dir
  SHIORI_REPO                          owner/name (default duskoide/shiori)
  GITHUB_TOKEN (or GH_TOKEN)           token for a private repository
USAGE
}

while [ $# -gt 0 ]; do
	case "$1" in
	--version) [ $# -ge 2 ] || die "--version needs a value"; VERSION="$2"; shift 2 ;;
	--version=*) VERSION="${1#*=}"; shift ;;
	--dir) [ $# -ge 2 ] || die "--dir needs a value"; BIN_DIR="$2"; shift 2 ;;
	--dir=*) BIN_DIR="${1#*=}"; shift ;;
	--from-source) FROM_SOURCE=1; shift ;;
	--no-desktop) DESKTOP=0; shift ;;
	--uninstall) UNINSTALL=1; shift ;;
	-h | --help) usage; exit 0 ;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
done

# --- platform ---------------------------------------------------------------

case "$(uname -s)" in
Linux) OS=linux ;;
Darwin) OS=darwin ;;
*) die "unsupported OS: $(uname -s) (Linux and macOS are supported)" ;;
esac
case "$(uname -m)" in
x86_64 | amd64) ARCH=amd64 ;;
aarch64 | arm64) ARCH=arm64 ;;
*) die "unsupported architecture: $(uname -m) (amd64 and arm64 are supported)" ;;
esac

DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
DESKTOP_FILE="$DATA_HOME/applications/dev.duskoide.shiori.desktop"
ICON_FILE="$DATA_HOME/icons/hicolor/256x256/apps/dev.duskoide.shiori.png"

if [ "$UNINSTALL" = 1 ]; then
	rm -f "$BIN_DIR/shiori" "$DESKTOP_FILE" "$ICON_FILE"
	if has update-desktop-database; then update-desktop-database "$DATA_HOME/applications" 2>/dev/null || true; fi
	say "removed $BIN_DIR/shiori and the desktop entry (settings in ~/.config/shiori are kept)"
	exit 0
fi

# --- helpers ----------------------------------------------------------------

fetch() { # fetch <url> <out> [extra header]
	if has curl; then
		if [ $# -ge 3 ]; then
			curl -fsSL --retry 3 -H "$3" -o "$2" "$1"
		else
			curl -fsSL --retry 3 -o "$2" "$1"
		fi
	elif has wget; then
		if [ $# -ge 3 ]; then
			wget -q --header="$3" -O "$2" "$1"
		else
			wget -q -O "$2" "$1"
		fi
	else
		die "curl or wget is required"
	fi
}

# api <path> <out> [accept]: a GitHub API request with the token.
api() {
	accept="${3:-application/vnd.github+json}"
	if has curl; then
		curl -fsSL --retry 3 -H "Authorization: Bearer $TOKEN" -H "Accept: $accept" -o "$2" "https://api.github.com/$1"
	else
		wget -q --header="Authorization: Bearer $TOKEN" --header="Accept: $accept" -O "$2" "https://api.github.com/$1"
	fi
}

# get_release_file <name> <out>: one file of the chosen release.
get_release_file() {
	if [ -z "$TOKEN" ]; then
		fetch "$BASE/$1" "$2" 2>/dev/null
		return
	fi
	# Private repository: find the asset's API URL in the release JSON. The
	# JSON lists each asset's "url" before its "name".
	id="$(tr ',' '\n' <"$TMP/release.json" | awk -v want="$1" '
		/"url": *"https:\/\/api\.github\.com\/repos\/.*\/releases\/assets\// { u = $0; sub(/.*"url": *"/, "", u); sub(/".*/, "", u) }
		/"name":/ { n = $0; sub(/.*"name": *"/, "", n); sub(/".*/, "", n); if (n == want && u != "") { print u; exit } }')"
	[ -n "$id" ] || return 1
	api "${id#https://api.github.com/}" "$2" application/octet-stream 2>/dev/null
}

sha256() {
	if has sha256sum; then
		sha256sum "$1" | cut -d' ' -f1
	elif has shasum; then
		shasum -a 256 "$1" | cut -d' ' -f1
	fi
}

TMP="$(mktemp -d 2>/dev/null || mktemp -d -t shiori)"
trap 'rm -rf "$TMP"' EXIT
trap 'exit 130' INT TERM

# --- download a release -----------------------------------------------------

# download_release fetches and verifies the release archive into $TMP/x.
# It returns 1 only when there is no archive to download; other failures exit.
download_release() {
	asset="shiori_${OS}_${ARCH}.tar.gz"
	if [ "$VERSION" = latest ]; then
		BASE="https://github.com/$REPO/releases/latest/download"
		rel="repos/$REPO/releases/latest"
	else
		case "$VERSION" in v*) ;; *) VERSION="v$VERSION" ;; esac
		BASE="https://github.com/$REPO/releases/download/$VERSION"
		rel="repos/$REPO/releases/tags/$VERSION"
	fi
	if [ -n "$TOKEN" ]; then
		api "$rel" "$TMP/release.json" 2>/dev/null || return 1
	fi

	say "downloading $asset ($VERSION)"
	get_release_file "$asset" "$TMP/$asset" || return 1

	if get_release_file checksums.txt "$TMP/checksums.txt"; then
		want="$(grep " $asset\$" "$TMP/checksums.txt" | cut -d' ' -f1 || true)"
		got="$(sha256 "$TMP/$asset" || true)"
		if [ -n "$want" ] && [ -n "$got" ]; then
			[ "$want" = "$got" ] || die "checksum mismatch for $asset"
			say "checksum ok"
		fi
	fi

	mkdir -p "$TMP/x"
	tar -xzf "$TMP/$asset" -C "$TMP/x" || die "could not unpack $asset"
	[ -f "$TMP/x/shiori" ] || die "$asset has no shiori binary"
}

# --- build from source ------------------------------------------------------

build_source() {
	has go || die "no release found for $OS/$ARCH and Go is not installed.
Install Go (https://go.dev/dl) and run again, or pick a release with --version."
	has git || die "git is needed to build from source"
	ref="$VERSION"
	[ "$ref" = latest ] && ref=main
	say "building $REPO@$ref from source with $(go version | cut -d' ' -f3)"
	# git uses your credentials (or GITHUB_TOKEN), so a private repository works.
	if [ -n "$TOKEN" ]; then
		auth="$(printf 'x-access-token:%s' "$TOKEN" | base64 | tr -d '\n')"
		git -c "http.https://github.com/.extraheader=Authorization: Basic $auth" \
			clone -q --depth 1 --branch "$ref" "https://github.com/$REPO.git" "$TMP/src"
	else
		git clone -q --depth 1 --branch "$ref" "https://github.com/$REPO.git" "$TMP/src"
	fi
	version="${ref#v}"
	[ "$ref" = main ] && version="0.0.0+$(git -C "$TMP/src" rev-parse --short HEAD)"
	mkdir -p "$TMP/x"
	# MyGo needs no cgo; GOTOOLCHAIN=auto fetches the Go the module asks for.
	(cd "$TMP/src" && CGO_ENABLED=0 GOTOOLCHAIN="${GOTOOLCHAIN:-auto}" \
		go build -trimpath -ldflags "-s -w -X main.version=$version" -o "$TMP/x/shiori" ./cmd/shiori)
	cp "$TMP/src/assets/icon.png" "$TMP/x/icon.png"
}

# --- place files ------------------------------------------------------------

install_files() { # install_files <dir with shiori and icon.png>
	mkdir -p "$BIN_DIR"
	# Write next to the target and rename, so a running shiori is not clobbered.
	cp "$1/shiori" "$BIN_DIR/.shiori.new"
	chmod 755 "$BIN_DIR/.shiori.new"
	mv -f "$BIN_DIR/.shiori.new" "$BIN_DIR/shiori"
	if [ "$OS" = darwin ]; then xattr -d com.apple.quarantine "$BIN_DIR/shiori" 2>/dev/null || true; fi
	say "installed $BIN_DIR/shiori"

	[ "$OS" = linux ] && [ "$DESKTOP" = 1 ] || return 0
	mkdir -p "$(dirname "$DESKTOP_FILE")"
	if [ -f "$1/icon.png" ]; then
		mkdir -p "$(dirname "$ICON_FILE")"
		cp "$1/icon.png" "$ICON_FILE"
	fi
	cat >"$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=Shiori
GenericName=Terminal
Comment=Terminals and coding agents
Exec=$BIN_DIR/shiori
Icon=dev.duskoide.shiori
Terminal=false
Categories=System;TerminalEmulator;Development;
StartupWMClass=Shiori
EOF
	if has update-desktop-database; then update-desktop-database "$(dirname "$DESKTOP_FILE")" 2>/dev/null || true; fi
	say "added a desktop entry ($DESKTOP_FILE)"
}

# --- main -------------------------------------------------------------------

if [ "$FROM_SOURCE" = 1 ]; then
	build_source
elif ! download_release; then
	warn "no release asset for $OS/$ARCH at $VERSION; building from source instead"
	build_source
fi
install_files "$TMP/x"

"$BIN_DIR/shiori" --version 2>/dev/null || true

case ":$PATH:" in
*":$BIN_DIR:"*) ;;
*) warn "$BIN_DIR is not on PATH; add it, e.g.:  export PATH=\"$BIN_DIR:\$PATH\"" ;;
esac
has hc || warn "herdr-core (hc) is not on PATH; Shiori needs it to run terminals"
if [ "$OS" = linux ] && has ldconfig && ! ldconfig -p 2>/dev/null | grep -q 'libgtk-3'; then
	warn "GTK 3 was not found; install it (libgtk-3-0 / gtk3) for the window to open"
fi
say "done. Run: shiori"
