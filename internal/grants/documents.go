package grants

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/ledongthuc/pdf"
	"github.com/xuri/excelize/v2"
)

const (
	maxDocumentBatch = 10
	maxDocumentBytes = 20 << 20
	maxTotalBytes    = 40 << 20
	maxDocumentText  = 1 << 20
	maxTotalText     = 4 << 20
	maxZipExpanded   = 64 << 20
	maxZipEntries    = 4096
)

var validDocumentName = regexp.MustCompile(`^[^/\\\x00-\x1f]+\.(?i:pdf|docx|xlsx)$`)

// ExtractDocuments extracts text from a bounded batch of untrusted documents.
func ExtractDocuments(inputs []DocumentInput) []ImportedDocument {
	out := make([]ImportedDocument, len(inputs))
	limit := len(inputs)
	if limit > maxDocumentBatch {
		limit = maxDocumentBatch
		for i := limit; i < len(inputs); i++ {
			out[i] = ImportedDocument{Name: inputs[i].Name, Error: "Batch limit exceeded (maximum 10 documents)."}
		}
	}
	remainingBytes, remainingText := maxTotalBytes, maxTotalText
	for i, input := range inputs[:limit] {
		doc := ImportedDocument{Name: input.Name}
		if !validDocumentName.MatchString(input.Name) || filepath.Base(input.Name) != input.Name {
			doc.Error = "Filename must be a bare relative .pdf, .docx, or .xlsx filename."
			out[i] = doc
			continue
		}
		switch strings.ToLower(filepath.Ext(input.Name)) {
		case ".pdf":
			doc.Kind = "pdf"
		case ".docx":
			doc.Kind = "docx"
		case ".xlsx":
			doc.Kind = "xlsx"
		}
		if len(input.ContentBase64) > base64.StdEncoding.EncodedLen(maxDocumentBytes)+4 {
			doc.Error = "Document exceeds the 20 MiB limit."
			out[i] = doc
			continue
		}
		data, err := base64.StdEncoding.DecodeString(input.ContentBase64)
		if err != nil {
			doc.Error = "Document content is not valid base64."
			out[i] = doc
			continue
		}
		if len(data) > maxDocumentBytes {
			doc.Error = "Document exceeds the 20 MiB limit."
			out[i] = doc
			continue
		}
		if len(data) > remainingBytes {
			doc.Error = "Batch exceeds the 40 MiB decoded-input limit."
			out[i] = doc
			continue
		}
		remainingBytes -= len(data)
		var text string
		switch doc.Kind {
		case "pdf":
			text, doc.Error = extractPDF(data)
		case "docx":
			text, doc.Error = extractDOCX(data)
		case "xlsx":
			text, doc.Warnings, doc.Error = extractXLSX(data)
		default:
			doc.Error = "Unsupported document format."
		}
		if doc.Error == "" && strings.TrimSpace(text) == "" && doc.Kind == "pdf" {
			doc.Error = "PDF contains no extractable text (it may be scanned)."
		}
		if doc.Error == "" && !utf8.ValidString(text) {
			doc.Error = "Document contains invalid text encoding."
		}
		if doc.Error == "" && (len(text) > maxDocumentText || len(text) > remainingText) {
			doc.Error = "Extracted text exceeds the configured text limit."
		}
		if doc.Error == "" {
			doc.Text = text
			remainingText -= len(text)
		}
		out[i] = doc
	}
	for index := range out {
		if out[index].Warnings == nil {
			out[index].Warnings = []string{}
		}
	}
	return out
}

func extractPDF(data []byte) (string, string) {
	r, err := pdf.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return "", "Invalid or encrypted PDF: " + err.Error()
	}
	pages := r.NumPage()
	if pages < 1 || pages > 1000 {
		return "", "PDF page count is invalid or exceeds 1000."
	}
	var b strings.Builder
	for i := 1; i <= pages; i++ {
		text, err := r.Page(i).GetPlainText(nil)
		if err != nil {
			return "", "Unable to extract PDF text: " + err.Error()
		}
		if b.Len()+len(text)+1 > maxDocumentText {
			return "", "Extracted PDF text exceeds the 1 MiB limit."
		}
		b.WriteString(text)
		b.WriteByte('\n')
	}
	return b.String(), ""
}

