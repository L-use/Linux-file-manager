//go:build bundledffmpeg

package main

import _ "embed"

//go:embed ffmpeg-bundle/ffmpeg.gz
var bundledFFmpeg []byte
