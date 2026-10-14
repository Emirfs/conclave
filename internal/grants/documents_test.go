package grants

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"io"
	"strings"
	"testing"
)

func TestExtractDocumentsRejectsUnsafeAndUnsupportedInputs(t *testing.T) {
	results := ExtractDocuments([]DocumentInput{{Name: "../application.pdf", ContentBase64: base64.StdEncoding.EncodeToString([]byte("%PDF"))}, {Name: "application.doc", ContentBase64: base64.StdEncoding.EncodeToString([]byte("text"))}, {Name: "application.pdf", ContentBase64: "%%%"}})
	for i, result := range results {
		if result.Error == "" {
			t.Fatalf("result %d was not rejected: %#v", i, result)
		}
	}
}
func TestExtractDocumentsReadsDOCXParagraphsAndTables(t *testing.T) {
	data := zipFixture(t, map[string]string{"[Content_Types].xml": `<Types/>`, "word/document.xml": `<w:document xmlns:w="urn:w"><w:body><w:p><w:r><w:t>Başvuru</w:t></w:r></w:p><w:tbl><w:tr><w:tc><w:p><w:r><w:t>Başlık</w:t></w:r></w:p></w:tc><w:tc><w:p><w:r><w:t>Değer</w:t></w:r></w:p></w:tc></w:tr></w:tbl></w:body></w:document>`})
	result := ExtractDocuments([]DocumentInput{{Name: "call.docx", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	for _, v := range []string{"Başvuru", "Başlık", "Değer"} {
		if !strings.Contains(result.Text, v) {
			t.Fatalf("missing %q from %q", v, result.Text)
		}
	}
}
func TestBuildWordProducesReadableDOCX(t *testing.T) {
	content, err := BuildWord("Çağrı", "# Amaç\nMetin **önemli** [resmî çağrı](https://example.org/call)\n\n- İlk\n- İkinci\n\n| Alan | Değer |\n| --- | --- |\n| Bütçe | 100 € |")
	if err != nil {
		t.Fatal(err)
	}
	files := openZipFixture(t, content)
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml", "word/styles.xml", "word/_rels/document.xml.rels"} {
		if files[name] == "" {
			t.Errorf("missing DOCX part %s", name)
		}
	}
	for _, v := range []string{"Çağrı", "Amaç", "önemli", "İlk", "İkinci", "Bütçe", "100 €", "w:hyperlink"} {
		if !strings.Contains(files["word/document.xml"], v) {
			t.Errorf("document lacks %q", v)
		}
	}
	if !strings.Contains(files["word/_rels/document.xml.rels"], "https://example.org/call") {
		t.Fatal("hyperlink relationship missing")
	}
}
func TestExtractDocumentsKeepsFormulaCachedValueAndWarns(t *testing.T) {
	data := xlsxFixture(t, `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><f>SUM(1,2)</f><v>3</v></c></row></sheetData></worksheet>`)
	result := ExtractDocuments([]DocumentInput{{Name: "budget.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !strings.Contains(result.Text, "3") {
		t.Fatalf("cached result absent: %q", result.Text)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "A1") || !strings.Contains(result.Warnings[0], "SUM(1,2)") || !strings.Contains(result.Warnings[0], "freshness is unknown") {
		t.Fatalf("formula review warning missing: %#v", result.Warnings)
	}
}
func TestExtractDocumentsReportsMissingFormulaCache(t *testing.T) {
	data := xlsxFixture(t, `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><f>SUM(1,2)</f><v></v></c></row></sheetData></worksheet>`)
	result := ExtractDocuments([]DocumentInput{{Name: "budget.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !strings.Contains(result.Text, "[unresolved formula: SUM(1,2)]") {
		t.Fatalf("missing unresolved marker: %q", result.Text)
	}
	if len(result.Warnings) == 0 || !strings.Contains(result.Warnings[0], "A1") || !strings.Contains(result.Warnings[0], "no cached value") {
		t.Fatalf("warning missing: %#v", result.Warnings)
	}
}
func TestExtractDocumentsPreservesCorruptDocumentError(t *testing.T) {
	result := ExtractDocuments([]DocumentInput{{Name: "broken.docx", ContentBase64: base64.StdEncoding.EncodeToString([]byte("not a zip"))}})[0]
	if result.Error == "" {
		t.Fatalf("corrupt document error was lost: %#v", result)
	}
}
func TestExtractDocumentsAcceptsUppercaseExtensions(t *testing.T) {
	data := zipFixture(t, map[string]string{"[Content_Types].xml": `<Types/>`, "word/document.xml": `<w:document xmlns:w="urn:w"><w:body><w:p><w:r><w:t>Başvuru</w:t></w:r></w:p></w:body></w:document>`})
	result := ExtractDocuments([]DocumentInput{{Name: "CALL.DOCX", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if result.Error != "" || result.Kind != "docx" || !strings.Contains(result.Text, "Başvuru") {
		t.Fatalf("uppercase extension rejected: %#v", result)
	}
}
func TestExtractDocumentsRejectsXLSXExternalRelationships(t *testing.T) {
	data := xlsxFixture(t, `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData/></worksheet>`)
	files := openZipFixture(t, data)
	files["xl/worksheets/_rels/sheet1.xml.rels"] = `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="external" Target="https://example.org/book.xlsx" TargetMode="External"/></Relationships>`
	data = zipFixture(t, files)
	result := ExtractDocuments([]DocumentInput{{Name: "budget.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if !strings.Contains(result.Error, "external relationships") {
		t.Fatalf("external relationship not rejected: %#v", result)
	}
}
func TestExtractDocumentsMapsWorkbookSheetRelationships(t *testing.T) {
	files := map[string]string{
		"[Content_Types].xml":        `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet10.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/><Override PartName="/xl/worksheets/sheet2.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`,
		"_rels/.rels":                `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`,
		"xl/workbook.xml":            `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Second" sheetId="1" r:id="rId2"/><sheet name="First" sheetId="2" r:id="rId1"/></sheets></workbook>`,
		"xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet2.xml"/><Relationship Id="rId2" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet10.xml"/></Relationships>`,
		"xl/worksheets/sheet10.xml":  `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><f>SUM(1,2)</f><v></v></c></row></sheetData></worksheet>`,
		"xl/worksheets/sheet2.xml":   `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1" t="str"><v>ordinary</v></c></row></sheetData></worksheet>`,
	}
	result := ExtractDocuments([]DocumentInput{{Name: "ordered.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(zipFixture(t, files))}})[0]
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "Second!A1") {
		t.Fatalf("formula associated with wrong sheet: %#v", result.Warnings)
	}
	if strings.Contains(strings.Join(result.Warnings, "\n"), "First!A1") {
		t.Fatalf("ordinary cell misidentified as formula: %#v", result.Warnings)
	}
}

func TestExtractDocumentsBoundsRepeatedLargeXLSXValues(t *testing.T) {
	value := strings.Repeat("x", 200_000)
	xmlSheet := `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1">` +
		`<c r="A1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`<c r="B1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`<c r="C1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`<c r="D1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`<c r="E1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`<c r="F1" t="inlineStr"><is><t>` + value + `</t></is></c>` +
		`</row></sheetData></worksheet>`
	result := ExtractDocuments([]DocumentInput{{Name: "large.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(xlsxFixture(t, xmlSheet))}})[0]
	if !strings.Contains(result.Error, "text exceeds") {
		t.Fatalf("expected bounded text error, got %#v", result)
	}
	if len(result.Text) != 0 {
		t.Fatalf("oversized extraction leaked partial text (%d bytes)", len(result.Text))
	}
}

func TestExtractDocumentsWarnsForSharedFormulaWithoutCachedValue(t *testing.T) {
	data := xlsxFixture(t, `<worksheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main"><sheetData><row r="1"><c r="A1"><f t="shared" si="0" ref="A1:A2"/><v>2</v></c><c r="A2"><f t="shared" si="0"/><v></v></c></row></sheetData></worksheet>`)
	result := ExtractDocuments([]DocumentInput{{Name: "shared.xlsx", ContentBase64: base64.StdEncoding.EncodeToString(data)}})[0]
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	if !strings.Contains(strings.Join(result.Warnings, "\n"), "A2") || !strings.Contains(result.Text, "A2 [unresolved formula:") {
		t.Fatalf("shared formula without cache was omitted: %#v", result)
	}
}
func zipFixture(t *testing.T, entries map[string]string) []byte {
	t.Helper()
	var out bytes.Buffer
	w := zip.NewWriter(&out)
	for name, content := range entries {
		f, err := w.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = f.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}
func openZipFixture(t *testing.T, data []byte) map[string]string {
	t.Helper()
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]string{}
	for _, f := range z.File {
		r, e := f.Open()
		if e != nil {
			t.Fatal(e)
		}
		b, e := io.ReadAll(r)
		_ = r.Close()
		if e != nil {
			t.Fatal(e)
		}
		files[f.Name] = string(b)
	}
	return files
}
func xlsxFixture(t *testing.T, sheet string) []byte {
	return zipFixture(t, map[string]string{"[Content_Types].xml": `<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/><Override PartName="/xl/worksheets/sheet1.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/></Types>`, "_rels/.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`, "xl/workbook.xml": `<workbook xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets><sheet name="Budget" sheetId="1" r:id="rId1"/></sheets></workbook>`, "xl/_rels/workbook.xml.rels": `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet1.xml"/></Relationships>`, "xl/worksheets/sheet1.xml": sheet})
}
