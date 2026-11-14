#!/usr/bin/env bash
# install wvf SwiftBar plugin — symlink into the plugin dir and verify.
set -e
SRC="$(cd "$(dirname "$0")" && pwd)/wvf.30s.sh"
DEST="${SWIFTBAR_PLUGINS:-$HOME/Library/Application Support/SwiftBar/Plugins}"
mkdir -p "$DEST"
ln -sf "$SRC" "$DEST/wvf.30s.sh"
[[ -L "$DEST/wvf.30s.sh" ]] && [[ -x "$(readlink "$DEST/wvf.30s.sh")" ]] || {
  echo "install FAILED: link not executable"; exit 1; }
echo "installed: $DEST/wvf.30s.sh -> $SRC"
