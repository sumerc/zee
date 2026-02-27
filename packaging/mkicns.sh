#!/bin/bash
set -euo pipefail

# Generate Zee.icns from a source PNG using sips + iconutil.
# Usage: packaging/mkicns.sh <source.png>
# Output: packaging/Zee.icns

SRC="${1:?Usage: mkicns.sh <source.png>}"
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
ICONSET="$WORK/Zee.iconset"
OUT="$(dirname "$0")/Zee.icns"

mkdir -p "$ICONSET"

for sz in 16 32 128 256 512; do
	sips -z $sz $sz "$SRC" --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null
	dbl=$((sz * 2))
	sips -z $dbl $dbl "$SRC" --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null
done

iconutil -c icns -o "$OUT" "$ICONSET"
echo "Created $OUT"
