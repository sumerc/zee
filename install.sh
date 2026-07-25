#!/usr/bin/env bash
# Zee installer for macOS.
# Usage: curl -fsSL https://raw.githubusercontent.com/sumerc/zee/main/install.sh | bash
#        VERSION=vX.Y.Z bash install.sh
#        DMG_PATH=./Zee-<version>.dmg bash install.sh   (dev: install a locally
#        built DMG — `make app` — running the full flow minus download/checksum)
set -euo pipefail

REPO="sumerc/zee"
APP_DIR="/Applications"
TMP="$(mktemp -d)"
MOUNT=""
DL_PID="" # pid of the in-flight background download, so cleanup can kill it
STAGE="${APP_DIR}/.Zee.app.new-$$" # staged copy, swapped in atomically
BACKUP="${APP_DIR}/.Zee.app.old-$$" # previous bundle, kept until swap succeeds

# Bold the key status lines when writing to a terminal — under `curl | bash`
# only stdin is the pipe, so stdout/stderr are still the user's tty. No-op when
# redirected to a file/pipe (no escape codes in captured logs).
[[ -t 1 ]] && BOLD=$'\033[1m' RESET=$'\033[0m' || BOLD='' RESET=''
[[ -t 2 ]] && EBOLD=$'\033[1m' ERESET=$'\033[0m' || EBOLD='' ERESET=''

err() { echo "${EBOLD}error: $*${ERESET}" >&2; exit 1; }
log() { echo "==> $*"; }
# logb: a status line worth not losing in the scroll (bold).
logb() { echo "==> ${BOLD}$*${RESET}"; }
cleanup() {
  # Kill a still-running background download (Ctrl+C would otherwise orphan it).
  [[ -n "${DL_PID:-}" ]] && kill "$DL_PID" 2>/dev/null || true
  [[ -n "$MOUNT" ]] && hdiutil detach "$MOUNT" -quiet >/dev/null 2>&1 || true
  rm -rf "$TMP"
  # If interrupted in the swap window (old already moved to BACKUP, new not yet
  # in place), restore the old app rather than leave nothing — data safety wins
  # over cleanliness. Only then discard leftovers.
  if [[ ! -e "${APP_DIR}/Zee.app" && -d "$BACKUP" ]]; then
    run_or_sudo mv "$BACKUP" "${APP_DIR}/Zee.app" || true
  fi
  [[ -e "$STAGE" ]] && run_or_sudo rm -rf "$STAGE" || true
  [[ -e "$BACKUP" ]] && run_or_sudo rm -rf "$BACKUP" || true
}
run_or_sudo() {
  "$@" 2>/dev/null || { log "Need sudo: $*"; sudo "$@"; }
}
# fetch <url> <dest> — download with a compact single-line progress display.
# curl's --progress-bar is terminal-width and stacks lines when it wraps (and
# draws one bar per redirect hop), so draw our own: resolve the final URL +
# size with one HEAD, download silently in the background, and redraw a short
# fixed-width status that can never wrap.
fetch() {
  local head final size cur
  head="$(curl -fsIL -w 'FINAL:%{url_effective}\n' "$1")" || return 1
  final="$(printf '%s' "$head" | sed -n 's/^FINAL://p' | tail -1)"
  size="$(printf '%s' "$head" | awk 'tolower($1)=="content-length:"{s=$2} END{print s+0}')"

  # -C -: resume a leftover partial (an interrupted run keeps its .part; the
  # caller's checksum still gates the final file, and release assets are
  # immutable, so continuing is always safe). DL_PID lets cleanup kill it on
  # Ctrl+C instead of orphaning the curl.
  curl -fs -C - "$final" -o "$2" &
  DL_PID=$!
  local width=20 filled bar
  while kill -0 "$DL_PID" 2>/dev/null; do
    cur="$(stat -f%z "$2" 2>/dev/null || echo 0)"
    if [[ "$size" -gt 0 ]]; then
      filled=$((cur * width / size))
      # '·' is multibyte — build the bar by hand (tr and printf field widths
      # count bytes, not characters)
      bar="$(printf '%*s' "$filled" '' | sed 's/ /·/g')$(printf '%*s' "$((width - filled))" '')"
      printf '\r    [%s] %3d%%  %d/%d MB ' "$bar" \
        "$((cur * 100 / size))" "$((cur / 1048576))" "$((size / 1048576))"
    else
      printf '\r    %d MB ' "$((cur / 1048576))"
    fi
    sleep 0.5
  done
  printf '\r\033[K'
  wait "$DL_PID"; local rc=$?
  DL_PID=""
  return $rc
}
trap cleanup EXIT

