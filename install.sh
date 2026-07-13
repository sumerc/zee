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
[[ "$(uname -m)" == "arm64" ]] || err "Zee requires Apple Silicon (arm64) — the local engine is arm64-only, and releases ship no Intel build."

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

# Offline models live under an immutable, app-version-independent release tag.
# The registry — filenames, hashes, and which models to pre-fetch — is generated
# from localmodel.go into localmodel/manifest.txt and read here from main, so
# nothing is hardcoded. Columns: filename<TAB>sha256<TAB>prefetch.
MODELS_TAG="models-v1"
MODELS_BASE="https://github.com/${REPO}/releases/download/${MODELS_TAG}"
MODELS_DIR="${HOME}/Library/Application Support/zee/models"
MANIFEST_URL="https://raw.githubusercontent.com/${REPO}/main/localmodel/manifest.txt"

# Pre-download the offline models before touching /Applications: they are part
# of the product promise (works offline, no key), not opt-in — so a failure
# aborts the install cleanly, before anything was changed. Re-runs are cheap:
# models already on disk pass the checksum and are skipped.
prefetch_models() {
  mkdir -p "$MODELS_DIR" || err "cannot create ${MODELS_DIR}"
  local manifest f sum dest
  manifest="$(curl -fsSL "$MANIFEST_URL")" \
    || err "model manifest unavailable: ${MANIFEST_URL}"
  # Each prefetch=true row: download the gguf from the release, verify its sha.
  while read -r f sum; do
    [[ -n "$f" ]] || continue
    dest="${MODELS_DIR}/${f}"
    if [[ -f "$dest" ]] && shasum -a 256 "$dest" | grep -q "$sum"; then
      log "Model ${f} already present"; continue
    fi
    log "Downloading model ${f}..."
    curl -fL --progress-bar "${MODELS_BASE}/${f}" -o "${dest}.part" \
      || { rm -f "${dest}.part"; err "model download failed: ${MODELS_BASE}/${f}"; }
    shasum -a 256 "${dest}.part" | grep -q "$sum" \
      || { rm -f "${dest}.part"; err "checksum mismatch for model ${f}"; }
    mv -f "${dest}.part" "$dest"
    log "Model ${f} OK"
  done < <(awk '!/^#/ && $3=="true" {print $1, $2}' <<<"$manifest")
}

log "Fetching offline models..."
prefetch_models

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
  # Quit a running instance before replacing the bundle (and so the post-install
  # wizard can register the hotkey / launch a fresh copy).
  osascript -e 'tell application "Zee" to quit' >/dev/null 2>&1 || true
  log "Removing existing ${APP_DIR}/Zee.app"
  run_or_sudo rm -rf "${APP_DIR}/Zee.app"
fi

log "Copying Zee.app to ${APP_DIR}..."
run_or_sudo cp -R "$MOUNT/Zee.app" "${APP_DIR}/"

log "Clearing quarantine attribute..."
run_or_sudo xattr -cr "${APP_DIR}/Zee.app"

log "Zee ${VERSION} installed to ${APP_DIR}/Zee.app"

# Hand off to the in-app setup wizard, which grants permissions (attributed to
# Zee.app because it runs as the bundle), stores any cloud API key, captures a
# hotkey, and verifies everything. Run it only with a real terminal — under
# `curl | bash` stdin is the script, so resolve the controlling tty explicitly
# and wire it as the app's stdio.
TTY=""
if [[ -e /dev/tty ]]; then
  TTY="$(tty < /dev/tty 2>/dev/null || true)"
  [[ "$TTY" == /dev/* ]] || TTY=""
fi

if [[ -n "$TTY" ]]; then
  log "Starting setup..."
  # `open -W` doesn't propagate the app's exit code; the wizard prints its own
  # pass/fail summary to this terminal, so only a failure to start is ours.
  open -W -a "${APP_DIR}/Zee.app" --stdin "$TTY" --stdout "$TTY" --stderr "$TTY" \
    --args -setup \
    || log "Setup could not start — run it later with: ${APP_DIR}/Zee.app/Contents/MacOS/zee -setup"
else
  cat <<EOF

Setup was not run (no interactive terminal detected).
Finish configuring Zee — provider + API key, permissions, and hotkey — with:

    /Applications/Zee.app/Contents/MacOS/zee -setup

On Apple Silicon, Zee also works offline out of the box (local model, no key).
EOF
fi
