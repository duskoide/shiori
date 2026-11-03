#!/bin/sh
# Installs the dev edition of Shiori (a build of this working copy, made
# by make dev-build as ./shiori-dev) beside the installed release:
# $PREFIX/bin/shiori-dev, and on Linux a "Shiori Dev" launcher. It keeps
# its files in ~/.config/shiori-dev and its terminals in the herdr-core
# session shiori-dev, and uses the release's bundled herdr-core
# ($PREFIX/libexec/shiori), else hc on PATH.
#
#   tools/dev-install.sh             install (again, for newer changes)
#   tools/dev-install.sh --uninstall remove what it installed
set -eu

PREFIX="${PREFIX:-$HOME/.local}"
DATA_HOME="${XDG_DATA_HOME:-$HOME/.local/share}"
BIN="$PREFIX/bin/shiori-dev"
DESKTOP_FILE="$DATA_HOME/applications/dev.duskoide.shiori-dev.desktop"
ICON_FILE="$DATA_HOME/icons/hicolor/256x256/apps/dev.duskoide.shiori-dev.png"
cd "$(dirname "$0")/.."

refresh_menu() {
	if command -v update-desktop-database >/dev/null 2>&1; then
		update-desktop-database "$(dirname "$DESKTOP_FILE")" 2>/dev/null || true
	fi
}

if [ "${1:-}" = --uninstall ]; then
	for f in "$BIN" "$DESKTOP_FILE" "$ICON_FILE"; do
		[ -e "$f" ] && unlink "$f"
	done
	refresh_menu
	echo "removed $BIN and its launcher; ~/.config/shiori-dev is kept"
	exit 0
fi

[ -x ./shiori-dev ] || { echo "no ./shiori-dev: run make dev-build first" >&2; exit 1; }
mkdir -p "$(dirname "$BIN")"
# Replaced, not written over, so a running shiori-dev keeps its binary.
cp ./shiori-dev "$BIN.new"
chmod 755 "$BIN.new"
mv -f "$BIN.new" "$BIN"
echo "installed $BIN ($("$BIN" --version))"

[ "$(uname -s)" = Linux ] || exit 0
mkdir -p "$(dirname "$DESKTOP_FILE")" "$(dirname "$ICON_FILE")"
cp assets/icon.png "$ICON_FILE"
cat >"$DESKTOP_FILE" <<EOF
[Desktop Entry]
Type=Application
Name=Shiori Dev
GenericName=Terminal
Comment=Shiori built from the working copy
Exec=$BIN
Icon=dev.duskoide.shiori-dev
Terminal=false
Categories=Development;
EOF
refresh_menu
echo "added a launcher ($DESKTOP_FILE)"
