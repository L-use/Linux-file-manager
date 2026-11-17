package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"net/http"
	"path"
	"strconv"
	"strings"
)

const minThumbnailSource = 1 << 20

type thumbnail struct {
	data          []byte
	width, height int
	contentType   string
}
type thumbnailKey struct {
	name, version string
	edge          int
}
type thumbnails struct {
	cache    *previewCache
	decoding chan struct{}
}

func newThumbnails(cache *previewCache) *thumbnails {
	return &thumbnails{cache: cache, decoding: make(chan struct{}, 1)}
}

func (t *thumbnails) serve(s *fileStore, w http.ResponseWriter, r *http.Request) error {
	q := r.URL.Query()
	name := q.Get("path")
	edge, err := strconv.Atoi(q.Get("edge"))
	if err != nil || (edge != 240 && edge != 800 && edge != 1280 && edge != 1600) {
		return failure(400, "badRequest")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	ext := strings.ToLower(path.Ext(name))
	if (ext != ".jpg" && ext != ".jpeg" && ext != ".png") || info.Size() < minThumbnailSource || info.Size() > 96<<20 {
		return failure(415, "unsupported")
	}
	key := thumbnailKey{name, version(info), edge}
	result := t.cache.image(key)
	if result.data == nil {
		select {
		case t.decoding <- struct{}{}:
			defer func() { <-t.decoding }()
		case <-r.Context().Done():
			return r.Context().Err()
		default:
			if edge != 240 {
				return failure(503, "busy")
			}
			select {
			case t.decoding <- struct{}{}:
				defer func() { <-t.decoding }()
			case <-r.Context().Done():
				return r.Context().Err()
			}
		}
		result = t.cache.image(key)
		if result.data == nil {
			_, generation := t.cache.budget()
			result, err = makeThumbnail(r.Context(), f, ext == ".png", edge)
			if err != nil {
				return err
			}
			current, err := descriptorStat(f)
			if err != nil {
				return err
			}
			if !matches(current, key.version) {
				return failure(409, "changed")
			}
			t.cache.put(previewCacheEntry{key: key, image: result, size: int64(len(result.data))}, generation)
		}
	}
	w.Header().Set("Content-Type", result.contentType)
	w.Header().Set("Content-Security-Policy", "default-src 'none'; sandbox")
	w.Header().Set("X-FM-Image-Width", strconv.Itoa(result.width))
	w.Header().Set("X-FM-Image-Height", strconv.Itoa(result.height))
	w.Header().Set("Content-Length", strconv.Itoa(len(result.data)))
	_, _ = w.Write(result.data)
	return nil
}

type contextImageReader struct {
	ctx context.Context
	io.Reader
}

func (r contextImageReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.Reader.Read(p)
}

func makeThumbnail(ctx context.Context, input io.ReadSeeker, isPNG bool, edge int) (thumbnail, error) {
	var output thumbnail
	config := jpeg.DecodeConfig
	decode := jpeg.Decode
	orientation := 1
	if isPNG {
		config, decode = png.DecodeConfig, png.Decode
	} else {
		orientation = jpegOrientation(input)
		if _, err := input.Seek(0, io.SeekStart); err != nil {
			return output, err
		}
	}
	reader := contextImageReader{ctx, input}
	dim, err := config(reader)
	if err != nil || dim.Width < 1 || dim.Height < 1 || int64(dim.Width)*int64(dim.Height) > 50_000_000 ||
		max(dim.Width, dim.Height) <= edge {
		return output, failure(415, "unsupported")
	}
	if _, err := input.Seek(0, io.SeekStart); err != nil {
		return output, err
	}
	source, err := decode(reader)
	if err != nil {
		return output, failure(415, "unsupported")
	}
	w, h := dim.Width, dim.Height
	if orientation >= 5 {
		w, h = h, w
	}
	scale := float64(edge) / float64(max(w, h))
	width, height := max(1, int(math.Round(float64(w)*scale))), max(1, int(math.Round(float64(h)*scale)))
	dest := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		if err := ctx.Err(); err != nil {
			return output, err
		}
		for x := range width {
			// Take a small area sample so high-frequency detail remains legible after downscaling.
			sampleThumbnailPixel(source, dest, x, y, float64(w)/float64(width), float64(h)/float64(height), orientation)
		}
	}
	var buffer bytes.Buffer
	contentType := "image/jpeg"
	if isPNG && !dest.Opaque() {
		contentType = "image/png"
		err = (&png.Encoder{CompressionLevel: png.BestSpeed}).Encode(&buffer, dest)
	} else {
		err = jpeg.Encode(&buffer, dest, &jpeg.Options{Quality: 77})
	}
	if err != nil {
		return output, err
	}
	if buffer.Len() > 768<<10 {
		return output, failure(415, "unsupported")
	}
	return thumbnail{data: buffer.Bytes(), width: w, height: h, contentType: contentType}, nil
}

