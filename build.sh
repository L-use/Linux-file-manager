#!/bin/sh
set -eu

cd "$(dirname "$0")"
: "${GO:=go}"
: "${VERSION:=$(cat VERSION)}"
if ! printf '%s\n' "$VERSION" | grep -Eq '^v[0-9]+\.[0-9]+\.[0-9]+$'; then
  printf 'Invalid release version: %s\n' "$VERSION" >&2
  exit 1
fi
out="${RELEASE_DIR:-$PWD/dist}"
stage=$(mktemp -d "$PWD/.release.XXXXXX")
embed="$PWD/ffmpeg-bundle"
bundle="${FFMPEG_BUNDLE_DIR:-$PWD/ffmpeg-dist}"
source="${FFMPEG_SOURCE_DIR:-$PWD/ffmpeg-sources}"
for file in "$bundle/ffmpeg-amd64.gz" "$bundle/ffmpeg-arm64.gz" "$source/ffmpeg-8.0.3.tar.xz" "$source/lame-3.100.tar.gz" "$source/x264-31e19f92f00c7003fa115047ce50978bc98c3a0d.tar.gz" "$source/zlib-1.4.tar.gz" "$source/musl-1.2.5.tar.gz" "$source/SHA256SUMS"; do
  if [ ! -f "$file" ]; then
    printf 'Missing %s. See README.md for the bundled FFmpeg build.\n' "$file" >&2
    rm -rf "$stage"
    exit 1
  fi
done
if ! mkdir "$embed"; then rm -rf "$stage"; exit 1; fi
trap 'rm -rf "$stage" "$embed"' EXIT
(
  cd "$source"
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c SHA256SUMS >/dev/null
  else shasum -a 256 -c SHA256SUMS >/dev/null
  fi
)

for arch in amd64 arm64; do
  name="file-manager-linux-$arch"
  release="$stage/$name"
  mkdir "$release"
  gzip -t "$bundle/ffmpeg-$arch.gz"
  cp "$bundle/ffmpeg-$arch.gz" "$embed/ffmpeg.gz"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -mod=vendor -tags=bundledffmpeg -trimpath -ldflags="-s -w -X main.buildVersion=$VERSION" -o "$release/filemanager" .
  ln -s filemanager "$release/file-manager"
  cp README.md LICENSE THIRD_PARTY.md FFMPEG-NOTICE.md "$release/"
  COPYFILE_DISABLE=1 tar -xOf "$source/ffmpeg-8.0.3.tar.xz" ffmpeg-8.0.3/COPYING.GPLv2 > "$release/FFMPEG-COPYING.GPLv2"
  cp config.example.json "$release/config.json"
  : > "$release/filemanager.portable"
  for license in web/vendor/*/LICENSE* $(cat GO_LICENSES); do
    mkdir -p "$release/$(dirname "$license")"
    cp "$license" "$release/$license"
  done
  (
    cd "$stage"
    zip -q -yr "$name.zip" "$name"
    COPYFILE_DISABLE=1 tar --format=ustar -czf "$name.tar.gz" "$name/filemanager" "$name/config.json" "$name/LICENSE" "$name/THIRD_PARTY.md" "$name/FFMPEG-NOTICE.md" "$name/FFMPEG-COPYING.GPLv2" "$name/vendor" "$name/web"
  )
done

zip -q -r "$stage/file-manager-source.zip" README.md LICENSE THIRD_PARTY.md FFMPEG-NOTICE.md VERSION GO_LICENSES config.example.json .gitignore build.sh build-ffmpeg.sh install.sh go.mod go.sum ./*.go vendor web -x '*/.DS_Store'
cp install.sh "$stage/install.sh"
mkdir "$stage/ffmpeg-source"
cp -R "$source"/. "$stage/ffmpeg-source/"
cp build-ffmpeg.sh FFMPEG-NOTICE.md "$stage/ffmpeg-source/"
COPYFILE_DISABLE=1 tar -C "$stage" -czf "$stage/ffmpeg-source.tar.gz" ffmpeg-source

set -- file-manager-linux-amd64.zip file-manager-linux-arm64.zip file-manager-source.zip \
  file-manager-linux-amd64.tar.gz file-manager-linux-arm64.tar.gz install.sh ffmpeg-source.tar.gz
(
  cd "$stage"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@" > SHA256SUMS
  else
    shasum -a 256 "$@" > SHA256SUMS
  fi
)
# Keep versioned local copies alongside the stable GitHub asset names.
for archive in file-manager-linux-amd64 file-manager-linux-arm64; do
  for extension in zip tar.gz; do
    versioned="$archive-$VERSION.$extension"
    ln "$stage/$archive.$extension" "$stage/$versioned"
    set -- "$@" "$versioned"
  done
done
mkdir -p "$out"
for artifact in "$@" SHA256SUMS; do
  mv -f "$stage/$artifact" "$out/$artifact"
done
