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

# Offline models live under an immutable, app-version-independent tag.
MODELS_TAG="models-v1"
MODELS_BASE="https://github.com/${REPO}/releases/download/${MODELS_TAG}"
MODELS_DIR="${HOME}/Library/Application Support/zee/models"
# The default 110M + the multilingual v3 (v2 is opt-in). SHA256s are not pinned
# here — they're read from the release's checksums.txt (published by
# `make model-release`), so a model bump touches only MODELS_TAG.
PREFETCH_MODELS=(
  "tdt_ctc-110m-f16.gguf"
  "tdt-0.6b-v3-q4_k.gguf"
)

# Best-effort: pre-download the offline models so Apple Silicon works with no
# API key on first launch. Never fails the install — the in-app downloader
# recovers anything missing.
prefetch_models() {
  [[ "$(uname -m)" == "arm64" ]] || return 0
  mkdir -p "$MODELS_DIR" 2>/dev/null || return 0
  local sums f sum dest
  sums="$(curl -fsSL "${MODELS_BASE}/checksums.txt" 2>/dev/null)" || {
    log "Model checksums unavailable — the app will fetch models on first launch"
    return 0
  }
  for f in "${PREFETCH_MODELS[@]}"; do
    sum="$(awk -v f="$f" '$2==f {print $1}' <<<"$sums")"
    [[ -n "$sum" ]] || { log "Model ${f} not in checksums — app will fetch on first launch"; continue; }
    dest="${MODELS_DIR}/${f}"
    if [[ -f "$dest" ]] && shasum -a 256 "$dest" | grep -q "$sum"; then
      log "Model ${f} already present"; continue
    fi
    log "Downloading model ${f} (best-effort)..."
    if curl -fL --progress-bar "${MODELS_BASE}/${f}" -o "${dest}.part" \
       && shasum -a 256 "${dest}.part" | grep -q "$sum"; then
      mv -f "${dest}.part" "$dest"
      log "Model ${f} OK"
    else
      log "Model ${f} unavailable — the app will fetch it on first launch"
      rm -f "${dest}.part"
    fi
  done
}

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

log "Fetching offline models (best-effort)..."
prefetch_models || true

cat <<EOF

Zee ${VERSION} installed to ${APP_DIR}/Zee.app

Next:
  1. Launch Zee from Spotlight or:
       open ${APP_DIR}/Zee.app

  2. macOS may prompt for Microphone and Accessibility.
     Grant both, then hold Ctrl+Shift+Space to record.

On Apple Silicon, Zee works offline out of the box — no API key needed.
For cloud providers, set a key and pick the provider from the tray menu:
       launchctl setenv GROQ_API_KEY       your_key
       launchctl setenv OPENAI_API_KEY     your_key
       launchctl setenv DEEPGRAM_API_KEY   your_key
       launchctl setenv MISTRAL_API_KEY    your_key
       launchctl setenv ELEVENLABS_API_KEY your_key
     (add to ~/.zshrc to persist across logins)
EOF
