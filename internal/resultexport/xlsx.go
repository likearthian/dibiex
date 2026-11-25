package resultexport

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"regexp"
	"strings"
	"unicode/utf16"
)

var excelEscape = regexp.MustCompile(`_x[0-9a-fA-F]{4}_`)

const xlsxStyles = `<styleSheet xmlns="http://schemas.openxmlformats.org/spreadsheetml/2006/main">
<fonts count="2"><font/><font><b/></font></fonts>
<fills count="3"><fill><patternFill patternType="none"/></fill><fill><patternFill patternType="gray125"/></fill><fill><patternFill patternType="solid"><fgColor rgb="FFE7E6E6"/><bgColor indexed="64"/></patternFill></fill></fills>
<borders count="1"><border/></borders>
<cellStyleXfs count="1"><xf numFmtId="0" fontId="0" fillId="0" borderId="0"/></cellStyleXfs>
<cellXfs count="2">
<xf numFmtId="0" fontId="0" fillId="0" borderId="0" xfId="0"/>
<xf numFmtId="0" fontId="1" fillId="2" borderId="0" xfId="0" applyFont="1" applyFill="1" applyAlignment="1"><alignment horizontal="center" vertical="center"/></xf>
</cellXfs>
<cellStyles count="1"><cellStyle name="Normal" xfId="0" builtinId="0"/></cellStyles>
</styleSheet>`

func xlsxText(s string) string {
	s = excelEscape.ReplaceAllStringFunc(s, func(token string) string { return "_x005F_" + token[1:] })
	var b strings.Builder
	for _, r := range s {
		if r < 32 && r != '\t' && r != '\n' && r != '\r' || r == '\ufffe' || r == '\uffff' {
			fmt.Fprintf(&b, "_x%04X_", r)
		} else {
			b.WriteRune(r)
		}
	}
	var escaped bytes.Buffer
	_ = xml.EscapeText(&escaped, []byte(b.String()))
	return escaped.String()
}

func writeXLSX(out io.Writer, d Data, o Options) error {
	return writeXLSXWithRowLimit(out, d, o, 1048576)
}

func writeXLSXWithRowLimit(out io.Writer, d Data, o Options, rowLimit int) (err error) {
	if len(d.Columns) > 16384 {
		return fmt.Errorf("result exceeds Excel worksheet column limit")
	}
	w := zip.NewWriter(out)
	defer func() {
		e := w.Close()
		if err == nil {
			err = e
		}
	}()
	method := uint16(zip.Deflate)
	if o.Compression == "None" {
		method = zip.Store
	}
	part := func(name, body string) error {
		p, e := w.CreateHeader(&zip.FileHeader{Name: name, Method: method})
		if e != nil {
			return e
		}
		_, e = io.WriteString(p, body)
		return e
	}
	const ns = "http://schemas.openxmlformats.org/spreadsheetml/2006/main"
	names := []string{}
	var sheet io.Writer
	rowNumber := 0
	startSheet := func(name string) error {
		names = append(names, name)
		p, e := w.CreateHeader(&zip.FileHeader{Name: fmt.Sprintf("xl/worksheets/sheet%d.xml", len(names)), Method: method})
		if e != nil {
			return e
		}
		sheet = p
		rowNumber = 0
		_, e = io.WriteString(sheet, `<worksheet xmlns="`+ns+`"><sheetData>`)
		return e
	}
	endSheet := func() error { _, e := io.WriteString(sheet, `</sheetData></worksheet>`); return e }
	writeRow := func(row []string, header bool) error {
		rowNumber++
		if rowNumber > rowLimit {
			return fmt.Errorf("query sheet exceeds Excel worksheet row limit")
		}
		if _, e := fmt.Fprintf(sheet, `<row r="%d">`, rowNumber); e != nil {
			return e
		}
		style := ""
		if header {
			style = ` s="1"`
		}
		for _, value := range row {
			if len(utf16.Encode([]rune(value))) > 32767 {
				return fmt.Errorf("a value exceeds Excel's 32,767-character cell limit")
			}
			if _, e := fmt.Fprintf(sheet, `<c%s t="inlineStr"><is><t xml:space="preserve">%s</t></is></c>`, style, xlsxText(value)); e != nil {
				return e
			}
		}
		_, e := io.WriteString(sheet, `</row>`)
		return e
	}
	if err = startSheet("Results"); err != nil {
		return err
	}
	if err = writeRow(d.Columns, true); err != nil {
		return err
	}
	if err = d.eachRow(func(row []string) error {
		if rowNumber == rowLimit {
			if e := endSheet(); e != nil {
				return e
			}
			if e := startSheet(fmt.Sprintf("Results %d", len(names)+1)); e != nil {
				return e
			}
			if e := writeRow(d.Columns, true); e != nil {
				return e
			}
		}
		return writeRow(row, false)
	}); err != nil {
		return err
	}
	if err = endSheet(); err != nil {
		return err
	}
	if o.QuerySheet {
		if err = startSheet("Query"); err != nil {
			return err
		}
		for _, line := range strings.Split(d.SQL, "\n") {
			if err = writeRow([]string{line}, false); err != nil {
				return err
			}
		}
		if err = endSheet(); err != nil {
			return err
		}
	}
	content := `<?xml version="1.0" encoding="UTF-8"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/xl/workbook.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.sheet.main+xml"/>`
	workbook := `<workbook xmlns="` + ns + `" xmlns:r="http://schemas.openxmlformats.org/officeDocument/2006/relationships"><sheets>`
	rels := `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships">`
	content += `<Override PartName="/xl/styles.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.styles+xml"/>`
	rels += `<Relationship Id="rIdStyles" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/styles" Target="styles.xml"/>`
	for j, name := range names {
		i := j + 1
		content += fmt.Sprintf(`<Override PartName="/xl/worksheets/sheet%d.xml" ContentType="application/vnd.openxmlformats-officedocument.spreadsheetml.worksheet+xml"/>`, i)
		workbook += fmt.Sprintf(`<sheet name="%s" sheetId="%d" r:id="rId%d"/>`, name, i, i)
		rels += fmt.Sprintf(`<Relationship Id="rId%d" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/worksheet" Target="worksheets/sheet%d.xml"/>`, i, i)
	}
	if err = part("xl/styles.xml", xlsxStyles); err != nil {
		return err
	}
	for _, p := range [][2]string{{"[Content_Types].xml", content + `</Types>`}, {"_rels/.rels", `<Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="xl/workbook.xml"/></Relationships>`}, {"xl/workbook.xml", workbook + `</sheets></workbook>`}, {"xl/_rels/workbook.xml.rels", rels + `</Relationships>`}} {
		if err = part(p[0], p[1]); err != nil {
			return err
		}
	}
	return nil
}
