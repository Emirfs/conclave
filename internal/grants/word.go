package grants

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	wordNamespace        = "http://schemas.openxmlformats.org/wordprocessingml/2006/main"
	relNamespace         = "http://schemas.openxmlformats.org/officeDocument/2006/relationships"
	pkgRelNamespace      = "http://schemas.openxmlformats.org/package/2006/relationships"
	contentTypeNamespace = "http://schemas.openxmlformats.org/package/2006/content-types"
)

type mdBlock struct {
	kind, text string
	cells      [][]string
}

// BuildWord renders Markdown content as a standards-based DOCX package.
func BuildWord(title, markdown string) ([]byte, error) {
	blocks := parseMarkdown(markdown)
	links := collectLinks(blocks)
	var body strings.Builder
	writeParagraph(&body, title, "Title", links)
	for _, block := range blocks {
		switch block.kind {
		case "heading":
			writeParagraph(&body, strings.TrimSpace(strings.TrimLeft(block.text, "#")), fmt.Sprintf("Heading%d", headingLevel(block.text)), links)
		case "list":
			writeParagraph(&body, strings.TrimLeft(block.text, "-+*0123456789. "), "ListParagraph", links)
		case "quote":
			writeParagraph(&body, strings.TrimSpace(strings.TrimPrefix(block.text, ">")), "Quote", links)
		case "table":
			writeTable(&body, block.cells, links)
		default:
			writeParagraph(&body, block.text, "", links)
		}
	}
	var styles strings.Builder
	styles.WriteString(`<w:styles xmlns:w="` + wordNamespace + `"><w:style w:type="paragraph" w:styleId="Normal"><w:name w:val="Normal"/></w:style><w:style w:type="paragraph" w:styleId="Title"><w:name w:val="Title"/><w:basedOn w:val="Normal"/><w:rPr><w:b/><w:sz w:val="32"/></w:rPr></w:style>`)
	for level := 1; level <= 6; level++ {
		styles.WriteString(fmt.Sprintf(`<w:style w:type="paragraph" w:styleId="Heading%d"><w:name w:val="heading %d"/><w:basedOn w:val="Normal"/><w:outlineLvl w:val="%d"/><w:rPr><w:b/><w:sz w:val="%d"/></w:rPr></w:style>`, level, level, level-1, 32-level*2))
	}
	styles.WriteString(`<w:style w:type="paragraph" w:styleId="ListParagraph"><w:name w:val="List Paragraph"/><w:basedOn w:val="Normal"/><w:ind w:left="720"/></w:style></w:styles>`)
	main := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="` + wordNamespace + `" xmlns:r="` + relNamespace + `"><w:body>` + body.String() + `<w:sectPr><w:pgSz w:w="12240" w:h="15840"/><w:pgMar w:top="1440" w:right="1440" w:bottom="1440" w:left="1440"/></w:sectPr></w:body></w:document>`
	contentTypes := `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="` + contentTypeNamespace + `"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/><Override PartName="/word/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.styles+xml"/></Types>`
	rootRels := `<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="` + pkgRelNamespace + `"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`
	var docRels strings.Builder
	docRels.WriteString(`<?xml version="1.0" encoding="UTF-8"?><Relationships xmlns="` + pkgRelNamespace + `">`)
	for i, link := range links {
		docRels.WriteString(fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/hyperlink" Target="%s" TargetMode="External"/>`, i+2, xmlEscape(link)))
	}
	docRels.WriteString(`</Relationships>`)
	var out bytes.Buffer
	zw := zip.NewWriter(&out)
	files := []struct{ name, content string }{{"[Content_Types].xml", contentTypes}, {"_rels/.rels", rootRels}, {"word/document.xml", main}, {"word/styles.xml", styles.String()}, {"word/_rels/document.xml.rels", docRels.String()}}
	for _, file := range files {
		w, err := zw.Create(file.name)
		if err != nil {
			return nil, err
		}
		if _, err := w.Write([]byte(file.content)); err != nil {
			return nil, err
		}
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

func headingLevel(s string) int {
	n := 0
	for n < len(s) && s[n] == '#' {
		n++
	}
	if n < 1 {
		return 1
	}
	if n > 6 {
		return 6
	}
	return n
}
func parseMarkdown(src string) []mdBlock {
	lines := strings.Split(strings.ReplaceAll(src, "\r\n", "\n"), "\n")
	var blocks []mdBlock
	for i := 0; i < len(lines); {
		line := strings.TrimSpace(lines[i])
		if line == "" {
			i++
			continue
		}
		if strings.HasPrefix(line, "|") && i+1 < len(lines) && strings.Contains(lines[i+1], "---") {
			var rows [][]string
			for i < len(lines) && strings.HasPrefix(strings.TrimSpace(lines[i]), "|") {
				raw := strings.Split(strings.Trim(strings.TrimSpace(lines[i]), "|"), "|")
				cells := make([]string, len(raw))
				for j, cell := range raw {
					cells[j] = strings.TrimSpace(cell)
				}
				if !(len(rows) == 1 && allMarkdownDivider(cells)) {
					rows = append(rows, cells)
				}
				i++
			}
			blocks = append(blocks, mdBlock{kind: "table", cells: rows})
			continue
		}
		if level := markdownHeadingLevel(line); level > 0 {
			blocks = append(blocks, mdBlock{kind: "heading", text: line})
			i++
			continue
		}
		if isList(line) {
			blocks = append(blocks, mdBlock{kind: "list", text: line})
			i++
			continue
		}
		if strings.HasPrefix(line, "> ") {
			blocks = append(blocks, mdBlock{kind: "quote", text: line})
			i++
			continue
		}
		var paragraph []string
		for i < len(lines) && strings.TrimSpace(lines[i]) != "" && markdownHeadingLevel(strings.TrimSpace(lines[i])) == 0 && !isList(strings.TrimSpace(lines[i])) {
			paragraph = append(paragraph, strings.TrimSpace(lines[i]))
			i++
		}
		blocks = append(blocks, mdBlock{kind: "paragraph", text: strings.Join(paragraph, " ")})
	}
	return blocks
}

func markdownHeadingLevel(text string) int {
	level := 0
	for level < len(text) && text[level] == '#' {
		level++
	}
	if level < 1 || level > 6 || (level < len(text) && text[level] != ' ') {
		return 0
	}
	return level
}
func allMarkdownDivider(c []string) bool {
	if len(c) == 0 {
		return false
	}
	for _, v := range c {
		v = strings.Trim(v, ": ")
		if len(v) < 3 || strings.Trim(v, "-") != "" {
			return false
		}
	}
	return true
}
func isList(s string) bool {
	return strings.HasPrefix(s, "- ") || strings.HasPrefix(s, "* ") || strings.HasPrefix(s, "+ ") || len(s) > 2 && s[0] >= '0' && s[0] <= '9' && strings.Contains(s[:3], ". ")
}
func collectLinks(blocks []mdBlock) []string {
	var links []string
	seen := map[string]bool{}
	add := func(s string) {
		for _, link := range inlineLinks(s) {
			if !seen[link] {
				seen[link] = true
				links = append(links, link)
			}
		}
	}
	for _, b := range blocks {
		add(b.text)
		for _, row := range b.cells {
			for _, cell := range row {
				add(cell)
			}
		}
	}
	return links
}
func inlineLinks(s string) []string {
	var links []string
	for {
		a := strings.Index(s, "](")
		if a < 0 {
			break
		}
		z := strings.Index(s[a+2:], ")")
		if z < 0 {
			break
		}
		link := s[a+2 : a+2+z]
		u, err := url.Parse(link)
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" {
			links = append(links, link)
		}
		s = s[a+2+z+1:]
	}
	return links
}
func writeParagraph(b *strings.Builder, text, style string, links []string) {
	b.WriteString(`<w:p>`)
	if style != "" {
		b.WriteString(`<w:pPr><w:pStyle w:val="` + style + `"/></w:pPr>`)
	}
	writeInline(b, text, links)
	b.WriteString(`</w:p>`)
}
func writeInline(b *strings.Builder, s string, links []string) {
	for len(s) > 0 {
		a := strings.Index(s, "](")
		if a < 0 {
			writeText(b, stripMarkdown(s))
			return
		}
		z := strings.Index(s[a+2:], ")")
		if z < 0 {
			writeText(b, stripMarkdown(s))
			return
		}
		labelStart := strings.LastIndex(s[:a], "[")
		if labelStart < 0 {
			writeText(b, stripMarkdown(s[:a+2+z+1]))
			s = s[a+2+z+1:]
			continue
		}
		label := s[labelStart+1 : a]
		link := s[a+2 : a+2+z]
		writeText(b, stripMarkdown(s[:labelStart]))
		id := 0
		for i, v := range links {
			if v == link {
				id = i + 2
				break
			}
		}
		u, err := url.Parse(link)
		if err == nil && (u.Scheme == "https" || u.Scheme == "http") && u.Host != "" && id > 0 {
			b.WriteString(`<w:hyperlink r:id="rId` + fmt.Sprint(id) + `"><w:r><w:rPr><w:color w:val="0563C1"/><w:u w:val="single"/></w:rPr><w:t xml:space="preserve">` + xmlEscape(stripMarkdown(label)) + `</w:t></w:r></w:hyperlink>`)
		} else {
			writeText(b, stripMarkdown(label))
		}
		s = s[a+2+z+1:]
	}
}
func writeText(b *strings.Builder, s string) {
	if s != "" {
		b.WriteString(`<w:r><w:t xml:space="preserve">` + xmlEscape(s) + `</w:t></w:r>`)
	}
}
func stripMarkdown(text string) string {
	var result strings.Builder
	for index := 0; index < len(text); {
		matched := false
		for _, marker := range [...]string{"**", "__", "*", "_", "`"} {
			if !strings.HasPrefix(text[index:], marker) || (index > 0 && text[index-1] == '\\') {
				continue
			}
			if marker != "`" && index > 0 {
				previous, _ := utf8.DecodeLastRuneInString(text[:index])
				if unicode.IsLetter(previous) || unicode.IsNumber(previous) || previous == '_' {
					continue
				}
			}
			start := index + len(marker)
			offset := strings.Index(text[start:], marker)
			if offset <= 0 {
				continue
			}
			end := start + offset
			if marker != "`" {
				first, _ := utf8.DecodeRuneInString(text[start:end])
				last, _ := utf8.DecodeLastRuneInString(text[start:end])
				if unicode.IsSpace(first) || unicode.IsSpace(last) {
					continue
				}
				if next := end + len(marker); next < len(text) {
					following, _ := utf8.DecodeRuneInString(text[next:])
					if unicode.IsLetter(following) || unicode.IsNumber(following) || following == '_' {
						continue
					}
				}
			}
			result.WriteString(text[start:end])
			index = end + len(marker)
			matched = true
			break
		}
		if !matched {
			result.WriteByte(text[index])
			index++
		}
	}
	return result.String()
}
func writeTable(b *strings.Builder, rows [][]string, links []string) {
	b.WriteString(`<w:tbl><w:tblPr><w:tblBorders><w:top w:val="single"/><w:left w:val="single"/><w:bottom w:val="single"/><w:right w:val="single"/><w:insideH w:val="single"/><w:insideV w:val="single"/></w:tblBorders></w:tblPr>`)
	for _, row := range rows {
		b.WriteString(`<w:tr>`)
		for _, cell := range row {
			b.WriteString(`<w:tc><w:tcPr><w:tcW w:w="2400" w:type="dxa"/></w:tcPr><w:p>`)
			writeInline(b, cell, links)
			b.WriteString(`</w:p></w:tc>`)
		}
		b.WriteString(`</w:tr>`)
	}
	b.WriteString(`</w:tbl>`)
}
func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
