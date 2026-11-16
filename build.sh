#!/bin/sh
set -eu

cd "$(dirname "$0")"
: "${GO:=go}"
out="${RELEASE_DIR:-$PWD/dist}"
stage=$(mktemp -d "$PWD/.release.XXXXXX")
trap 'rm -rf "$stage"' EXIT

for arch in amd64 arm64; do
  name="file-manager-linux-$arch"
  release="$stage/$name"
  mkdir "$release"
  CGO_ENABLED=0 GOOS=linux GOARCH="$arch" "$GO" build -mod=vendor -trimpath -ldflags='-s -w' -o "$release/filemanager" .
  ln -s filemanager "$release/file-manager"
  cp README.md LICENSE THIRD_PARTY.md "$release/"
  cp config.example.json "$release/config.json"
  : > "$release/filemanager.portable"
  for license in web/vendor/*/LICENSE* vendor/golang.org/x/*/LICENSE; do
    mkdir -p "$release/$(dirname "$license")"
    cp "$license" "$release/$license"
  done
  (
    cd "$stage"
    zip -q -yr "$name.zip" "$name"
    COPYFILE_DISABLE=1 tar -czf "$name.tar.gz" "$name/filemanager" "$name/config.json"
  )
done

zip -q -r "$stage/file-manager-source.zip" README.md LICENSE THIRD_PARTY.md config.example.json .gitignore build.sh install.sh go.mod go.sum ./*.go vendor web -x '*/.DS_Store'
cp install.sh "$stage/install.sh"

set -- file-manager-linux-amd64.zip file-manager-linux-arm64.zip file-manager-source.zip \
  file-manager-linux-amd64.tar.gz file-manager-linux-arm64.tar.gz install.sh
(
  cd "$stage"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$@" > SHA256SUMS
  else
    shasum -a 256 "$@" > SHA256SUMS
  fi
)
mkdir -p "$out"
for artifact in "$@" SHA256SUMS; do
  mv "$stage/$artifact" "$out/$artifact"
done
