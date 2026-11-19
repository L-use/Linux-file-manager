#!/bin/sh
# Build self-contained macOS FFmpeg; only Apple system libraries remain dynamic.
set -eu
cd "$(dirname "$0")"
base=$PWD
out="$base/ffmpeg-dist"
work=$(mktemp -d /tmp/fm-macos-ffmpeg.XXXXXXXX)
# Retain build logs under the temporary work directory for diagnosis.
for arch in arm64 amd64; do
 cpu=$arch; [ "$arch" != amd64 ] || cpu=x86_64
 target="$work/$arch"; mkdir -p "$target/src" "$target/prefix" "$out"
 prefix="$target/prefix"
 tar -xf "$base/ffmpeg-sources/x264-31e19f92f00c7003fa115047ce50978bc98c3a0d.tar.gz" -C "$target/src"
 x264=$(find "$target/src" -mindepth 1 -maxdepth 1 -type d)
 (cd "$x264"; CC="clang -arch $cpu -mmacosx-version-min=12.0" ./configure --host="$cpu-apple-darwin" --prefix="$prefix" --enable-static --disable-cli --disable-asm; make -j4; make install)
 tar -xf "$base/ffmpeg-sources/lame-3.100.tar.gz" -C "$target/src"
 (cd "$target/src/lame-3.100"; CC="clang -arch $cpu -mmacosx-version-min=12.0" ./configure --host="$( [ "$arch" = arm64 ] && echo arm || echo x86_64 )-apple-darwin" --prefix="$prefix" --disable-shared --enable-static --disable-frontend --disable-dependency-tracking; make -j4; make install)
 cat > "$target/pkg-config" <<PC
#!/bin/sh
case "\$*" in
 *--version*) echo 1.8.0;;
 *--modversion*) echo 0.164.3108;;
 *--cflags*) echo -I$prefix/include;;
 *--libs*) echo -L$prefix/lib -lx264 -lm -lpthread;;
 *x264*) exit 0;;
 *) exit 1;;
esac
PC
 chmod +x "$target/pkg-config"
 tar -xf "$base/ffmpeg-sources/ffmpeg-8.0.3.tar.xz" -C "$target/src"
 (cd "$target/src/ffmpeg-8.0.3"; ./configure --cc="clang -arch $cpu -mmacosx-version-min=12.0" --arch="$cpu" --target-os=darwin --enable-cross-compile --pkg-config="$target/pkg-config" --extra-cflags="-I$prefix/include" --extra-ldflags="-L$prefix/lib" --disable-autodetect --disable-doc --disable-debug --disable-ffplay --disable-ffprobe --disable-network --disable-avdevice --disable-shared --enable-static --enable-gpl --enable-libx264 --enable-libmp3lame --enable-zlib --disable-x86asm; make -j4 ffmpeg; strip ffmpeg; gzip -n -c ffmpeg > "$out/ffmpeg-darwin-$arch.gz")
done

rm -rf "$work"
