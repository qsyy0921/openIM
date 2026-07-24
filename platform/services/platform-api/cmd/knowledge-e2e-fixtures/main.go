package main

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ledongpdf "github.com/ledongthuc/pdf"
	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	"github.com/qsyy0921/openim/platform/services/platform-api/internal/knowledge"
)

type documentFixture struct {
	Format        string `json:"format"`
	Title         string `json:"title"`
	Filename      string `json:"filename"`
	Marker        string `json:"marker"`
	VersionFile   string `json:"version_file,omitempty"`
	VersionMarker string `json:"version_marker,omitempty"`
}

type fixtureManifest struct {
	SchemaVersion   int               `json:"schema_version"`
	BatchID         string            `json:"batch_id"`
	Question        string            `json:"question"`
	OldVersionQuery string            `json:"old_version_query"`
	Documents       []documentFixture `json:"documents"`
	InvalidPDF      string            `json:"invalid_pdf"`
}

var validBatchID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9-]{7,63}$`)

func main() {
	var outputDir string
	var batchID string
	flag.StringVar(&outputDir, "output", "", "empty output directory for generated fixtures")
	flag.StringVar(&batchID, "batch-id", "", "unique 8-64 character acceptance batch ID")
	flag.Parse()

	if err := generateFixtures(outputDir, batchID); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Printf("enterprise_rag_fixture_manifest=%s\n", filepath.Join(outputDir, "manifest.json"))
}

func generateFixtures(outputDir, batchID string) error {
	outputDir = strings.TrimSpace(outputDir)
	batchID = strings.TrimSpace(batchID)
	if outputDir == "" || !validBatchID.MatchString(batchID) {
		return errors.New("output directory and a valid unique batch ID are required")
	}
	if err := os.MkdirAll(outputDir, 0o700); err != nil {
		return fmt.Errorf("create fixture directory: %w", err)
	}
	entries, err := os.ReadDir(outputDir)
	if err != nil {
		return fmt.Errorf("inspect fixture directory: %w", err)
	}
	if len(entries) != 0 {
		return errors.New("fixture output directory must be empty")
	}

	markers := map[string]string{
		"markdown": "MD-" + batchID,
		"text":     "TXT-" + batchID,
		"pdf":      "PDF-" + batchID,
		"docx":     "DOCX-" + batchID,
	}
	versionMarker := "MD2-" + batchID
	documents := []documentFixture{
		{
			Format: "markdown", Title: "E2E RAG Markdown " + batchID,
			Filename: "policy-markdown.md", Marker: markers["markdown"],
			VersionFile: "policy-markdown-v2.md", VersionMarker: versionMarker,
		},
		{
			Format: "text", Title: "E2E RAG TXT " + batchID,
			Filename: "policy-text.txt", Marker: markers["text"],
		},
		{
			Format: "pdf", Title: "E2E RAG PDF " + batchID,
			Filename: "policy-pdf.pdf", Marker: markers["pdf"],
		},
		{
			Format: "docx", Title: "E2E RAG DOCX " + batchID,
			Filename: "policy-docx.docx", Marker: markers["docx"],
		},
	}

	common := "Enterprise RAG four-format acceptance batch " + batchID + ". "
	if err := writeExclusive(filepath.Join(outputDir, documents[0].Filename), []byte(
		"# Markdown control policy\n\n"+common+
			"The Markdown control code is "+markers["markdown"]+
			". Report this exact code and cite this document when asked.\n")); err != nil {
		return err
	}
	if err := writeExclusive(filepath.Join(outputDir, documents[0].VersionFile), []byte(
		"# Markdown control policy version 2\n\n"+common+
			"The current Markdown control code is "+versionMarker+
			". The previous code is retired and must not be cited as current.\n")); err != nil {
		return err
	}
	if err := writeExclusive(filepath.Join(outputDir, documents[1].Filename), []byte(
		common+"The TXT control code is "+markers["text"]+
			". Report this exact code and cite this document when asked.\n")); err != nil {
		return err
	}
	if err := writePDF(filepath.Join(outputDir, documents[2].Filename),
		common+"The PDF control code is "+markers["pdf"]+
			". Report this exact code and cite this document when asked.", markers["pdf"]); err != nil {
		return err
	}
	if err := writeDOCX(filepath.Join(outputDir, documents[3].Filename),
		"DOCX control policy", common+"The DOCX control code is "+markers["docx"]+
			". Report this exact code and cite this document when asked."); err != nil {
		return err
	}
	invalidPDF := "invalid-structure.pdf"
	if err := writeExclusive(filepath.Join(outputDir, invalidPDF),
		[]byte("%PDF-1.7\nnot-a-valid-object-graph\n%%EOF\n")); err != nil {
		return err
	}
	parser := knowledge.NewParser()
	for _, document := range documents {
		if _, err := parser.Parse(filepath.Join(outputDir, document.Filename),
			knowledge.SourceFormat(document.Format)); err != nil {
			return fmt.Errorf("validate generated %s fixture: %w", document.Format, err)
		}
	}
	if _, err := parser.Parse(filepath.Join(outputDir, invalidPDF), knowledge.FormatPDF); err == nil {
		return errors.New("invalid PDF fixture unexpectedly passed the production parser")
	}

	manifest := fixtureManifest{
		SchemaVersion: 1,
		BatchID:       batchID,
		Question: "请根据四份 E2E 文档列出 Markdown、TXT、PDF、DOCX 的控制代码，" +
			"逐项回答并引用每一份文档。验收批次 " + batchID + "。",
		OldVersionQuery: "请报告 Markdown 文档上一版本的控制代码 " + markers["markdown"] +
			"，并引用当前有效版本。验收批次 " + batchID + "。",
		Documents:  documents,
		InvalidPDF: invalidPDF,
	}
	raw, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("encode fixture manifest: %w", err)
	}
	raw = append(raw, '\n')
	if err := writeExclusive(filepath.Join(outputDir, "manifest.json"), raw); err != nil {
		return err
	}
	return nil
}

func writeExclusive(path string, content []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create fixture %s: %w", filepath.Base(path), err)
	}
	if _, err := file.Write(content); err != nil {
		_ = file.Close()
		return fmt.Errorf("write fixture %s: %w", filepath.Base(path), err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close fixture %s: %w", filepath.Base(path), err)
	}
	return nil
}

func writeDOCX(path, heading, body string) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create DOCX fixture: %w", err)
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Default Extension="rels" ContentType="application/vnd.openxmlformats-package.relationships+xml"/><Default Extension="xml" ContentType="application/xml"/><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" Type="http://schemas.openxmlformats.org/officeDocument/2006/relationships/officeDocument" Target="word/document.xml"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0" encoding="UTF-8" standalone="yes"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>` + escapeXML(heading) + `</w:t></w:r></w:p><w:p><w:r><w:t>` + escapeXML(body) + `</w:t></w:r></w:p></w:body></w:document>`,
	}
	for _, name := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		writer, err := archive.Create(name)
		if err != nil {
			_ = archive.Close()
			_ = file.Close()
			return fmt.Errorf("create DOCX part %s: %w", name, err)
		}
		if _, err := io.WriteString(writer, parts[name]); err != nil {
			_ = archive.Close()
			_ = file.Close()
			return fmt.Errorf("write DOCX part %s: %w", name, err)
		}
	}
	if err := archive.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("close DOCX archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close DOCX fixture: %w", err)
	}
	return nil
}

