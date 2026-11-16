package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"io/fs"
	"path"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"
)

const officeArchiveLimit = 48 << 20
const wordXMLLimit = 8 << 20
const officeEditLimit = 1 << 20

type wordRun struct {
	start, openEnd, closeStart int
	text                       string
}

type wordParagraph struct {
	runs    []wordRun
	text    string
	complex bool
}

type wordPreview struct {
	Version    string   `json:"version"`
	Paragraphs []string `json:"paragraphs"`
}

func wordNamespace(space string) bool {
	return space == "http://schemas.openxmlformats.org/wordprocessingml/2006/main" ||
		space == "http://purl.oclc.org/ooxml/wordprocessingml/main"
}

func readWordArchive(f io.ReaderAt, size int64) (*zip.Reader, *zip.File, error) {
	if size > officeArchiveLimit {
		return nil, nil, failure(413, "docLimit")
	}
	z, err := zip.NewReader(f, size)
	if err != nil {
		return nil, nil, failure(415, "readError")
	}
	if len(z.File) > 12000 {
		return nil, nil, failure(413, "docLimit")
	}
	var main *zip.File
	for _, entry := range z.File {
		if strings.HasPrefix(strings.ToLower(entry.Name), "_xmlsignatures/") {
			return nil, nil, failure(415, "wordNothingEditable")
		}
		if entry.Name == "word/document.xml" {
			if main != nil {
				return nil, nil, failure(415, "readError")
			}
			main = entry
		}
	}
	if main == nil {
		return nil, nil, failure(415, "readError")
	}
	if main.UncompressedSize64 > wordXMLLimit {
		return nil, nil, failure(413, "docLimit")
	}
	return z, main, nil
}

func readWordParagraphs(main *zip.File) ([]byte, []wordParagraph, error) {
	source, err := main.Open()
	if err != nil {
		return nil, nil, err
	}
	defer source.Close()
	raw, err := io.ReadAll(io.LimitReader(source, wordXMLLimit+1))
	if err != nil {
		return nil, nil, err
	}
	if len(raw) > wordXMLLimit {
		return nil, nil, failure(413, "docLimit")
	}

	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var paragraphs []wordParagraph
	var paragraph wordParagraph
	var run *wordRun
	var builder strings.Builder
	depth, body, current, style, blocked := 0, 0, 0, 0, 0
	for {
		offset := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			if depth != 0 {
				return nil, nil, failure(415, "readError")
			}
			return raw, paragraphs, nil
		}
		if err != nil {
			return nil, nil, failure(415, "readError")
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			inWord := wordNamespace(element.Name.Space)
			name := element.Name.Local
			if depth == 2 && inWord && name == "body" {
				body = depth
				continue
			}
			if body == 0 || blocked != 0 {
				continue
			}
			if current == 0 {
				switch {
				case inWord && name == "p":
					current = depth
					paragraph = wordParagraph{}
					builder.Reset()
				case inWord && (name == "tbl" || name == "tr" || name == "tc"):
				default:
					blocked = depth
				}
				continue
			}
			if style != 0 {
				continue
			}
			if !inWord || run != nil {
				paragraph.complex = true
				continue
			}
			switch name {
			case "pPr", "rPr":
				style = depth
			case "r", "proofErr", "bookmarkStart", "bookmarkEnd":
			case "t":
				openEnd := int(decoder.InputOffset())
				if raw[openEnd-2] != '/' {
					run = &wordRun{start: offset, openEnd: openEnd}
				}
			default:
				paragraph.complex = true
			}
		case xml.CharData:
			if run != nil {
				run.text += string(element)
			}
		case xml.Comment, xml.Directive, xml.ProcInst:
			if run != nil {
				paragraph.complex = true
			}
		case xml.EndElement:
			switch {
			case depth == body:
				body = 0
			case depth == blocked:
				blocked = 0
			case depth == current:
				if !paragraph.complex && len(paragraph.runs) > 0 {
					if len(paragraphs) == 5000 {
						return nil, nil, failure(413, "docLimit")
					}
					paragraph.text = builder.String()
					paragraphs = append(paragraphs, paragraph)
				}
				current = 0
			case depth == style:
				style = 0
			case run != nil && wordNamespace(element.Name.Space) && element.Name.Local == "t":
				run.closeStart = offset
				paragraph.runs = append(paragraph.runs, *run)
				builder.WriteString(run.text)
				run = nil
			}
			depth--
		}
	}
}

