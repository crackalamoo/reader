#!/bin/sh
# Downloads PDF.js into static/pdfjs/ (not checked in; embedded into the binary
# at build time) and applies the reader's local modifications. Run once before
# `go run .` / `go build`.
#
# Uses the LEGACY build: it is transpiled for older browsers, and the modern
# build silently failed to run in the user's browser. Keeps build/pdf*.mjs and
# the web/ viewer; drops source maps, the debugger, the sample PDF, and all
# locales except en-US.
set -eu

VERSION=6.3.289
cd "$(dirname "$0")/.."
DEST=static/pdfjs
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

curl -fsSL -o "$TMP/pdfjs.zip" \
  "https://github.com/mozilla/pdf.js/releases/download/v$VERSION/pdfjs-$VERSION-legacy-dist.zip"
unzip -q "$TMP/pdfjs.zip" -d "$TMP/dist"

rm -rf "$DEST"
mkdir -p "$DEST/build" "$DEST/web/locale"
cp "$TMP/dist/LICENSE" "$DEST/"
cp "$TMP/dist/build/pdf.mjs" "$TMP/dist/build/pdf.worker.mjs" "$TMP/dist/build/pdf.sandbox.mjs" "$DEST/build/"
for f in viewer.html viewer.mjs viewer.css; do cp "$TMP/dist/web/$f" "$DEST/web/"; done
for d in images cmaps standard_fonts wasm iccs; do cp -R "$TMP/dist/web/$d" "$DEST/web/"; done
cp -R "$TMP/dist/web/locale/en-US" "$DEST/web/locale/"
printf '{"en-us":"en-US/viewer.ftl"}\n' > "$DEST/web/locale/locale.json"

# Report text selection to the parent page so "Quote selection" works for PDFs.
# The viewer's CSP forbids inline scripts, so this is an external script.
awk '/<\/head>/ && !done {
  print "  <!-- reader: report text selection to the parent page (served by main.go) -->"
  print "  <script src=\"/selection.js\"></script>"
  done=1
} { print }' "$DEST/web/viewer.html" > "$TMP/viewer.html"
mv "$TMP/viewer.html" "$DEST/web/viewer.html"

echo "PDF.js $VERSION (legacy) installed in $DEST"
