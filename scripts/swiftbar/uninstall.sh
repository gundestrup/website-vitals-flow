#!/usr/bin/env bash
set -e
DEST="${SWIFTBAR_PLUGINS:-$HOME/Library/Application Support/SwiftBar/Plugins}"
rm -f "$DEST/wvf.30s.sh"
[ ! -e "$DEST/wvf.30s.sh" ] || { echo "uninstall FAILED"; exit 1; }
echo "uninstalled: $DEST/wvf.30s.sh"