func (s *fileStore) wordPreview(name string) (wordPreview, error) {
	if !strings.EqualFold(path.Ext(name), ".docx") {
		return wordPreview{}, failure(415, "unsupported")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return wordPreview{}, err
	}
	defer f.Close()
	_, main, err := readWordArchive(f, info.Size())
	if err != nil {
		return wordPreview{}, err
	}
	_, paragraphs, err := readWordParagraphs(main)
	if err != nil {
		return wordPreview{}, err
	}
	latest, err := descriptorStat(f)
	if err != nil {
		return wordPreview{}, err
	}
	revision := version(info)
	if !matches(latest, revision) {
		return wordPreview{}, failure(409, "changed")
	}
	values := make([]string, len(paragraphs))
	for i, paragraph := range paragraphs {
		values[i] = paragraph.text
	}
	return wordPreview{revision, values}, nil
}

type wordChange struct {
	Index int    `json:"index"`
	Text  string `json:"text"`
}

var wordXMLSpace = regexp.MustCompile(`[ \t\r\n]+xml:space[ \t\r\n]*=[ \t\r\n]*("[^"]*"|'[^']*')`)

type wordReplacement struct {
	run  wordRun
	text string
}

// Keep the common prefix and suffix in their original runs. New text uses the
// formatting of the run where the replacement starts.
func wordReplacements(paragraph wordParagraph, replacement string) []wordReplacement {
	old, updated := []rune(paragraph.text), []rune(replacement)
	first := 0
	for first < len(old) && first < len(updated) && old[first] == updated[first] {
		first++
	}
	last, newLast := len(old), len(updated)
	for last > first && newLast > first && old[last-1] == updated[newLast-1] {
		last--
		newLast--
	}
	var changes []wordReplacement
	offset := 0
	insert := true
	for _, run := range paragraph.runs {
		content := []rune(run.text)
		end := offset + len(content)
		value := string(content[:max(0, min(len(content), first-offset))])
		if insert && (end > first || end == first && first == len(old)) {
			value += string(updated[first:newLast])
			insert = false
		}
		value += string(content[max(0, min(len(content), last-offset)):])
		if value != run.text {
			changes = append(changes, wordReplacement{run, value})
		}
		offset = end
	}
	return changes
}

func (s *fileStore) saveWord(name, expected string, changes []wordChange) error {
	if !strings.EqualFold(path.Ext(name), ".docx") || expected == "" || len(changes) == 0 {
		return failure(400, "badRequest")
	}
	f, info, err := s.regular(name)
	if err != nil {
		return err
	}
	defer f.Close()
	if !matches(info, expected) {
		return failure(409, "changed")
	}
	archive, main, err := readWordArchive(f, info.Size())
	if err != nil {
		return err
	}
	raw, paragraphs, err := readWordParagraphs(main)
	if err != nil {
		return err
	}
	sort.Slice(changes, func(i, j int) bool { return changes[i].Index < changes[j].Index })
	var edited bytes.Buffer
	copied, previous := 0, -1
	for _, change := range changes {
		if change.Index <= previous || change.Index >= len(paragraphs) ||
			!utf8.ValidString(change.Text) || strings.ContainsAny(change.Text, "\r\n\t") {
			return failure(400, "badRequest")
		}
		for _, r := range change.Text {
			if r < 0x20 || r == 0xfffe || r == 0xffff {
				return failure(400, "badRequest")
			}
		}
		previous = change.Index
		for _, replacement := range wordReplacements(paragraphs[change.Index], change.Text) {
			run := replacement.run
			edited.Write(raw[copied:run.start])
			open := wordXMLSpace.ReplaceAll(raw[run.start:run.openEnd-1], nil)
			edited.Write(open)
			edited.WriteString(` xml:space="preserve">`)
			if err := xml.EscapeText(&edited, []byte(replacement.text)); err != nil {
				return err
			}
			copied = run.closeStart
		}
	}
	if copied == 0 {
		return nil
	}
	edited.Write(raw[copied:])
	if edited.Len() > wordXMLLimit {
		return failure(413, "docLimit")
	}
	return s.writeOfficeArchive(name, expected, archive, map[*zip.File][]byte{main: edited.Bytes()}, info)
}

func (s *fileStore) writeOfficeArchive(name, revision string, source *zip.Reader, replacements map[*zip.File][]byte, info fs.FileInfo) error {
	var packed bytes.Buffer
	writer := zip.NewWriter(&packed)
	if err := writer.SetComment(source.Comment); err != nil {
		return err
	}
	for _, file := range source.File {
		if contents, ok := replacements[file]; ok {
			part, err := writer.Create(file.Name)
			if err != nil {
				return err
			}
			if _, err := part.Write(contents); err != nil {
				return err
			}
		} else if err := writer.Copy(file); err != nil {
			return err
		}
	}
	if err := writer.Close(); err != nil {
		return err
	}
	if packed.Len() > officeArchiveLimit {
		return failure(413, "docLimit")
	}
	return s.writeFile(name, revision, &packed, int64(packed.Len()), info)
}
