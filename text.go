package main

import (
	"bytes"
	"io"
	"io/fs"
	"os"
	"strings"
	"unicode/utf8"
)

const textByteLimit = 512 << 10
const textLineLimit = 12000
const utf8BOM = "\uFEFF"

type textPreview struct {
	Text      string `json:"text"`
	Bytes     int    `json:"bytes"`
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
	if truncated {
		n := len(raw)
		for i := n - 1; i >= 0 && i >= n-4; i-- {
			if utf8.RuneStart(raw[i]) {
				if !utf8.FullRune(raw[i:]) {
					raw = raw[:i]
				}
				break
			}
		}
	}
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
