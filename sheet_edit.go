package main

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"math"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"
)

type sheetChange struct {
	Sheet string `json:"sheet"`
	Cell  string `json:"cell"`
	Value string `json:"value"`
	Kind  string `json:"kind"`
}

var cellRefPattern = regexp.MustCompile(`^[A-Z]{1,3}[1-9][0-9]{0,6}$`)
var sheetEscapePattern = regexp.MustCompile(`(?i)_x[0-9a-f]{4}_`)
var sheetRecalcAttribute = regexp.MustCompile(`[ \t\r\n]+fullCalcOnLoad[ \t\r\n]*=[ \t\r\n]*("[^"]*"|'[^']*')`)
var sharedCountAttribute = regexp.MustCompile(`[ \t\r\n]+(count|uniqueCount)[ \t\r\n]*=[ \t\r\n]*("[0-9]+"|'[0-9]+')`)

func sheetAddress(ref string) (int, int, bool) {
	if !cellRefPattern.MatchString(ref) {
		return 0, 0, false
	}
	col, i := 0, 0
	for ; ref[i] >= 'A' && ref[i] <= 'Z'; i++ {
		col = col*26 + int(ref[i]-'A') + 1
	}
	row, _ := strconv.Atoi(ref[i:])
	return col, row, col <= 16384 && row <= 1048576
}

func sheetXML(file *zip.File) ([]byte, error) {
	if file == nil {
		return nil, failure(415, "readError")
	}
	if file.UncompressedSize64 > sheetPartLimit {
		return nil, failure(413, "docLimit")
	}
	reader, err := file.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, sheetPartLimit+1))
	if err == nil && len(data) > sheetPartLimit {
		err = failure(413, "docLimit")
	}
	return data, err
}

const sheetPartLimit = 32 << 20
const spreadsheetNS = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
const strictSpreadsheetNS = "http://purl.oclc.org/ooxml/spreadsheetml/main"

func worksheetNamespace(space string) bool {
	return space == spreadsheetNS || space == strictSpreadsheetNS
}

func xmlAttribute(attrs []xml.Attr, local string) string {
	for _, attr := range attrs {
		if attr.Name.Space == "" && attr.Name.Local == local {
			return attr.Value
		}
	}
	return ""
}

func workbookParts(book, rels []byte) (map[string]string, error) {
	var workbook struct {
		XMLName xml.Name `xml:"workbook"`
		Sheets  []struct {
			Attrs []xml.Attr `xml:",any,attr"`
		} `xml:"sheets>sheet"`
	}
	var relationships struct {
		Items []struct {
			ID     string `xml:"Id,attr"`
			Target string `xml:"Target,attr"`
			Type   string `xml:"Type,attr"`
			Mode   string `xml:"TargetMode,attr"`
		} `xml:"Relationship"`
	}
	if xml.Unmarshal(book, &workbook) != nil || !worksheetNamespace(workbook.XMLName.Space) ||
		xml.Unmarshal(rels, &relationships) != nil {
		return nil, failure(415, "readError")
	}
	targets := make(map[string]string)
	for _, rel := range relationships.Items {
		if rel.Mode == "External" || !strings.HasSuffix(rel.Type, "/worksheet") || strings.ContainsAny(rel.Target, "%?#\\") {
			continue
		}
		if strings.HasPrefix(rel.Target, "/") {
			targets[rel.ID] = path.Clean(strings.TrimPrefix(rel.Target, "/"))
		} else {
			targets[rel.ID] = path.Join("xl", rel.Target)
		}
	}
	parts := make(map[string]string)
	for _, sheet := range workbook.Sheets {
		for _, attr := range sheet.Attrs {
			space := attr.Name.Space
			if attr.Name.Local == "id" && (space == "http://schemas.openxmlformats.org/officeDocument/2006/relationships" ||
				space == "http://purl.oclc.org/ooxml/officeDocument/relationships") {
				parts[xmlAttribute(sheet.Attrs, "name")] = targets[attr.Value]
			}
		}
	}
	return parts, nil
}

type sheetPatch struct {
	start, end int
	data       string
}

type cellRange struct{ left, top, right, bottom int }

func sheetRange(value string) (cellRange, bool) {
	first, last, ok := strings.Cut(value, ":")
	if !ok {
		last = first
	}
	col, row, a := sheetAddress(first)
	endCol, endRow, b := sheetAddress(last)
	return cellRange{col, row, endCol, endRow}, a && b
}

func (r cellRange) contains(col, row int) bool {
	return col >= r.left && col <= r.right && row >= r.top && row <= r.bottom
}