func extractDOCX(data []byte) (string, string) {
	files, err := openBoundedZip(data)
	if err != nil {
		return "", "Invalid DOCX: " + err.Error()
	}
	if _, ok := files["[Content_Types].xml"]; !ok {
		return "", "Invalid DOCX: missing content types."
	}
	main, ok := files["word/document.xml"]
	if !ok {
		return "", "Invalid DOCX: missing main document."
	}
	text, err := extractWordXML(main)
	if err != nil {
		return "", "Invalid DOCX XML: " + err.Error()
	}
	var b strings.Builder
	b.WriteString(text)
	names := make([]string, 0)
	for name := range files {
		if name != "word/document.xml" && (strings.HasPrefix(name, "word/header") || strings.HasPrefix(name, "word/footer") || strings.HasPrefix(name, "word/footnotes") || strings.HasPrefix(name, "word/endnotes")) {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		part, err := extractWordXML(files[name])
		if err != nil {
			return "", "Invalid DOCX XML: " + err.Error()
		}
		if b.Len()+len(part) > maxDocumentText {
			return "", "Extracted DOCX text exceeds the 1 MiB limit."
		}
		b.WriteString(part)
	}
	return strings.TrimSpace(b.String()), ""
}
func extractWordXML(data []byte) (string, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var b strings.Builder
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "t":
				var value string
				if err := dec.DecodeElement(&value, &t); err != nil {
					return "", err
				}
				if b.Len()+len(value) > maxDocumentText {
					return "", errors.New("DOCX text exceeds limit")
				}
				b.WriteString(value)
			case "tab":
				if b.Len()+1 > maxDocumentText {
					return "", errors.New("DOCX text exceeds limit")
				}
				b.WriteByte('\t')
			}
		case xml.EndElement:
			if t.Name.Local == "p" || t.Name.Local == "tr" {
				if b.Len()+1 > maxDocumentText {
					return "", errors.New("DOCX text exceeds limit")
				}
				b.WriteByte('\n')
			}
			if t.Name.Local == "tc" {
				if b.Len()+1 > maxDocumentText {
					return "", errors.New("DOCX text exceeds limit")
				}
				b.WriteByte('\t')
			}
		}
	}
	return b.String(), nil
}

type worksheetFormulaXML struct {
	SheetData struct {
		Rows []struct {
			Cells []struct {
				Ref     string `xml:"r,attr"`
				Formula *struct {
					Text string `xml:",chardata"`
					Type string `xml:"t,attr"`
				} `xml:"f"`
				Value string `xml:"v"`
			} `xml:"c"`
		} `xml:"row"`
	} `xml:"sheetData"`
}
type workbookSheetsXML struct {
	Sheets []struct {
		Name string `xml:"name,attr"`
		ID   string `xml:"id,attr"`
	} `xml:"sheets>sheet"`
}
type workbookRelsXML struct {
	Relationships []struct {
		ID     string `xml:"Id,attr"`
		Target string `xml:"Target,attr"`
		Mode   string `xml:"TargetMode,attr"`
	} `xml:"Relationship"`
}

func worksheetParts(files map[string][]byte) (map[string]string, error) {
	var workbook workbookSheetsXML
	var rels workbookRelsXML
	if err := xml.Unmarshal(files["xl/workbook.xml"], &workbook); err != nil {
		return nil, err
	}
	if err := xml.Unmarshal(files["xl/_rels/workbook.xml.rels"], &rels); err != nil {
		return nil, err
	}
	targets := make(map[string]string, len(rels.Relationships))
	for _, rel := range rels.Relationships {
		if strings.EqualFold(rel.Mode, "External") {
			return nil, errors.New("external relationships are not supported")
		}
		target := filepath.ToSlash(filepath.Clean(filepath.Join("xl", rel.Target)))
		if strings.HasPrefix(target, "../") || strings.HasPrefix(target, "/") {
			return nil, errors.New("unsafe workbook relationship")
		}
		targets[rel.ID] = target
	}
	out := make(map[string]string, len(workbook.Sheets))
	for _, sheet := range workbook.Sheets {
		rid := sheet.ID
		if i := strings.Index(rid, "}"); i >= 0 {
			rid = rid[i+1:]
		}
		target, ok := targets[rid]
		if !ok {
			return nil, fmt.Errorf("worksheet relationship missing for %s", sheet.Name)
		}
		out[sheet.Name] = target
	}
	return out, nil
}

