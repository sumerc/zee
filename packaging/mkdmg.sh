#!/bin/bash
set -euo pipefail

# Create a macOS .dmg containing Zee.app with an Applications symlink.
# Usage: packaging/mkdmg.sh <binary> <version> <output.dmg>

BINARY="${1:?Usage: mkdmg.sh <binary> <version> <output.dmg>}"
VERSION="${2:?}"
OUTPUT="${3:?}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

STAGING=$(mktemp -d)
trap 'rm -rf "$STAGING"' EXIT
APP="$STAGING/Zee.app"

mkdir -p "$APP/Contents/MacOS" "$APP/Contents/Resources"

cp "$BINARY" "$APP/Contents/MacOS/zee"
chmod +x "$APP/Contents/MacOS/zee"

sed "s/__VERSION__/$VERSION/g" "$SCRIPT_DIR/Info.plist" > "$APP/Contents/Info.plist"

if [ -f "$SCRIPT_DIR/Zee.icns" ]; then
	cp "$SCRIPT_DIR/Zee.icns" "$APP/Contents/Resources/Zee.icns"
else
	echo "warning: $SCRIPT_DIR/Zee.icns not found, DMG will have no app icon" >&2
fi

ln -s /Applications "$STAGING/Applications"

hdiutil create -volname "Zee $VERSION" \
	-srcfolder "$STAGING" \
	-ov -format UDZO \
	"$OUTPUT"

echo "Created $OUTPUT"