func writePDF(path, text, marker string) error {
	content := "BT /F1 12 Tf 72 760 Td (" + escapePDFLiteral(text) + ") Tj ET\n"
	widths := strings.TrimSpace(strings.Repeat("600 ", 95))
	objects := []string{
		"<< /Type /Catalog /Pages 2 0 R >>",
		"<< /Type /Pages /Kids [3 0 R] /Count 1 >>",
		"<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Resources << /Font << /F1 5 0 R >> >> /Contents 4 0 R >>",
		fmt.Sprintf("<< /Length %d >>\nstream\n%sendstream", len(content), content),
		"<< /Type /Font /Subtype /Type1 /BaseFont /Helvetica /Encoding /WinAnsiEncoding " +
			"/FirstChar 32 /LastChar 126 /Widths [" + widths + "] /FontDescriptor 6 0 R >>",
		"<< /Type /FontDescriptor /FontName /Helvetica /Flags 32 " +
			"/FontBBox [-166 -225 1000 931] /ItalicAngle 0 /Ascent 718 " +
			"/Descent -207 /CapHeight 718 /StemV 88 >>",
	}
	var document bytes.Buffer
	document.WriteString("%PDF-1.7\n%\xe2\xe3\xcf\xd3\n")
	offsets := make([]int, len(objects)+1)
	for index, object := range objects {
		offsets[index+1] = document.Len()
		fmt.Fprintf(&document, "%d 0 obj\n%s\nendobj\n", index+1, object)
	}
	xrefOffset := document.Len()
	fmt.Fprintf(&document, "xref\n0 %d\n0000000000 65535 f \n", len(objects)+1)
	for index := 1; index <= len(objects); index++ {
		fmt.Fprintf(&document, "%010d 00000 n \n", offsets[index])
	}
	fmt.Fprintf(&document, "trailer\n<< /Size %d /Root 1 0 R >>\nstartxref\n%d\n%%%%EOF\n",
		len(objects)+1, xrefOffset)
	if err := writeExclusive(path, document.Bytes()); err != nil {
		return err
	}
	configuration := pdfmodel.NewDefaultConfiguration()
	configuration.ValidationMode = pdfmodel.ValidationStrict
	if err := pdfapi.ValidateFile(path, configuration); err != nil {
		return fmt.Errorf("validate generated PDF: %w", err)
	}
	file, reader, err := ledongpdf.Open(path)
	if err != nil {
		return fmt.Errorf("open generated PDF text layer: %w", err)
	}
	defer file.Close()
	plain, err := reader.GetPlainText()
	if err != nil {
		return fmt.Errorf("read generated PDF text layer: %w", err)
	}
	extracted, err := io.ReadAll(plain)
	if err != nil {
		return fmt.Errorf("extract generated PDF text: %w", err)
	}
	if !strings.Contains(strings.Join(strings.Fields(string(extracted)), " "), marker) {
		return fmt.Errorf("generated PDF text layer does not contain marker %q: %q", marker, string(extracted))
	}
	return nil
}

func escapePDFLiteral(value string) string {
	value = strings.ReplaceAll(value, `\`, `\\`)
	value = strings.ReplaceAll(value, `(`, `\(`)
	return strings.ReplaceAll(value, `)`, `\)`)
}

func escapeXML(value string) string {
	var output bytes.Buffer
	for _, char := range value {
		switch char {
		case '&':
			output.WriteString("&amp;")
		case '<':
			output.WriteString("&lt;")
		case '>':
			output.WriteString("&gt;")
		case '"':
			output.WriteString("&quot;")
		case '\'':
			output.WriteString("&apos;")
		default:
			output.WriteRune(char)
		}
	}
	return output.String()
}