[[ "$(uname -s)" == "Darwin" ]] || err "Zee currently supports macOS only."
[[ "$(uname -m)" == "arm64" ]] || err "Zee requires Apple Silicon (arm64) — the local engine is arm64-only, and releases ship no Intel build."

# Same ANSI-Shadow banner the setup wizard prints (which gets -no-banner from
# us so it doesn't repeat); shown first so the install identifies itself
# before any downloading starts.
cat <<'BANNER'

 ███████╗███████╗███████╗
 ╚══███╔╝██╔════╝██╔════╝
   ███╔╝ █████╗  █████╗
  ███╔╝  ██╔══╝  ██╔══╝
 ███████╗███████╗███████╗
 ╚══════╝╚══════╝╚══════╝

BANNER

# Refuse to replace the bundle out from under a running instance. We don't quit
# it ourselves: `osascript ... to quit` launches Zee if it's *not* running (to
# deliver the event) and triggers an Automation TCC prompt — the user just
# quits it deliberately. Fail-fast, before any download.
if pgrep -x zee >/dev/null 2>&1; then
  err "Zee is running — quit it first (menu bar → Quit), then re-run the installer."
fi

# DMG_PATH installs a locally built DMG (dev flow): version resolution,
# download, and checksum are skipped — everything else (model prefetch, copy,
# quarantine clear, setup handoff) runs identically.
DMG_PATH="${DMG_PATH:-}"
VERSION="${VERSION:-}"
if [[ -n "$DMG_PATH" ]]; then
  [[ -f "$DMG_PATH" ]] || err "local DMG not found: ${DMG_PATH}"
  log "Installing local DMG ${DMG_PATH}"
else
  if [[ -z "$VERSION" ]]; then
    log "Resolving latest release..."
    VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
      | awk -F'"' '/"tag_name"/ {print $4; exit}')"
    [[ -n "$VERSION" ]] || err "could not resolve latest version (GitHub API rate limit?). Set VERSION=vX.Y.Z and retry."
  fi
  log "Installing Zee ${VERSION}"
fi

# Offline models live under an immutable, app-version-independent release tag.
# The registry — filenames, hashes, and which models to pre-fetch — is generated
# from localmodel.go into localmodel/manifest.txt and read here from main, so
# nothing is hardcoded. Columns: filename<TAB>sha256<TAB>prefetch.
MODELS_TAG="models-v2"
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
    # A bare $dest (no .part) only exists because a prior run passed the sha
    # check below and mv'd it into place — in-flight downloads are always
    # .part — so existence is proof of verification. Skip re-hashing it: these
    # models are ~1 GB total and re-hashing them on every install cost seconds
    # for no added safety.
    if [[ -f "$dest" ]]; then
      log "Model ${f} already present"; continue
    fi
    log "Downloading model ${f}..."
    # Attempt 1 may resume a leftover .part; if the assembled file fails the
    # checksum (stale/corrupt partial), retry once from scratch before erring.
    for attempt in 1 2; do
      fetch "${MODELS_BASE}/${f}" "${dest}.part" \
        || { rm -f "${dest}.part"; err "model download failed: ${MODELS_BASE}/${f}"; }
      if shasum -a 256 "${dest}.part" | grep -q "$sum"; then
        mv -f "${dest}.part" "$dest"
        log "Model ${f} OK"
        break
      fi
      rm -f "${dest}.part"
      [[ "$attempt" -eq 1 ]] \
        && log "Checksum mismatch for ${f} (stale partial?) — retrying from scratch..." \
        || err "checksum mismatch for model ${f}"
    done
  done < <(awk '!/^#/ && $3=="true" {print $1, $2}' <<<"$manifest")
}