func sheetValue(change sheetChange) (string, string, error) {
	switch change.Kind {
	case "blank":
		if change.Value == "" {
			return "", "", nil
		}
	case "number":
		val, err := strconv.ParseFloat(change.Value, 64)
		if err == nil && !math.IsNaN(val) && !math.IsInf(val, 0) {
			return "n", "<v>" + strconv.FormatFloat(val, 'g', -1, 64) + "</v>", nil
		}
	case "boolean":
		if change.Value == "true" {
			return "b", "<v>1</v>", nil
		}
		if change.Value == "false" {
			return "b", "<v>0</v>", nil
		}
	case "text":
		if utf8.ValidString(change.Value) && len(utf16.Encode([]rune(change.Value))) <= 32767 {
			for _, r := range change.Value {
				if (r < 0x20 && r != '\t' && r != '\n' && r != '\r') || r == 0xfffe || r == 0xffff {
					return "", "", failure(400, "badRequest")
				}
			}
			value := sheetEscapePattern.ReplaceAllStringFunc(change.Value, func(s string) string { return "_x005F_" + s[1:] })
			var escaped bytes.Buffer
			if err := xml.EscapeText(&escaped, []byte(value)); err != nil {
				return "", "", err
			}
			return "inlineStr", `<is><t xml:space="preserve">` + escaped.String() + "</t></is>", nil
		}
	}
	return "", "", failure(400, "badRequest")
}

func patchWorksheet(raw []byte, changes map[string]sheetChange, changedShared *bool) ([]byte, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	var patches []sheetPatch
	var protected []cellRange
	var start int
	var cell xml.StartElement
	var change sheetChange
	depth, dataDepth, activeCell := 0, 0, 0
	var sheetNS string
	var afterData int
	var recalc sheetPatch
	for {
		offset := int(decoder.InputOffset())
		token, err := decoder.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, failure(415, "readError")
		}
		switch element := token.(type) {
		case xml.StartElement:
			depth++
			if depth == 1 {
				sheetNS = element.Name.Space
				if !worksheetNamespace(sheetNS) || element.Name.Local != "worksheet" {
					return nil, failure(415, "readError")
				}
			}
			if depth == 2 && element.Name.Space == sheetNS {
				switch element.Name.Local {
				case "sheetData":
					dataDepth = depth
				case "sheetCalcPr":
					end := int(decoder.InputOffset())
					tag := sheetRecalcAttribute.ReplaceAll(raw[offset:end], nil)
					closeTag := bytes.TrimRight(tag, "/>")
					recalc = sheetPatch{offset, end, string(closeTag) + ` fullCalcOnLoad="1"` + string(tag[len(closeTag):])}
				case "sheetProtection":
					value := xmlAttribute(element.Attr, "sheet")
					if value != "0" && value != "false" {
						return nil, failure(403, "sheetCellLocked")
					}
				}
			}
			if dataDepth != 0 && depth == dataDepth+2 && element.Name.Space == sheetNS && element.Name.Local == "c" {
				ref := xmlAttribute(element.Attr, "r")
				if found, ok := changes[ref]; ok {
					activeCell, start, cell, change = depth, offset, element.Copy(), found
				}
			}
			if dataDepth != 0 && depth == dataDepth+3 && element.Name.Space == sheetNS && element.Name.Local == "f" {
				if area, ok := sheetRange(xmlAttribute(element.Attr, "ref")); ok {
					protected = append(protected, area)
				}
			}
			if activeCell != 0 && depth == activeCell+1 &&
				(element.Name.Space != sheetNS || (element.Name.Local != "v" && element.Name.Local != "is")) {
				return nil, failure(409, "sheetCellLocked")
			}
		case xml.EndElement:
			if depth == activeCell {
				end := int(decoder.InputOffset())
				kind, body, err := sheetValue(change)
				if err != nil {
					return nil, err
				}
				before := xmlAttribute(cell.Attr, "t")
				if (before != "" && before != "s" && before != "inlineStr" && before != "n" && before != "b") ||
					xmlAttribute(cell.Attr, "cm") != "" || xmlAttribute(cell.Attr, "vm") != "" {
					return nil, failure(409, "sheetCellLocked")
				}
				value, err := encodeSheetCell(cell, sheetNS, kind, body)
				if err != nil {
					return nil, err
				}
				patches = append(patches, sheetPatch{start, end, value})
				*changedShared = *changedShared || before == "s"
				activeCell = 0
			}
			if depth == dataDepth {
				afterData = int(decoder.InputOffset())
				dataDepth = 0
			}
			depth--
		}
	}
	if len(patches) != len(changes) {
		return nil, failure(409, "sheetCellLocked")
	}
	if recalc.end == 0 {
		recalc = sheetPatch{afterData, afterData, `<sheetCalcPr xmlns="` + sheetNS + `" fullCalcOnLoad="1"/>`}
	}
	patches = append(patches, recalc)
	sort.Slice(patches, func(i, j int) bool { return patches[i].start < patches[j].start })
	for ref := range changes {
		col, row, _ := sheetAddress(ref)
		for _, area := range protected {
			if area.contains(col, row) {
				return nil, failure(409, "sheetCellLocked")
			}
		}
	}
	var edited bytes.Buffer
	start = 0
	for _, patch := range patches {
		if patch.start < start {
			return nil, failure(415, "readError")
		}
		edited.Write(raw[start:patch.start])
		edited.WriteString(patch.data)
		start = patch.end
	}
	edited.Write(raw[start:])
	if edited.Len() > sheetPartLimit {
		return nil, failure(413, "docLimit")
	}
	return edited.Bytes(), nil
}

