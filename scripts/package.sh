#!/bin/sh
set -eu

VERSION="${1:-0.4.5}"
ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
DIST_DIR="$ROOT_DIR/dist"
STAGE_DIR="$DIST_DIR/stage"

rm -rf "$DIST_DIR"
mkdir -p "$STAGE_DIR"

build_archive() {
  os="$1"
  arch="$2"
  suffix="$3"
  archive="$4"
  package="rekordlink_${VERSION}_${os}_${arch}"
  package_dir="$STAGE_DIR/$package"
  mkdir -p "$package_dir"
  (cd "$ROOT_DIR" && GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -trimpath \
    -ldflags "-s -w -X main.version=$VERSION" \
    -o "$package_dir/rekordlink$suffix" ./cmd/rekordlink)
  if [ "$os" = "darwin" ] && [ "$(uname -s)" = "Darwin" ]; then
    native_arch="$arch"
    if [ "$native_arch" = "amd64" ]; then
      native_arch="x86_64"
    fi
    app_dir="$package_dir/RekordLink.app"
    mkdir -p "$app_dir/Contents/MacOS" "$app_dir/Contents/Resources"
    cp "$package_dir/rekordlink" "$app_dir/Contents/Resources/rekordlink"
    sed "s/@@VERSION@@/$VERSION/g" "$ROOT_DIR/macos/RekordLinkMenu/Info.plist.in" > "$app_dir/Contents/Info.plist"
    xcrun --sdk macosx clang -fobjc-arc -framework Cocoa -mmacosx-version-min=11.0 -arch "$native_arch" \
      -o "$app_dir/Contents/MacOS/RekordLinkMenu" "$ROOT_DIR/macos/RekordLinkMenu/main.m"
    codesign --force --deep --sign "${MACOS_SIGN_IDENTITY:--}" "$app_dir"
  fi
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/SECURITY.md" "$ROOT_DIR/CHANGELOG.md" "$package_dir/"
  mkdir -p "$package_dir/docs"
  cp "$ROOT_DIR/docs/RESEARCH_AND_ARCHITECTURE.md" "$ROOT_DIR/docs/DOCKER_RELAY.md" "$ROOT_DIR/docs/CLOUDFLARE_RELAY.md" "$package_dir/docs/"
  if [ "$archive" = "zip" ]; then
    (cd "$STAGE_DIR" && zip -q -r "$DIST_DIR/$package.zip" "$package")
  else
    (cd "$STAGE_DIR" && tar -czf "$DIST_DIR/$package.tar.gz" "$package")
  fi
}

build_archive darwin arm64 "" tar
build_archive darwin amd64 "" tar
build_archive linux amd64 "" tar
build_archive windows amd64 ".exe" zip

docker_package="rekordlink_${VERSION}_docker_source"
docker_dir="$STAGE_DIR/$docker_package"
mkdir -p "$docker_dir/docs"
cp "$ROOT_DIR/Dockerfile" "$ROOT_DIR/compose.yaml" "$ROOT_DIR/compose.cloudflare.yaml" "$ROOT_DIR/.env.example" "$ROOT_DIR/.env.cloudflare.example" "$ROOT_DIR/.dockerignore" "$ROOT_DIR/go.mod" "$docker_dir/"
cp -R "$ROOT_DIR/cmd" "$ROOT_DIR/internal" "$ROOT_DIR/docker" "$docker_dir/"
find "$docker_dir" -name '.DS_Store' -delete
cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/SECURITY.md" "$ROOT_DIR/CHANGELOG.md" "$docker_dir/"
cp "$ROOT_DIR/docs/DOCKER_RELAY.md" "$ROOT_DIR/docs/CLOUDFLARE_RELAY.md" "$ROOT_DIR/docs/RESEARCH_AND_ARCHITECTURE.md" "$docker_dir/docs/"
for required in \
  "$docker_dir/cmd/rekordlink/main.go" \
  "$docker_dir/internal/library/testdata/dj-a.xml" \
  "$docker_dir/internal/library/testdata/dj-b.xml"; do
  if [ ! -s "$required" ]; then
    echo "refusing to package empty required file: $required" >&2
    exit 1
  fi
done
(cd "$STAGE_DIR" && tar -czf "$DIST_DIR/$docker_package.tar.gz" "$docker_package")

if [ "$(uname -s)" = "Darwin" ]; then
  universal_package="RekordLink_${VERSION}_macOS_universal"
  universal_dir="$STAGE_DIR/$universal_package"
  universal_app="$universal_dir/RekordLink.app"
  mkdir -p "$universal_app/Contents/MacOS" "$universal_app/Contents/Resources"
  lipo -create \
    "$STAGE_DIR/rekordlink_${VERSION}_darwin_arm64/rekordlink" \
    "$STAGE_DIR/rekordlink_${VERSION}_darwin_amd64/rekordlink" \
    -output "$universal_app/Contents/Resources/rekordlink"
  sed "s/@@VERSION@@/$VERSION/g" "$ROOT_DIR/macos/RekordLinkMenu/Info.plist.in" > "$universal_app/Contents/Info.plist"
  xcrun --sdk macosx clang -fobjc-arc -framework Cocoa -mmacosx-version-min=11.0 -arch arm64 -arch x86_64 \
    -o "$universal_app/Contents/MacOS/RekordLinkMenu" "$ROOT_DIR/macos/RekordLinkMenu/main.m"
  codesign --force --deep --sign "${MACOS_SIGN_IDENTITY:--}" "$universal_app"
  cp "$ROOT_DIR/README.md" "$ROOT_DIR/LICENSE" "$ROOT_DIR/SECURITY.md" "$ROOT_DIR/CHANGELOG.md" "$universal_dir/"
  mkdir -p "$universal_dir/docs"
  cp "$ROOT_DIR/docs/RESEARCH_AND_ARCHITECTURE.md" "$ROOT_DIR/docs/DOCKER_RELAY.md" "$ROOT_DIR/docs/CLOUDFLARE_RELAY.md" "$universal_dir/docs/"
  (cd "$STAGE_DIR" && zip -q -r "$DIST_DIR/$universal_package.zip" "$universal_package")
fi

rm -rf "$STAGE_DIR"
(cd "$DIST_DIR" && shasum -a 256 ./*.tar.gz ./*.zip > SHA256SUMS)

printf 'Release packages written to %s\n' "$DIST_DIR"
