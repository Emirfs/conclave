package grants

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"io"
	"strings"
	"testing"
)

func wordDocumentText(t *testing.T, data []byte) (string, string) {
	t.Helper()
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range archive.File {
		if part.Name != "word/document.xml" {
			continue
		}
		reader, err := part.Open()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(reader)
		reader.Close()
		if err != nil {
			t.Fatal(err)
		}
		decoder := xml.NewDecoder(bytes.NewReader(raw))
		var text strings.Builder
		for {
			token, err := decoder.Token()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatalf("invalid Word XML: %v", err)
			}
			if start, ok := token.(xml.StartElement); ok && start.Name.Local == "t" {
				var value string
				if err := decoder.DecodeElement(&value, &start); err != nil {
					t.Fatal(err)
				}
				text.WriteString(value)
				text.WriteByte('\n')
			}
		}
		return text.String(), string(raw)
	}
	t.Fatal("DOCX main document missing")
	return "", ""
}
func TestWordFormatsNestedHeadingsAndPreservesLiteralIdentifiers(t *testing.T) {
	data, err := BuildWord("Sensör projesi", "# Başvuru\n\n## Yöntem\n\nMODEL_01 ve ada_yilmaz@example.com kullanılır. **Vurgulu** açıklama.\n\n### Ekler\n\n#etiket ve ####### geçersiz başlık düz metindir.\n\n| Kalem | Tutar |\n| --- | --- |\n| MODEL_01 | 1200 TL |")
	if err != nil {
		t.Fatal(err)
	}
	text, raw := wordDocumentText(t, data)
	for _, expected := range []string{"Yöntem", "Ekler", "MODEL_01", "ada_yilmaz@example.com", "#etiket", "#######", "Vurgulu", "1200 TL"} {
		if !strings.Contains(text, expected) {
			t.Fatalf("Word lost confirmed content %q", expected)
		}
	}
	if strings.Contains(text, "## Yöntem") || strings.Contains(text, "**Vurgulu**") {
		t.Fatal("formatting markers leaked into rendered section headings")
	}
	if !strings.Contains(raw, `w:val="Heading2"`) || !strings.Contains(raw, `w:val="Heading3"`) || !strings.Contains(raw, "<w:tbl>") {
		t.Fatal("Word section/table structure missing")
	}
}
func TestWordSerializationRemainsWellFormedWithXMLBoundaryCharacters(t *testing.T) {
	data, err := BuildWord("Başlık & <etiket>", "Kontrol:\x01 Türkçe & <etiket> \ufffd")
	if err != nil {
		t.Fatal(err)
	}
	_, raw := wordDocumentText(t, data)
	if strings.ContainsRune(raw, '\x01') {
		t.Fatal("XML-invalid control entered Word document")
	}
}