func extractXLSX(data []byte) (string, []string, string) {
	files, err := openBoundedZip(data)
	if err != nil {
		return "", nil, "Invalid XLSX: " + err.Error()
	}
	for name := range files {
		if strings.HasSuffix(strings.ToLower(name), "vbaproject.bin") {
			return "", nil, "Macro-enabled workbooks are not supported."
		}
	}
	for name, contents := range files {
		if strings.HasSuffix(name, ".rels") {
			var rels workbookRelsXML
			if xml.Unmarshal(contents, &rels) != nil {
				return "", nil, "Invalid XLSX relationships."
			}
			for _, rel := range rels.Relationships {
				if strings.EqualFold(rel.Mode, "External") {
					return "", nil, "Workbooks with external relationships are not supported."
				}
			}
		}
	}
	parts, err := worksheetParts(files)
	if err != nil {
		return "", nil, "Invalid XLSX workbook relationships: " + err.Error()
	}
	f, err := excelize.OpenReader(bytes.NewReader(data), excelize.Options{UnzipSizeLimit: maxZipExpanded})
	if err != nil {
		return "", nil, "Invalid or encrypted XLSX: " + err.Error()
	}
	defer f.Close()
	var b strings.Builder
	var warnings []string
	warningBytes := 0
	appendText := func(value string) bool {
		if len(value) > maxDocumentText-b.Len()-warningBytes {
			return false
		}
		b.WriteString(value)
		return true
	}
	sheets := f.GetSheetList()
	for _, sheet := range sheets {
		if !appendText("[Sheet: " + sheet + "]\n") {
			return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
		}
		rows, err := f.GetRows(sheet, excelize.Options{RawCellValue: false})
		if err != nil {
			return "", nil, "Unable to read XLSX sheet: " + err.Error()
		}
		formulaCells := map[string]bool{}
		for rowIndex, row := range rows {
			for colIndex, cell := range row {
				ref, err := excelize.CoordinatesToCellName(colIndex+1, rowIndex+1)
				if err != nil {
					return "", nil, "Invalid XLSX cell reference."
				}
				formula, err := f.GetCellFormula(sheet, ref)
				if err != nil {
					return "", nil, "Unable to inspect XLSX formula: " + err.Error()
				}
				if formula != "" {
					formulaCells[ref] = true
					if len(sheet)+len(ref)+len(formula)+180 > maxDocumentText-b.Len() {
						return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
					}
					cached, err := f.GetCellValue(sheet, ref, excelize.Options{RawCellValue: true})
					if err != nil {
						return "", nil, "Unable to read cached XLSX formula value: " + err.Error()
					}
					if cached == "" {
						marker := "[unresolved formula: " + formula + "]"
						warning := fmt.Sprintf("Formula cell %s!%s (%s) has no cached value. This cell is unresolved; enter or confirm its value.", sheet, ref, formula)
						if len(marker)+len(warning) > maxDocumentText-b.Len() {
							return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
						}
						if len(warning) > maxDocumentText-b.Len()-warningBytes {
							return "", nil, "Extracted XLSX text and warnings exceed the 1 MiB limit."
						}
						warningBytes += len(warning)
						warnings = append(warnings, warning)
						cell = marker
					} else {
						warning := fmt.Sprintf("Formula cell %s!%s (%s) uses cached value %q; cache freshness is unknown. Review or correct this value.", sheet, ref, formula, cached)
						if len(warning)+len(cached) > maxDocumentText-b.Len() {
							return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
						}
						if len(warning) > maxDocumentText-b.Len()-warningBytes {
							return "", nil, "Extracted XLSX text and warnings exceed the 1 MiB limit."
						}
						warningBytes += len(warning)
						warnings = append(warnings, warning)
						cell = cached
					}
				}
				if colIndex > 0 && !appendText("\t") {
					return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
				}
				if !appendText(cell) {
					return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
				}
			}
			if !appendText("\n") {
				return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
			}
		}
		rawData, ok := files[parts[sheet]]
		if !ok {
			return "", nil, "XLSX worksheet part is missing: " + parts[sheet]
		}
		var raw worksheetFormulaXML
		if err := xml.Unmarshal(rawData, &raw); err != nil {
			return "", nil, "Invalid XLSX worksheet XML: " + err.Error()
		}
		for _, row := range raw.SheetData.Rows {
			for _, cell := range row.Cells {
				if cell.Formula == nil || formulaCells[cell.Ref] {
					continue
				}
				formulaCells[cell.Ref] = true
				if cell.Value != "" {
					continue
				}
				formula := strings.TrimSpace(cell.Formula.Text)
				if formula == "" {
					formula = "(shared or array formula; expression unavailable)"
				}
				if len(sheet)+len(cell.Ref)+len(formula)+180 > maxDocumentText-b.Len() {
					return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
				}
				warning := fmt.Sprintf("Formula cell %s!%s (%s) has no cached value. This cell is unresolved; enter or confirm its value.", sheet, cell.Ref, formula)
				marker := cell.Ref + " [unresolved formula: " + formula + "]\t"
				if len(warning)+len(marker) > maxDocumentText-b.Len() {
					return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
				}
				if len(warning) > maxDocumentText-b.Len()-warningBytes {
					return "", nil, "Extracted XLSX text and warnings exceed the 1 MiB limit."
				}
				warningBytes += len(warning)
				warnings = append(warnings, warning)
				if !appendText(marker) {
					return "", nil, "Extracted XLSX text exceeds the 1 MiB limit."
				}
			}
		}
	}
	return b.String(), warnings, ""
}

func openBoundedZip(data []byte) (map[string][]byte, error) {
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, err
	}
	if len(zr.File) > maxZipEntries {
		return nil, errors.New("archive has too many entries")
	}
	out := make(map[string][]byte, len(zr.File))
	var expanded uint64
	for _, file := range zr.File {
		if file.FileInfo().IsDir() {
			continue
		}
		name := filepath.ToSlash(file.Name)
		if strings.HasPrefix(name, "/") || strings.Contains(name, "../") || strings.Contains(name, "\\") {
			return nil, errors.New("archive contains unsafe path")
		}
		if file.UncompressedSize64 > maxZipExpanded-expanded {
			return nil, errors.New("archive expanded size exceeds limit")
		}
		expanded += file.UncompressedSize64
		r, err := file.Open()
		if err != nil {
			return nil, err
		}
		content, err := io.ReadAll(io.LimitReader(r, maxZipExpanded+1))
		closeErr := r.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		if len(content) > maxZipExpanded {
			return nil, errors.New("archive member exceeds limit")
		}
		out[name] = content
	}
	return out, nil
}
