#!/usr/bin/env bash
# zee-transcribe-compare — run every saved Zee sample through every available
# STT provider and emit both human-readable and JSONL results.
#
# Required: ZEE_BIN (absolute path to zee binary). Falls back to discovering
# the running process or /Users/supo/Desktop/p/zee/zee.
set -u

ZEE_BIN="${ZEE_BIN:-}"
if [ -z "$ZEE_BIN" ]; then
  ZEE_BIN=$(lsof -c zee 2>/dev/null | awk '$4=="txt" && $NF ~ /zee$/ {print $NF; exit}')
fi
[ -z "$ZEE_BIN" ] && ZEE_BIN="/Users/supo/Desktop/p/zee/zee"
if [ ! -x "$ZEE_BIN" ]; then
  echo "ERROR: zee binary not found or not executable: $ZEE_BIN" >&2
  exit 1
fi

ZEE_DIR="$HOME/Library/Application Support/zee"
CFG="$ZEE_DIR/config.json"
SAMPLES="$ZEE_DIR/samples"
BACKUP="/tmp/zee-config-backup.$$.json"
HUMAN=/tmp/zee-compare-results.txt
JSONL=/tmp/zee-compare-results.jsonl

[ -f "$CFG" ] || { echo "ERROR: $CFG missing" >&2; exit 1; }
[ -d "$SAMPLES" ] || { echo "ERROR: $SAMPLES missing" >&2; exit 1; }

cp "$CFG" "$BACKUP"
restore() { cp "$BACKUP" "$CFG" 2>/dev/null; rm -f "$BACKUP"; }
trap restore EXIT INT TERM

: > "$HUMAN"
: > "$JSONL"

# Header: active hints (what each provider receives as biasing).
HINTS_FILE="$ZEE_DIR/hints.txt"
{
  echo "########## hints.txt ##########"
  if [ -f "$HINTS_FILE" ]; then
    grep -vE '^\s*(#|$)' "$HINTS_FILE" | paste -sd, -
  else
    echo "(no hints.txt found — providers receive no biasing)"
  fi
  echo
} | tee -a "$HUMAN"

# (provider, model, env_var) — keep groq turbo first since it's the fastest baseline.
# Update this list when zee adds providers/models (transcriber/*.go).
COMBOS=(
  "groq|whisper-large-v3-turbo|GROQ_API_KEY"
  "groq|whisper-large-v3|GROQ_API_KEY"
  "openai|gpt-4o-transcribe|OPENAI_API_KEY"
  "mistral|voxtral-mini-latest|MISTRAL_API_KEY"
  "elevenlabs|scribe_v2|ELEVENLABS_API_KEY"
)

# JSON string escaper for the JSONL output. Python is on every macOS.
jesc() { python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()), end="")'; }

write_cfg() {
  local prov="$1" model="$2"
  cat > "$CFG" <<EOF
{
  "language": "en",
  "device": "C-1U",
  "provider": "$prov",
  "model": "$model",
  "auto_paste": true,
  "auto_start": false
}
EOF
}

# Discover samples (sorted by directory name = chronological).
# Optional SAMPLE env var filters to a single dir (basename match) or "latest".
SAMPLE_DIRS=()
while IFS= read -r d; do SAMPLE_DIRS+=("$d"); done < <(find "$SAMPLES" -maxdepth 1 -mindepth 1 -type d -name "2026-*" | sort)
if [ -n "${SAMPLE:-}" ]; then
  if [ "$SAMPLE" = "latest" ]; then
    SAMPLE_DIRS=("${SAMPLE_DIRS[-1]}")
  else
    FILTERED=()
    for d in "${SAMPLE_DIRS[@]}"; do
      [ "$(basename "$d")" = "$SAMPLE" ] && FILTERED+=("$d")
    done
    SAMPLE_DIRS=("${FILTERED[@]}")
  fi
fi

if [ "${#SAMPLE_DIRS[@]}" -eq 0 ]; then
  echo "No samples found under $SAMPLES — enable ZEE_SAVE_LAST_AUDIO=1 and record some clips first." >&2
  exit 1
fi

for combo in "${COMBOS[@]}"; do
  IFS='|' read -r prov model envk <<< "$combo"
  if [ -z "${!envk:-}" ]; then
    echo "########## SKIP $prov/$model ($envk not set) ##########" | tee -a "$HUMAN"
    continue
  fi
  echo "########## $prov / $model ##########" | tee -a "$HUMAN"
  write_cfg "$prov" "$model"
  for d in "${SAMPLE_DIRS[@]}"; do
    sample=$(basename "$d")
    audio=$(find "$d" -maxdepth 1 -type f -name "audio.*" | head -1)
    [ -z "$audio" ] && continue
    echo "----- $sample -----" | tee -a "$HUMAN"
    t0=$(python3 -c 'import time; print(int(time.time()*1000))')
    text=$(timeout 45 "$ZEE_BIN" -transcribe "$audio" 2>&1)
    rc=$?
    t1=$(python3 -c 'import time; print(int(time.time()*1000))')
    elapsed_ms=$((t1 - t0))
    echo "[${elapsed_ms}ms] $text" | tee -a "$HUMAN"
    text_trimmed=$(printf '%s' "$text")
    if [ $rc -ne 0 ]; then
      printf '{"sample":%s,"provider":%s,"model":%s,"elapsed_ms":%d,"error":%s}\n' \
        "$(printf '%s' "$sample" | jesc)" \
        "$(printf '%s' "$prov" | jesc)" \
        "$(printf '%s' "$model" | jesc)" \
        "$elapsed_ms" \
        "$(printf '%s' "$text_trimmed" | jesc)" >> "$JSONL"
    else
      printf '{"sample":%s,"provider":%s,"model":%s,"elapsed_ms":%d,"text":%s}\n' \
        "$(printf '%s' "$sample" | jesc)" \
        "$(printf '%s' "$prov" | jesc)" \
        "$(printf '%s' "$model" | jesc)" \
        "$elapsed_ms" \
        "$(printf '%s' "$text_trimmed" | jesc)" >> "$JSONL"
    fi
  done
done

# Also dump per-sample metadata so the rendering step doesn't need to re-stat files.
META=/tmp/zee-compare-samples.jsonl
: > "$META"
for d in "${SAMPLE_DIRS[@]}"; do
  sample=$(basename "$d")
  info="$d/info.json"
  audio=$(find "$d" -maxdepth 1 -type f -name "audio.*" | head -1)
  [ -z "$audio" ] && continue
  size_kb=$(awk -v b="$(stat -f%z "$audio")" 'BEGIN{printf "%.1f", b/1024}')
  ext="${audio##*.}"
  python3 -c "
import json,sys
info=json.load(open('$info'))
info['sample']='$sample'
info['size_kb']=$size_kb
info['ext']='$ext'
print(json.dumps(info))
" >> "$META"
done

echo
echo "DONE — config restored."
echo "  samples meta: $META"
echo "  raw output:   $HUMAN"
echo "  per-cell:     $JSONL"