func sampleThumbnailPixel(source image.Image, dest *image.RGBA, x, y int, scaleX, scaleY float64, orientation int) {
	bounds := source.Bounds()
	var r, g, b, a uint32
	for _, fy := range [2]float64{0.25, 0.75} {
		for _, fx := range [2]float64{0.25, 0.75} {
			sx, sy := orientedPixel(int((float64(x)+fx)*scaleX), int((float64(y)+fy)*scaleY), bounds.Dx(), bounds.Dy(), orientation)
			cr, cg, cb, ca := source.At(bounds.Min.X+sx, bounds.Min.Y+sy).RGBA()
			r, g, b, a = r+cr, g+cg, b+cb, a+ca
		}
	}
	offset := dest.PixOffset(x, y)
	dest.Pix[offset] = byte(r >> 10)
	dest.Pix[offset+1] = byte(g >> 10)
	dest.Pix[offset+2] = byte(b >> 10)
	dest.Pix[offset+3] = byte(a >> 10)
}

func orientedPixel(x, y, width, height, orientation int) (int, int) {
	switch orientation {
	case 2:
		return width - 1 - x, y
	case 3:
		return width - 1 - x, height - 1 - y
	case 4:
		return x, height - 1 - y
	case 5:
		return y, x
	case 6:
		return y, height - 1 - x
	case 7:
		return width - 1 - y, height - 1 - x
	case 8:
		return width - 1 - y, x
	default:
		return x, y
	}
}

func jpegOrientation(input io.Reader) int {
	r := bufio.NewReader(io.LimitReader(input, 512<<10))
	var raw [2]byte
	if _, err := io.ReadFull(r, raw[:]); err != nil || raw != [2]byte{0xff, 0xd8} {
		return 1
	}
	for {
		marker, err := r.ReadByte()
		if err != nil || marker != 0xff {
			return 1
		}
		for marker == 0xff {
			marker, err = r.ReadByte()
			if err != nil {
				return 1
			}
		}
		if marker == 0xda || marker == 0xd9 {
			return 1
		}
		if _, err := io.ReadFull(r, raw[:]); err != nil {
			return 1
		}
		length := int(binary.BigEndian.Uint16(raw[:])) - 2
		if length < 0 {
			return 1
		}
		if marker != 0xe1 {
			if _, err := r.Discard(length); err != nil {
				return 1
			}
			continue
		}
		payload := make([]byte, length)
		if _, err := io.ReadFull(r, payload); err != nil {
			return 1
		}
		if bytes.HasPrefix(payload, []byte("Exif\x00\x00")) {
			return tiffOrientation(payload[6:])
		}
	}
}

func tiffOrientation(data []byte) int {
	if len(data) < 8 {
		return 1
	}
	var order binary.ByteOrder
	switch string(data[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 1
	}
	if order.Uint16(data[2:4]) != 42 {
		return 1
	}
	offset := int64(order.Uint32(data[4:8]))
	if offset > int64(len(data)-2) {
		return 1
	}
	entries := int(order.Uint16(data[offset : offset+2]))
	directory := data[offset+2:]
	for i := 0; i < entries && len(directory) >= 12; i++ {
		if order.Uint16(directory[:2]) == 0x112 && order.Uint16(directory[2:4]) == 3 && order.Uint32(directory[4:8]) == 1 {
			value := int(order.Uint16(directory[8:10]))
			if value >= 1 && value <= 8 {
				return value
			}
			return 1
		}
		directory = directory[12:]
	}
	return 1
}
