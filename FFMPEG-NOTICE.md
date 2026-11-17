# Bundled FFmpeg

The Linux releases contain FFmpeg 8.0.3 as a separate executable. File Manager starts it as a child process for compatible playback and previews. The FFmpeg executable is built with GPL support and is distributed under GPL version 2 or later. File Manager's own source is distributed under its MIT license.

The corresponding source bundle is named `ffmpeg-source.tar.gz` and is provided alongside the Linux downloads. It includes the FFmpeg source archive, the build script with the configure options, and the sources and build records for x264, LAME, zlib, and musl. The upstream license texts are in those source archives. A copy of the FFmpeg GPL text is also included in the Linux package as `FFMPEG-COPYING.GPLv2`.

The build uses Alpine Linux 3.22 and these library packages:

| Component | Version |
| --- | --- |
| FFmpeg | 8.0.3 |
| x264 | 0.164.3108-r0 |
| LAME | 3.100-r5 |
| zlib | 1.4-r0 |
| musl | 1.2.5-r13 |

To rebuild the binaries, extract the source bundle and run `sh build-ffmpeg.sh /path/to/ffmpeg-source /path/to/ffmpeg-dist` from the project or the extracted source bundle on a machine with Docker available.

Upstream projects: [FFmpeg](https://ffmpeg.org/) · [x264](https://www.videolan.org/developers/x264.html) · [LAME](https://lame.sourceforge.io/) · [zlib](https://zlib.net/).
