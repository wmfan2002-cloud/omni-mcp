#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 4 ]]; then
  echo "Usage: $0 <binary-path> <arch> <version> <output-zip>" >&2
  exit 1
fi

BIN_PATH="$1"
ARCH="$2"
VERSION="$3"
OUT_ZIP="$4"

ROOT_DIR="$(cd "$(dirname "$0")/.." && pwd)"
WORK_DIR="$(mktemp -d)"
trap 'rm -rf "$WORK_DIR"' EXIT

APP_DIR="$WORK_DIR/Omni MCP.app"
mkdir -p "$APP_DIR/Contents/MacOS" "$APP_DIR/Contents/Resources"

cp "$BIN_PATH" "$APP_DIR/Contents/MacOS/omni-mcp"
chmod +x "$APP_DIR/Contents/MacOS/omni-mcp"

CLEAN_VER="${VERSION#v}"
if [[ -z "$CLEAN_VER" ]]; then
  CLEAN_VER="0.3.0"
fi

ICON_FILE="AppIcon.icns"
if command -v sips >/dev/null 2>&1 && command -v iconutil >/dev/null 2>&1 && [[ -f "$ROOT_DIR/assets/logo-512.png" ]]; then
  ICONSET="$WORK_DIR/AppIcon.iconset"
  mkdir -p "$ICONSET"
  for sz in 16 32 128 256 512; do
    sips -z "$sz" "$sz" "$ROOT_DIR/assets/logo-512.png" --out "$ICONSET/icon_${sz}x${sz}.png" >/dev/null
    sz2=$((sz * 2))
    if [[ $sz2 -le 512 ]]; then
      sips -z "$sz2" "$sz2" "$ROOT_DIR/assets/logo-512.png" --out "$ICONSET/icon_${sz}x${sz}@2x.png" >/dev/null
    else
      cp "$ROOT_DIR/assets/logo-512.png" "$ICONSET/icon_${sz}x${sz}@2x.png"
    fi
  done
  iconutil -c icns "$ICONSET" -o "$APP_DIR/Contents/Resources/AppIcon.icns"
else
  ICON_FILE="AppIcon.png"
  cp "$ROOT_DIR/assets/logo-512.png" "$APP_DIR/Contents/Resources/AppIcon.png"
fi

cat > "$APP_DIR/Contents/Info.plist" <<PLIST
<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>CFBundleDevelopmentRegion</key>
  <string>en</string>
  <key>CFBundleDisplayName</key>
  <string>Omni MCP</string>
  <key>CFBundleExecutable</key>
  <string>omni-mcp</string>
  <key>CFBundleIconFile</key>
  <string>${ICON_FILE}</string>
  <key>CFBundleIdentifier</key>
  <string>io.github.omnimcp.desktop</string>
  <key>CFBundleInfoDictionaryVersion</key>
  <string>6.0</string>
  <key>CFBundleName</key>
  <string>Omni MCP</string>
  <key>CFBundlePackageType</key>
  <string>APPL</string>
  <key>CFBundleShortVersionString</key>
  <string>${CLEAN_VER}</string>
  <key>CFBundleVersion</key>
  <string>${CLEAN_VER}</string>
  <key>LSMinimumSystemVersion</key>
  <string>12.0</string>
  <key>NSHighResolutionCapable</key>
  <true/>
</dict>
</plist>
PLIST

if command -v codesign >/dev/null 2>&1; then
  codesign --force --deep --sign - "$APP_DIR"
fi

mkdir -p "$(dirname "$OUT_ZIP")"
rm -f "$OUT_ZIP"
if command -v ditto >/dev/null 2>&1; then
  ditto -c -k --sequesterRsrc --keepParent "$APP_DIR" "$OUT_ZIP"
else
  (cd "$WORK_DIR" && zip -rq "$(cd "$(dirname "$OUT_ZIP")" && pwd)/$(basename "$OUT_ZIP")" "Omni MCP.app")
fi

echo "Packaged macOS desktop bundle ($ARCH): $OUT_ZIP"
