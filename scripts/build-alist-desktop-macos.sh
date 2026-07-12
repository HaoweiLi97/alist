#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DESKTOP_DIR="$ROOT_DIR/desktop/macos"
BUILD_DIR="$DESKTOP_DIR/build"
WORK_DIR="$BUILD_DIR/work"

APP_NAME="${ALIST_DESKTOP_APP_NAME:-AList Desktop}"
BUNDLE_ID="${ALIST_DESKTOP_BUNDLE_ID:-com.alist.desktop}"
VERSION="${ALIST_DESKTOP_VERSION:-$(git -C "$ROOT_DIR" describe --tags --always 2>/dev/null || echo dev)}"
VERSION="${VERSION#v}"
SAFE_VERSION="${VERSION//\//-}"
BUNDLE_VERSION="$VERSION"
if [[ ! "$BUNDLE_VERSION" =~ ^[0-9]+(\.[0-9]+){1,2}$ ]]; then
  BUNDLE_VERSION="0.0.0"
fi
BUILD_NUMBER="${GITHUB_RUN_NUMBER:-1}"
APP_BUNDLE="$BUILD_DIR/$APP_NAME.app"
CONTENTS_DIR="$APP_BUNDLE/Contents"
MACOS_DIR="$CONTENTS_DIR/MacOS"
RESOURCES_DIR="$CONTENTS_DIR/Resources"
APP_EXECUTABLE="$MACOS_DIR/AListDesktop"
ALIST_EXECUTABLE="$RESOURCES_DIR/bin/alist"

rm -rf "$BUILD_DIR"
mkdir -p "$WORK_DIR" "$MACOS_DIR" "$RESOURCES_DIR/bin"

build_alist() {
  local arch="$1"
  local goarch="$arch"
  if [[ "$arch" == "x86_64" ]]; then
    goarch="amd64"
  fi
  local output="$WORK_DIR/alist-$arch"
  local cc="clang -arch $arch"
  echo "Building AList server for darwin/$arch"
  (
    cd "$ROOT_DIR"
    CGO_ENABLED=1 GOOS=darwin GOARCH="$goarch" CC="$cc" \
      go build -tags=jsoniter -trimpath \
      -ldflags="-s -w -X github.com/alist-org/alist/v3/internal/conf.Version=$VERSION" \
      -o "$output" .
  )
}

build_host() {
  local arch="$1"
  local scratch="$WORK_DIR/swift-$arch"
  echo "Building desktop host for $arch"
  swift build \
    --package-path "$DESKTOP_DIR" \
    --scratch-path "$scratch" \
    --configuration release \
    --arch "$arch"
  local bin_path
  bin_path="$(swift build --package-path "$DESKTOP_DIR" --scratch-path "$scratch" --configuration release --arch "$arch" --show-bin-path)"
  cp "$bin_path/AListDesktop" "$WORK_DIR/AListDesktop-$arch"
}

build_alist arm64
build_alist x86_64
build_host arm64
build_host x86_64

lipo -create "$WORK_DIR/alist-arm64" "$WORK_DIR/alist-x86_64" -output "$ALIST_EXECUTABLE"
lipo -create "$WORK_DIR/AListDesktop-arm64" "$WORK_DIR/AListDesktop-x86_64" -output "$APP_EXECUTABLE"
chmod 755 "$ALIST_EXECUTABLE" "$APP_EXECUTABLE"

sed \
  -e "s|__APP_NAME__|$APP_NAME|g" \
  -e "s|__EXECUTABLE_NAME__|AListDesktop|g" \
  -e "s|__BUNDLE_ID__|$BUNDLE_ID|g" \
  -e "s|__VERSION__|$BUNDLE_VERSION|g" \
  -e "s|__BUILD_NUMBER__|$BUILD_NUMBER|g" \
  "$DESKTOP_DIR/Resources/Info.plist.template" > "$CONTENTS_DIR/Info.plist"

create_icons() {
  local logo="$ROOT_DIR/desktop/windows/Resources/logo.svg"
  local source_png="$WORK_DIR/AppIcon-1024.png"
  local iconset="$WORK_DIR/AppIcon.iconset"

  if command -v rsvg-convert >/dev/null 2>&1; then
    rsvg-convert -w 1024 -h 1024 "$logo" > "$source_png"
  else
    qlmanage -t -s 1024 -o "$WORK_DIR" "$logo" >/dev/null 2>&1 || true
    local preview
    preview="$(find "$WORK_DIR" -maxdepth 1 -name 'logo.svg.png' -print -quit)"
    if [[ -z "$preview" ]]; then
      echo "Icon converter unavailable; packaging with the default macOS app icon"
      return
    fi
    mv "$preview" "$source_png"
  fi

  mkdir -p "$iconset"
  for size in 16 32 128 256 512; do
    sips -z "$size" "$size" "$source_png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
    double=$((size * 2))
    sips -z "$double" "$double" "$source_png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
  done
  iconutil -c icns "$iconset" -o "$RESOURCES_DIR/AppIcon.icns"
  sips -z 36 36 "$source_png" --out "$RESOURCES_DIR/MenuBarIcon.png" >/dev/null
}

create_icons

SIGN_IDENTITY="${ALIST_DESKTOP_CODESIGN_IDENTITY:--}"
if [[ "$SIGN_IDENTITY" == "-" ]]; then
  codesign --force --sign - "$ALIST_EXECUTABLE"
  codesign --force --deep --sign - "$APP_BUNDLE"
else
  codesign --force --options runtime --timestamp --sign "$SIGN_IDENTITY" "$ALIST_EXECUTABLE"
  codesign --force --deep --options runtime --timestamp --sign "$SIGN_IDENTITY" "$APP_BUNDLE"
fi
codesign --verify --deep --strict "$APP_BUNDLE"

ZIP_PATH="$BUILD_DIR/AList-Desktop-macOS-universal-$SAFE_VERSION.zip"
ditto -c -k --sequesterRsrc --keepParent "$APP_BUNDLE" "$ZIP_PATH"

DMG_STAGE="$WORK_DIR/dmg"
mkdir -p "$DMG_STAGE"
cp -R "$APP_BUNDLE" "$DMG_STAGE/"
ln -s /Applications "$DMG_STAGE/Applications"
DMG_PATH="$BUILD_DIR/AList-Desktop-macOS-universal-$SAFE_VERSION.dmg"
hdiutil create -volname "$APP_NAME" -srcfolder "$DMG_STAGE" -ov -format UDZO "$DMG_PATH" >/dev/null

if [[ -n "${ALIST_DESKTOP_NOTARY_PROFILE:-}" ]]; then
  xcrun notarytool submit "$DMG_PATH" --keychain-profile "$ALIST_DESKTOP_NOTARY_PROFILE" --wait
  xcrun stapler staple "$APP_BUNDLE"
  xcrun stapler staple "$DMG_PATH"
elif [[ -n "${ALIST_DESKTOP_NOTARY_APPLE_ID:-}" && -n "${ALIST_DESKTOP_NOTARY_TEAM_ID:-}" && -n "${ALIST_DESKTOP_NOTARY_PASSWORD:-}" ]]; then
  xcrun notarytool submit "$DMG_PATH" \
    --apple-id "$ALIST_DESKTOP_NOTARY_APPLE_ID" \
    --team-id "$ALIST_DESKTOP_NOTARY_TEAM_ID" \
    --password "$ALIST_DESKTOP_NOTARY_PASSWORD" \
    --wait
  xcrun stapler staple "$APP_BUNDLE"
  xcrun stapler staple "$DMG_PATH"
fi

echo "Created $ZIP_PATH"
echo "Created $DMG_PATH"
