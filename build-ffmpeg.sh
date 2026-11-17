#!/bin/sh
# Build a separate, static FFmpeg executable for the Linux release packages.
set -eu
cd "$(dirname "$0")"

if [ "${1:-}" = "--inside" ]; then
  apk add --no-cache build-base pkgconf nasm xz     x264-dev=0.164.3108-r0 lame-dev=3.100-r5 zlib-dev=1.4-r0 zlib-static=1.4-r0
  cd /tmp
  tar -xJf /sources/ffmpeg-8.0.3.tar.xz
  cd ffmpeg-8.0.3
  ./configure --disable-autodetect --disable-everything --disable-network \
    --disable-doc --disable-debug --disable-ffplay --disable-ffprobe \
    --disable-avdevice --disable-shared --enable-static --enable-gpl \
    --enable-ffmpeg --enable-libx264 --enable-libmp3lame --enable-zlib \
    --extra-ldflags=-static --pkg-config-flags=--static \
    --enable-protocol=fd,pipe \
    --enable-demuxer=mov,matroska,avi,asf,mpegps,mpegts,mpegvideo,flv,ogg,rm,mp3,wav,flac,aac,image_tiff_pipe \
    --enable-parser=h264,hevc,av1,vp8,vp9,mpegvideo,mpeg4video,mpegaudio,aac,aac_latm,ac3,flac,opus,vc1 \
    --enable-decoder=h264,hevc,av1,vp7,vp8,vp9,mpeg1video,mpeg2video,mpeg4,msmpeg4v1,msmpeg4v2,msmpeg4v3,h263,h263i,h263p,mjpeg,png,tiff,wmv1,wmv2,wmv3,vc1,rv10,rv20,rv30,rv40,theora,flv,svq1,svq3,prores,dvvideo,dirac,ffv1,huffyuv,rawvideo \
    --enable-decoder=aac,aac_latm,mp1,mp2,mp3,alac,ac3,eac3,flac,vorbis,opus,wmav1,wmav2,wmapro,wmalossless,ra_144,ra_288,cook,sipr,dca,amrnb,amrwb,pcm_*,adpcm_* \
    --enable-encoder=aac,libx264,libmp3lame,png,mjpeg \
    --enable-muxer=mp4,mp3,image2pipe \
    --enable-filter=scale,format,aformat,aresample,null,anull,trim,atrim \
    >/tmp/configure.log || { cat /tmp/configure.log; tail -100 ffbuild/config.log; exit 1; }
  make -j"${FFMPEG_JOBS:-4}" ffmpeg
  strip ffmpeg
  if readelf -l ffmpeg | grep -q INTERP; then
    printf '%s\n' 'FFmpeg is not statically linked.' >&2
    exit 1
  fi
  ./ffmpeg -hide_banner -protocols | grep -w fd >/dev/null
  gzip -n -c ffmpeg > "/out/ffmpeg-$2.gz"
  exit
fi

if [ "$#" -ne 2 ]; then
  printf 'Usage: %s SOURCE_DIRECTORY OUTPUT_DIRECTORY\n' "$0" >&2
  exit 2
fi
sources=$(cd "$1" && pwd -P)
mkdir -p "$2"
out=$(cd "$2" && pwd -P)
printf '%s  %s\n' 6136812ea6d4e68bdba27e33c2a94382711cdf4f8602ffef056ff792bd6f9818 "$sources/ffmpeg-8.0.3.tar.xz" | {
  if command -v sha256sum >/dev/null 2>&1; then sha256sum -c -
  else shasum -a 256 -c -
  fi
}
script="$PWD/build-ffmpeg.sh"
for arch in amd64 arm64; do
  docker run --rm --platform "linux/$arch" -e FFMPEG_JOBS="${FFMPEG_JOBS:-4}" \
    -v "$sources:/sources:ro" -v "$out:/out" -v "$script:/build.sh:ro" \
    alpine:3.22 sh /build.sh --inside "$arch"
done
