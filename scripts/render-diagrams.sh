#!/usr/bin/env bash
# Renders the documentation diagrams: every docs/assets/diagrams/<name>.html
# becomes docs/assets/diagrams/<name>.png beside it, at twice the CSS size so
# the picture stays sharp on a page, on a high-density screen and on a slide.
#
#   scripts/render-diagrams.sh                 # every diagram
#   scripts/render-diagrams.sh compaction-chain serve-runtime
#
# Needs a Chromium or Chrome binary (CHROME=/path overrides the lookup) and the
# Noto Sans font the diagrams are drawn with (fonts-noto-core on Debian and
# Ubuntu). optipng, when installed, recompresses the picture losslessly, which
# takes a fifth or so off a dense diagram and keeps it under the 1 MB an asset
# may weigh. The width comes from the page (--width on <body>, 1200 px unless a
# diagram sets its own), the height from the data-height that diagram.js
# records once the arrows are drawn.
set -euo pipefail

root="$(cd "$(dirname "$0")/.." && pwd)"
dir="$root/docs/assets/diagrams"
scale="${DIAGRAM_SCALE:-2}"

chrome="${CHROME:-}"
if [ -z "$chrome" ]; then
  for c in chromium chromium-browser google-chrome google-chrome-stable; do
    if command -v "$c" >/dev/null 2>&1; then
      chrome="$(command -v "$c")"
      break
    fi
  done
fi
if [ -z "$chrome" ]; then
  echo "render-diagrams: no Chromium or Chrome found; set CHROME=/path/to/chrome" >&2
  exit 1
fi
if command -v fc-list >/dev/null 2>&1 && ! fc-list : family | grep 'Noto Sans' >/dev/null; then
  echo "render-diagrams: warning: the Noto Sans font is not installed, the text will be set in a fallback font" >&2
fi

profile="$(mktemp -d)"
trap 'rm -rf "$profile"' EXIT
run_chrome() {
  "$chrome" --headless=new --no-sandbox --disable-gpu --hide-scrollbars \
    --user-data-dir="$profile" --no-first-run --disable-extensions \
    --allow-file-access-from-files --virtual-time-budget=3000 "$@" 2>/dev/null
}

if [ "$#" -gt 0 ]; then
  names=("$@")
else
  names=()
  for f in "$dir"/*.html; do
    names+=("$(basename "$f" .html)")
  done
fi

for name in "${names[@]}"; do
  src="$dir/$name.html"
  out="$dir/$name.png"
  if [ ! -f "$src" ]; then
    echo "render-diagrams: no $src" >&2
    exit 1
  fi
  dom="$(run_chrome --window-size=1600,1200 --dump-dom "file://$src")"
  height="$(printf '%s' "$dom" | grep -o 'data-height="[0-9]*"' | head -n1 | grep -o '[0-9]*' || true)"
  width="$(printf '%s' "$dom" | grep -o -- '--width: *[0-9]*px' | head -n1 | grep -o '[0-9]*' || true)"
  width="${width:-1200}"
  if [ -z "$height" ]; then
    echo "render-diagrams: $name.html recorded no height; does it call wires()?" >&2
    exit 1
  fi
  run_chrome --window-size="$width,$height" --force-device-scale-factor="$scale" \
    --default-background-color=ffffffff --screenshot="$out" "file://$src" >/dev/null
  if command -v optipng >/dev/null 2>&1; then
    optipng -quiet -o2 "$out"
  fi
  echo "$name.png: $((width * scale))x$((height * scale))"
done
