package main

import (
	"context"
	"encoding/binary"
	"errors"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path"
	"strings"
	"time"

	"golang.org/x/image/draw"
	"golang.org/x/image/tiff"
)

// A DNG is a TIFF container. Read the tagged preview image without interpreting raw sensor data.
func dngPreview(input io.ReaderAt, size int64) (io.Reader, error) {
	var header [8]byte
	if _, err := input.ReadAt(header[:], 0); err != nil {
		return nil, err
	}
	var order binary.ByteOrder
	switch string(header[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return nil, errors.New("invalid DNG byte order")
	}
	if order.Uint16(header[2:4]) != 42 {
		return nil, errors.New("invalid DNG header")
	}
	queue := []uint32{order.Uint32(header[4:8])}
	seen := make(map[uint32]bool)
	var bestStart, bestSize, bestPixels int64
	for len(queue) > 0 && len(seen) < 32 {
		offset := queue[0]
		queue = queue[1:]
		if offset < 8 || seen[offset] {
			continue
		}
		seen[offset] = true
		var countBytes [2]byte
		if _, err := input.ReadAt(countBytes[:], int64(offset)); err != nil {
			continue
		}
		count := int(order.Uint16(countBytes[:]))
		if count > 1024 {
			continue
		}
		tags := make([]byte, count*12+4)
		if _, err := input.ReadAt(tags, int64(offset)+2); err != nil {
			continue
		}
		queue = append(queue, order.Uint32(tags[count*12:]))
		var start, length, strips, stripBytes uint32
		for i := range count {
			tag := tags[i*12 : i*12+12]
			id := order.Uint16(tag[:2])
			if id != 330 && id != 513 && id != 514 && id != 273 && id != 279 {
				continue
			}
			kind, n := order.Uint16(tag[2:4]), order.Uint32(tag[4:8])
			if n == 0 || n > 16 || (kind != 3 && kind != 4 && kind != 13) {
				continue
			}
			if id != 330 && n != 1 {
				continue
			}
			width := 4
			if kind == 3 {
				width = 2
			}
			value := tag[8:12]
			if int(n)*width > 4 {
				value = make([]byte, int(n)*width)
				if _, err := input.ReadAt(value, int64(order.Uint32(tag[8:12]))); err != nil {
					continue
				}
			}
			for j := range int(n) {
				var number uint32
				if width == 2 {
					number = uint32(order.Uint16(value[j*width:]))
				} else {
					number = order.Uint32(value[j*width:])
				}
				switch id {
				case 330:
					queue = append(queue, number)
				case 513:
					start = number
				case 514:
					length = number
				case 273:
					strips = number
				case 279:
					stripBytes = number
				}
			}
		}
		for _, location := range [][2]uint32{{start, length}, {strips, stripBytes}} {
			off, n := int64(location[0]), int64(location[1])
			if off <= 0 || n <= 0 || n > 32<<20 || off+n > size {
				continue
			}
			c, err := jpeg.DecodeConfig(io.NewSectionReader(input, off, n))
			if err != nil {
				continue
			}
			pixels := int64(c.Width) * int64(c.Height)
			if pixels > bestPixels && pixels <= 50_000_000 {
				bestStart, bestSize, bestPixels = off, n, pixels
			}
		}
	}
	if bestSize == 0 {
		return nil, errors.New("DNG contains no supported JPEG preview")
	}
	return io.NewSectionReader(input, bestStart, bestSize), nil
}

func (t *thumbnails) serveRaster(s *fileStore, w http.ResponseWriter, r *http.Request, ffmpeg string) error {
	name := r.URL.Query().Get("path")
	ext := strings.ToLower(path.Ext(name))
	if ext != ".tiff" && ext != ".tif" && ext != ".dng" {
		return failure(415, "unsupported")
	}
	input, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer input.Close()
	if info.Size() > 128<<20 {
		return failure(413, "rasterLimit")
	}
	select {
	case t.decoding <- struct{}{}:
		defer func() { <-t.decoding }()
	case <-r.Context().Done():
		return r.Context().Err()
	}
	var source image.Image
	if ext == ".dng" {
		embedded, err := dngPreview(input, info.Size())
		if err != nil {
			return failure(415, "dngNoPreview")
		}
		source, err = jpeg.Decode(embedded)
		if err != nil {
			return failure(415, "dngNoPreview")
		}
	} else {
		source, err = decodeTIFF(input)
		var unsupported tiff.UnsupportedError
		if errors.As(err, &unsupported) && ffmpeg != "" {
			return compatibleTIFF(r.Context(), w, input, ffmpeg, r.URL.Query().Get("thumbnail") == "1")
		}
		if err != nil {
			var issue *problem
			if errors.As(err, &issue) {
				return err
			}
			return failure(415, "mediaError")
		}
	}
	if r.URL.Query().Get("thumbnail") == "1" {
		bounds := source.Bounds()
		scale := min(1., 240./float64(max(bounds.Dx(), bounds.Dy())))
		dest := image.NewRGBA(image.Rect(0, 0, max(1, int(float64(bounds.Dx())*scale)), max(1, int(float64(bounds.Dy())*scale))))
		draw.ApproxBiLinear.Scale(dest, dest.Bounds(), source, bounds, draw.Src, nil)
		source = dest
	}
	w.Header().Set("Content-Type", "image/png")
	_ = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(w, source)
	return nil
}

func decodeTIFF(input *os.File) (image.Image, error) {
	dim, err := tiff.DecodeConfig(input)
	if err != nil {
		return nil, err
	}
	if dim.Width < 1 || dim.Height < 1 || int64(dim.Width)*int64(dim.Height) > 50_000_000 {
		return nil, failure(413, "rasterLimit")
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	return tiff.Decode(input)
}

func compatibleTIFF(ctx context.Context, w http.ResponseWriter, input *os.File, ffmpeg string, small bool) error {
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"-hide_banner", "-loglevel", "error", "-nostdin", "-max_alloc", "134217728",
		"-protocol_whitelist", "fd,pipe", "-format_whitelist", "tiff_pipe", "-max_pixels", "50000000",
		"-threads", "1", "-fd", "3", "-i", "fd:", "-frames:v", "1"}
	if small {
		args = append(args, "-filter_threads", "1", "-vf", "scale=240:240:force_original_aspect_ratio=decrease")
	}
	args = append(args, "-c:v", "png", "-fs", "134217728", "-f", "image2pipe", "pipe:1")
	command := exec.CommandContext(ctx, ffmpeg, args...)
	command.ExtraFiles = []*os.File{input}
	data, err := command.Output()
	if err != nil || len(data) == 0 || len(data) > 128<<20 {
		return failure(415, "mediaError")
	}
	w.Header().Set("Content-Type", "image/png")
	_, _ = w.Write(data)
	return nil
}