func encodeSheetCell(cell xml.StartElement, space, kind, body string) (string, error) {
	var builder bytes.Buffer
	builder.WriteString(`<c xmlns="` + space + `"`)
	for _, attribute := range cell.Attr {
		if attribute.Name.Space == "xmlns" || attribute.Name.Local == "xmlns" || attribute.Name.Local == "t" {
			continue
		}
		if attribute.Name.Space != "" {
			return "", failure(409, "sheetCellLocked")
		}
		builder.WriteString(" " + attribute.Name.Local + `="`)
		if err := xml.EscapeText(&builder, []byte(attribute.Value)); err != nil {
			return "", err
		}
		builder.WriteByte('"')
	}
	if kind != "" {
		builder.WriteString(` t="` + kind + `"`)
	}
	builder.WriteByte('>')
	builder.WriteString(body)
	builder.WriteString("</c>")
	return builder.String(), nil
}

func (s *fileStore) saveSheet(name, expected string, changes []sheetChange) error {
	if !strings.EqualFold(path.Ext(name), ".xlsx") || expected == "" || len(changes) == 0 || len(changes) > 2000 {
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
	if info.Size() > officeArchiveLimit {
		return failure(413, "docLimit")
	}
	archive, err := zip.NewReader(f, info.Size())
	if err != nil {
		return failure(415, "readError")
	}
	if len(archive.File) > 12000 {
		return failure(413, "docLimit")
	}
	files := make(map[string]*zip.File)
	for _, file := range archive.File {
		if strings.HasPrefix(strings.ToLower(file.Name), "_xmlsignatures/") {
			return failure(403, "sheetCellLocked")
		}
		if files[file.Name] != nil {
			return failure(415, "readError")
		}
		files[file.Name] = file
	}
	book, err := sheetXML(files["xl/workbook.xml"])
	if err != nil {
		return err
	}
	rels, err := sheetXML(files["xl/_rels/workbook.xml.rels"])
	if err != nil {
		return err
	}
	parts, err := workbookParts(book, rels)
	if err != nil {
		return err
	}
	group := make(map[string]map[string]sheetChange)
	for _, change := range changes {
		part := parts[change.Sheet]
		_, _, valid := sheetAddress(change.Cell)
		if !valid || part == "" {
			return failure(400, "badRequest")
		}
		if group[part] == nil {
			group[part] = make(map[string]sheetChange)
		}
		if _, exists := group[part][change.Cell]; exists {
			return failure(400, "badRequest")
		}
		group[part][change.Cell] = change
	}
	names := make([]string, 0, len(group))
	for part := range group {
		names = append(names, part)
	}
	sort.Strings(names)
	replacements := make(map[*zip.File][]byte)
	size := 0
	var changedShared bool
	for _, part := range names {
		file := files[part]
		raw, err := sheetXML(file)
		if err != nil {
			return err
		}
		changed, err := patchWorksheet(raw, group[part], &changedShared)
		if err != nil {
			return err
		}
		size += len(changed)
		if size > 64<<20 {
			return failure(413, "docLimit")
		}
		replacements[file] = changed
	}
	if shared := files["xl/sharedStrings.xml"]; changedShared && shared != nil {
		raw, err := sheetXML(shared)
		if err != nil {
			return err
		}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		for {
			start := int(decoder.InputOffset())
			token, err := decoder.Token()
			if err != nil {
				return failure(415, "readError")
			}
			if tag, ok := token.(xml.StartElement); ok {
				if !worksheetNamespace(tag.Name.Space) || tag.Name.Local != "sst" {
					return failure(415, "readError")
				}
				end := int(decoder.InputOffset())
				tag := sharedCountAttribute.ReplaceAll(raw[start:end], nil)
				if len(tag) < end-start {
					replacements[shared] = bytes.Join([][]byte{raw[:start], tag, raw[end:]}, nil)
				}
				break
			}
		}
	}
	return s.writeOfficeArchive(name, expected, archive, replacements, info)
}
