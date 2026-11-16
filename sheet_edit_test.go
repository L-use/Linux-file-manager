package main

import (
	"bytes"
	"encoding/xml"
	"strings"
	"testing"
)

func TestWorksheetEditPreservesFormulasAndStyles(t *testing.T) {
	const formula = `<s:c r="B1"><s:f t="array" ref="B1:B2">A1+1</s:f><s:v>3</s:v></s:c>`
	original := []byte(`<s:worksheet xmlns:s="` + spreadsheetNS + `"><s:sheetData>
<s:row r="1"><s:c r="A1" s="2"><s:v>2</s:v></s:c>` + formula + `</s:row>
<s:row r="2"><s:c r="A2" s="3"/><s:c r="B2"><s:v>3</s:v></s:c></s:row>
</s:sheetData><s:sheetCalcPr fullCalcOnLoad="0"/></s:worksheet>`)
	var changedShared bool
	total := sheetChange{Cell: "A1", Value: "15.5", Kind: "number"}
	text := sheetChange{Cell: "A2", Value: "=SUM(1,2) & 中文", Kind: "text"}
	edited, err := patchWorksheet(original, map[string]sheetChange{"A1": total, "A2": text}, &changedShared)
	if err != nil {
		t.Fatal(err)
	}
	var root struct {
		XMLName xml.Name `xml:"worksheet"`
	}
	if err := xml.Unmarshal(edited, &root); err != nil {
		t.Fatalf("invalid output XML: %v", err)
	}
	for _, want := range []string{formula, `r="A1" s="2" t="n"><v>15.5</v>`,
		`r="A2" s="3" t="inlineStr"><is><t xml:space="preserve">=SUM(1,2) &amp; 中文</t>`,
		`<s:sheetCalcPr fullCalcOnLoad="1"/>`} {
		if !strings.Contains(string(edited), want) {
			t.Errorf("expected %q in %s", want, edited)
		}
	}
	for _, cell := range []string{"B1", "B2"} {
		if _, err := patchWorksheet(original, map[string]sheetChange{cell: total}, &changedShared); err == nil {
			t.Errorf("allowed edit of formula range cell %s", cell)
		}
	}
	protected := bytes.Replace(original, []byte("</s:worksheet>"), []byte(`<s:sheetProtection sheet="1"/></s:worksheet>`), 1)
	if _, err := patchWorksheet(protected, map[string]sheetChange{"A1": total}, &changedShared); err == nil {
		t.Error("allowed edit of protected sheet")
	}
}
