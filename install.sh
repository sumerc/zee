#!/usr/bin/env bash
# Zee installer for macOS.
# Usage: curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash
#        VERSION=vX.Y.Z bash install.sh
set -euo pipefail

REPO="sumerc/zee"
APP_DIR="/Applications"
TMP="$(mktemp -d)"
MOUNT=""

err() { echo "error: $*" >&2; exit 1; }
log() { echo "==> $*"; }
cleanup() {
  [[ -n "$MOUNT" ]] && hdiutil detach "$MOUNT" -quiet >/dev/null 2>&1 || true
  rm -rf "$TMP"
}
run_or_sudo() {
  "$@" 2>/dev/null || { log "Need sudo: $*"; sudo "$@"; }
}
trap cleanup EXIT

[[ "$(uname -s)" == "Darwin" ]] || err "Zee currently supports macOS only."

VERSION="${VERSION:-}"
if [[ -z "$VERSION" ]]; then
  log "Resolving latest release..."
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
    | awk -F'"' '/"tag_name"/ {print $4; exit}')"
  [[ -n "$VERSION" ]] || err "could not resolve latest version (GitHub API rate limit?). Set VERSION=vX.Y.Z and retry."
fi
log "Installing Zee ${VERSION}"

DMG="Zee-${VERSION}.dmg"
BASE="https://github.com/${REPO}/releases/download/${VERSION}"

log "Downloading ${DMG}..."
curl -fL --progress-bar "${BASE}/${DMG}" -o "${TMP}/${DMG}" \
  || err "download failed: ${BASE}/${DMG}"

log "Verifying checksum..."
curl -fsSL "${BASE}/checksums.txt" -o "${TMP}/checksums.txt" \
  || err "download failed: ${BASE}/checksums.txt"
expected="$(awk -v f="${DMG}" '$2==f {print $1}' "${TMP}/checksums.txt")"
[[ -n "$expected" ]] || err "${DMG} not found in checksums.txt"
actual="$(shasum -a 256 "${TMP}/${DMG}" | awk '{print $1}')"
[[ "$expected" == "$actual" ]] || err "checksum mismatch (expected $expected, got $actual)"
log "Checksum OK"

log "Mounting DMG..."
MOUNT="$(hdiutil attach -nobrowse -readonly -mountrandom /tmp "${TMP}/${DMG}" \
  | grep -oE '/(private/tmp|Volumes)/[^[:space:]]+' \
  | tail -1)"
[[ -n "$MOUNT" && -d "$MOUNT/Zee.app" ]] || err "Zee.app not found in DMG"

if [[ -d "${APP_DIR}/Zee.app" ]]; then
  log "Removing existing ${APP_DIR}/Zee.app"
  run_or_sudo rm -rf "${APP_DIR}/Zee.app"
fi

log "Copying Zee.app to ${APP_DIR}..."
run_or_sudo cp -R "$MOUNT/Zee.app" "${APP_DIR}/"

log "Clearing quarantine attribute..."
run_or_sudo xattr -cr "${APP_DIR}/Zee.app"

cat <<EOF

Zee ${VERSION} installed to ${APP_DIR}/Zee.app

Next:
  1. Set an API key (at least one):
       launchctl setenv GROQ_API_KEY     your_key
       launchctl setenv OPENAI_API_KEY   your_key
       launchctl setenv DEEPGRAM_API_KEY your_key
       launchctl setenv MISTRAL_API_KEY  your_key
     (add to ~/.zshrc to persist across logins)

  2. Launch Zee from Spotlight or:
       open ${APP_DIR}/Zee.app

  3. macOS may prompt for Microphone and Accessibility.
     Grant both, then hold Ctrl+Shift+Space to record.
EOF