log "Fetching offline models..."
prefetch_models

if [[ -n "$DMG_PATH" ]]; then
  DMG_FILE="$DMG_PATH"
else
  DMG="Zee-${VERSION}.dmg"
  BASE="https://github.com/${REPO}/releases/download/${VERSION}"
  DMG_FILE="${TMP}/${DMG}"

  log "Downloading ${DMG}..."
  fetch "${BASE}/${DMG}" "$DMG_FILE" \
    || err "download failed: ${BASE}/${DMG}"

  log "Verifying checksum..."
  curl -fsSL "${BASE}/checksums.txt" -o "${TMP}/checksums.txt" \
    || err "download failed: ${BASE}/checksums.txt"
  expected="$(awk -v f="${DMG}" '$2==f {print $1}' "${TMP}/checksums.txt")"
  [[ -n "$expected" ]] || err "${DMG} not found in checksums.txt"
  actual="$(shasum -a 256 "$DMG_FILE" | awk '{print $1}')"
  [[ "$expected" == "$actual" ]] || err "checksum mismatch (expected $expected, got $actual)"
  log "Checksum OK"
fi

log "Mounting DMG..."
MOUNT="$(hdiutil attach -nobrowse -readonly -mountrandom /tmp "$DMG_FILE" \
  | grep -oE '/(private/tmp|Volumes)/[^[:space:]]+' \
  | tail -1)"
[[ -n "$MOUNT" && -d "$MOUNT/Zee.app" ]] || err "Zee.app not found in DMG"

# Stage-then-swap: copy the new bundle and clear its quarantine in a hidden
# staging dir on the same volume, so the slow/fallible work never touches the
# installed app. The swap itself is two fast renames (atomic on the volume);
# only if it succeeds is the old bundle discarded. A failed swap restores it.
log "Staging Zee.app..."
run_or_sudo rm -rf "$STAGE"
run_or_sudo cp -R "$MOUNT/Zee.app" "$STAGE"
run_or_sudo xattr -cr "$STAGE"

log "Installing to ${APP_DIR}/Zee.app..."
if [[ -d "${APP_DIR}/Zee.app" ]]; then
  run_or_sudo mv "${APP_DIR}/Zee.app" "$BACKUP"
fi
if ! run_or_sudo mv "$STAGE" "${APP_DIR}/Zee.app"; then
  [[ -d "$BACKUP" ]] && run_or_sudo mv "$BACKUP" "${APP_DIR}/Zee.app"
  err "install failed during swap — previous Zee.app restored"
fi
run_or_sudo rm -rf "$BACKUP"

logb "Zee ${VERSION:-(local build)} installed to ${APP_DIR}/Zee.app"

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
  # Plain invocation: zee re-execs itself through `open` for correct TCC
  # attribution and still returns the wizard's real exit code, so a failed
  # setup (mic denied, nothing verified) fails this installer too.
  # On success the wizard prints its own completion message — nothing to add.
  if ! "${APP_DIR}/Zee.app/Contents/MacOS/zee" setup -no-banner <"$TTY" >"$TTY" 2>&1; then
    logb "Zee is installed, but setup did not finish cleanly."
    logb "Fix it any time with: ${APP_DIR}/Zee.app/Contents/MacOS/zee setup"
    exit 1
  fi
else
  cat <<EOF

Setup was not run (no interactive terminal detected).
Finish configuring Zee — provider + API key, permissions, and hotkey — with:

    ${BOLD}/Applications/Zee.app/Contents/MacOS/zee setup${RESET}

On Apple Silicon, Zee also works offline out of the box (local model, no key).
EOF
fi
