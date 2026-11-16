package main

import (
	"archive/zip"
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWordEditPreservesUnaffectedRunsAndArchiveParts(t *testing.T) {
	const name = "report.docx"
	const raw = `<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body>
<w:p><w:r><w:t>Alpha</w:t></w:r><w:r><w:rPr><w:b/></w:rPr><w:t>Beta</w:t></w:r><w:r><w:t>Gamma</w:t></w:r></w:p>
<w:p><w:fldSimple w:instr="DATE"><w:r><w:t>Skip field</w:t></w:r></w:fldSimple></w:p>
<w:ins><w:p><w:r><w:t>Skip revision</w:t></w:r></w:p></w:ins>
<w:tbl><w:tr><w:tc><w:p><w:r><w:t>Cell</w:t></w:r></w:p></w:tc></w:tr></w:tbl>
</w:body></w:document>`
	var archive bytes.Buffer
	z := zip.NewWriter(&archive)
	for part, contents := range map[string]string{"word/document.xml": raw, "word/media/figure.png": "original bytes"} {
		w, err := z.Create(part)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(contents)); err != nil {
			t.Fatal(err)
		}
	}
	if err := z.Close(); err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, name), archive.Bytes(), 0640); err != nil {
		t.Fatal(err)
	}
	store, err := openFiles(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer store.root.Close()
	start, err := store.wordPreview(name)
	if err != nil || !slices.Equal(start.Paragraphs, []string{"AlphaBetaGamma", "Cell"}) {
		t.Fatalf("initial preview %#v, %v", start, err)
	}
	if err := store.saveWord(name, start.Version, []wordChange{{0, "Alpha<&>图Gamma"}, {1, ""}}); err != nil {
		t.Fatal(err)
	}
	result, err := store.wordPreview(name)
	if err != nil || !slices.Equal(result.Paragraphs, []string{"Alpha<&>图Gamma", ""}) {
		t.Fatalf("edited preview %#v, %v", result, err)
	}
	if err := store.saveWord(name, start.Version, []wordChange{{0, "stale"}}); err == nil {
		t.Fatal("stale edit was accepted")
	}
	actual, err := zip.OpenReader(filepath.Join(root, name))
	if err != nil {
		t.Fatal(err)
	}
	defer actual.Close()
	part, err := fs.ReadFile(actual, "word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	for _, preserved := range []string{`<w:t>Alpha</w:t>`, `<w:rPr><w:b/></w:rPr><w:t xml:space="preserve">&lt;&amp;&gt;图</w:t>`, `<w:t>Gamma</w:t>`, "Skip field", "Skip revision"} {
		if !strings.Contains(string(part), preserved) {
			t.Errorf("expected to find %q in %s", preserved, part)
		}
	}
	image, err := fs.ReadFile(actual, "word/media/figure.png")
	if err != nil || string(image) != "original bytes" {
		t.Fatalf("media changed: %q, %v", image, err)
	}
}
