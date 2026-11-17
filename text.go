package main

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"

	encodingpkg "golang.org/x/text/encoding"
	"golang.org/x/text/encoding/simplifiedchinese"
	"golang.org/x/text/encoding/unicode"
	"golang.org/x/text/transform"
)

const textByteLimit = 512 << 10
const textLineLimit = 12000
const utf8BOM = "\uFEFF"

type textPreview struct {
	Text  string `json:"text"`
	Bytes int    `json:"bytes"
	"errors"`
	Total     int64  `json:"total"`
	Truncated bool   `json:"truncated"`
	Encoding  string `json:"encoding"`
	Version   string `json:"version"`
	BOM       bool   `json:"bom"`
}

func readableText(data []byte) bool {
	return utf8.Valid(data) && bytes.IndexByte(data, 0) < 0
}

func textPrefix(data []byte) []byte {
	lines := 0
	for i, b := range data {
		isEnd := b == '\n' || (b == '\r' && (i+1 == len(data) || data[i+1] != '\n'))
		if isEnd {
			lines++
			if lines == textLineLimit {
				return data[:i+1]
			}
		}
	}
	return data
}

func readText(f *os.File, info fs.FileInfo) (textPreview, error) {
	raw, err := io.ReadAll(io.LimitReader(f, textByteLimit))
	if err != nil {
		return textPreview{}, err
	}
	latest, err := descriptorStat(f)
	if err != nil {
		return textPreview{}, err
	}
	originalVersion := version(info)
	if !matches(latest, originalVersion) {
		return textPreview{}, failure(409, "changed")
	}
	raw = textPrefix(raw)
	truncated := int64(len(raw)) < info.Size()
	raw = completeUTF8(raw, truncated)
	if !readableText(raw) {
		return textPreview{}, failure(415, "encoding")
	}
	value := string(raw)
	return textPreview{
		Text: strings.TrimPrefix(value, utf8BOM), Bytes: len(raw), Total: info.Size(),
		Truncated: truncated, Encoding: "UTF-8", Version: originalVersion, BOM: strings.HasPrefix(value, utf8BOM),
	}, nil
}

func (s *fileStore) saveText(name, expected string, data []byte) error {
	if expected == "" {
		return failure(400, "badRequest")
	}
	if len(data) > textByteLimit || len(textPrefix(data)) != len(data) {
		return failure(413, "editLimit")
	}
	if !readableText(data) {
		return failure(415, "encoding")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if !matches(info, expected) {
		return failure(409, "changed")
	}
	original, err := readText(f, info)
	if err != nil {
		return err
	}
	if original.Truncated {
		return failure(413, "editLimit")
	}
	return s.writeFile(name, expected, bytes.NewReader(data), int64(len(data)), info)
}

func readTextEncoding(f *os.File, info fs.FileInfo, encoding string) (textPreview, error) {
	if encoding == "utf-8" {
		return readText(f, info)
	}
	if encoding != "" && encoding != "auto" && encoding != "utf-16le" && encoding != "utf-16be" && encoding != "gb18030" {
		return textPreview{}, failure(400, "badRequest")
	}
	raw, err := io.ReadAll(io.LimitReader(f, textByteLimit+4))
	if err != nil {
		return textPreview{}, err
	}
	latest, err := descriptorStat(f)
	if err != nil {
		return textPreview{}, err
	}
	revision := version(info)
	if !matches(latest, revision) {
		return textPreview{}, failure(409, "changed")
	}
	if encoding == "" || encoding == "auto" {
		switch {
		case bytes.HasPrefix(raw, []byte{0xff, 0xfe}):
			encoding = "utf-16le"
		case bytes.HasPrefix(raw, []byte{0xfe, 0xff}):
			encoding = "utf-16be"
		case bytes.HasPrefix(raw, []byte{0xef, 0xbb, 0xbf}) || readableText(completeUTF8(raw, int64(len(raw)) < info.Size())):
			if _, err := f.Seek(0, io.SeekStart); err != nil {
				return textPreview{}, err
			}
			return readText(f, info)
		default:
			encoding = likelyUTF16(raw)
			if encoding == "" {
				encoding = "gb18030"
			}
		}
	}
	data := raw[:min(len(raw), textByteLimit)]
	if len(raw) > textByteLimit {
		// A bounded prefix may end in the middle of a multibyte sequence.
		raw = data
	}
	var decoder *encodingpkg.Decoder
	label := strings.ToUpper(encoding)
	switch encoding {
	case "utf-16le", "utf-16be":
		order := unicode.LittleEndian
		if encoding == "utf-16be" {
			order = unicode.BigEndian
		}
		decoder = unicode.UTF16(order, unicode.IgnoreBOM).NewDecoder()
	case "gb18030":
		decoder = simplifiedchinese.GB18030.NewDecoder()
	}
	bounded := int64(len(raw)) < info.Size()
	decoded := make([]byte, len(raw)*3+4)
	n, consumed, err := decoder.Transform(decoded, raw, !bounded)
	if err != nil && !(bounded && errors.Is(err, transform.ErrShortSrc)) {
		return textPreview{}, failure(415, "encoding")
	}
	decoded = decoded[:n]
	if !readableText(decoded) || bytes.Contains(decoded, []byte("\ufffd")) {
		return textPreview{}, failure(415, "encoding")
	}
	prefix := textPrefix(decoded)
	value := string(prefix)
	return textPreview{Text: strings.TrimPrefix(value, utf8BOM), Bytes: consumed, Total: info.Size(),
		Truncated: bounded || len(prefix) < len(decoded), Encoding: label, Version: revision, BOM: strings.HasPrefix(value, utf8BOM)}, nil
}

func completeUTF8(data []byte, truncated bool) []byte {
	if truncated {
		for i := len(data) - 1; i >= 0 && i >= len(data)-4; i-- {
			if utf8.RuneStart(data[i]) {
				if !utf8.FullRune(data[i:]) {
					return data[:i]
				}
				break
			}
		}
	}
	return data
}

func likelyUTF16(data []byte) string {
	sample := data[:min(len(data), 512)]
	pairs := len(sample) / 2
	if pairs < 4 {
		return ""
	}
	var even, odd int
	for i := range pairs {
		if sample[i*2] == 0 {
			even++
		}
		if sample[i*2+1] == 0 {
			odd++
		}
	}
	if even*3 >= pairs && odd*16 < pairs {
		return "utf-16be"
	}
	if odd*3 >= pairs && even*16 < pairs {
		return "utf-16le"
	}
	return ""
}
