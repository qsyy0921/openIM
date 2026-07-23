package knowledge

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	pdfapi "github.com/pdfcpu/pdfcpu/pkg/api"
	pdfcpu "github.com/pdfcpu/pdfcpu/pkg/pdfcpu"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
	pdftypes "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/types"
)

func TestValidateUploadMetadataRequiresFourWayAgreement(t *testing.T) {
	valid := UploadFile{
		OriginalFilename: "policy.md", DeclaredType: "text/markdown", DetectedType: "text/plain",
		Format: FormatMarkdown, Size: 12, Checksum: checksumText("policy"),
	}
	if err := ValidateUploadMetadata(valid); err != nil {
		t.Fatal(err)
	}
	valid.DetectedType = "application/pdf"
	if err := ValidateUploadMetadata(valid); err == nil {
		t.Fatal("mismatched signature was accepted")
	}
	valid.DetectedType = "text/plain"
	valid.OriginalFilename = "../policy.md"
	if err := ValidateUploadMetadata(valid); err == nil {
		t.Fatal("unsafe filename was accepted")
	}
}

func TestTextParserRejectsInvalidUTF8AndNUL(t *testing.T) {
	parser := NewParser()
	for name, content := range map[string][]byte{
		"invalid": {0xff, 0xfe},
		"nul":     []byte("policy\x00secret"),
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "source.txt")
			if err := os.WriteFile(path, content, 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := parser.Parse(path, FormatText); err == nil {
				t.Fatal("unsafe text was accepted")
			}
		})
	}
}

func TestDOCXParserRejectsExternalRelationships(t *testing.T) {
	path := filepath.Join(t.TempDir(), "external.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"><Relationship Id="rId1" TargetMode="External" Target="https://example.invalid"/></Relationships>`,
		"word/document.xml":   `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Policy</w:t></w:r></w:p></w:body></w:document>`,
	}
	for name, content := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParser().Parse(path, FormatDOCX); err == nil || !strings.Contains(err.Error(), "DOCX_EXTERNAL_RELATIONSHIP_REJECTED") {
		t.Fatalf("external relationship was not rejected explicitly: %v", err)
	}
}

func TestDOCXParserExtractsHeadingAndText(t *testing.T) {
	path := filepath.Join(t.TempDir(), "policy.docx")
	writeTestDOCX(t, path,
		`<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`,
		`<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:pPr><w:pStyle w:val="Heading1"/></w:pPr><w:r><w:t>访问控制</w:t></w:r></w:p><w:p><w:r><w:t>每季度复核访问权限。</w:t></w:r></w:p></w:body></w:document>`,
	)
	sections, err := NewParser().Parse(path, FormatDOCX)
	if err != nil {
		t.Fatal(err)
	}
	if len(sections) != 1 || sections[0].Heading != "访问控制" ||
		sections[0].Text != "每季度复核访问权限。" {
		t.Fatalf("unexpected DOCX sections: %#v", sections)
	}
}

func TestDOCXParserRejectsUnsafePartName(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unsafe.docx")
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	writer, err := archive.Create("../word/document.xml")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Write([]byte("unsafe")); err != nil {
		t.Fatal(err)
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParser().Parse(path, FormatDOCX); err == nil ||
		!strings.Contains(err.Error(), "DOCX_STRUCTURE_INVALID") {
		t.Fatalf("unsafe DOCX part name was not rejected: %v", err)
	}
}

func TestDOCXParserRejectsMacroAndCompressionBomb(t *testing.T) {
	relationships := `<?xml version="1.0"?><Relationships xmlns="http://schemas.openxmlformats.org/package/2006/relationships"></Relationships>`
	document := `<?xml version="1.0"?><w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main"><w:body><w:p><w:r><w:t>Policy</w:t></w:r></w:p></w:body></w:document>`
	tests := []struct {
		name      string
		extraName string
		extra     string
		code      string
	}{
		{
			name: "macro", extraName: "word/vbaProject.bin", extra: "active-content",
			code: "DOCX_ACTIVE_CONTENT_REJECTED",
		},
		{
			name: "compression-bomb", extraName: "word/large.xml",
			extra: strings.Repeat("A", 2<<20), code: "DOCX_EXPANSION_LIMIT",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), test.name+".docx")
			writeTestDOCXWithExtra(t, path, relationships, document, test.extraName, test.extra)
			if _, err := NewParser().Parse(path, FormatDOCX); err == nil ||
				!strings.Contains(err.Error(), test.code) {
				t.Fatalf("unsafe DOCX was not rejected with %s: %v", test.code, err)
			}
		})
	}
}

func TestPDFParserRejectsMalformedStructure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "malformed.pdf")
	if err := os.WriteFile(path, []byte("%PDF-1.7\nnot-a-valid-object-graph\n%%EOF"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParser().Parse(path, FormatPDF); err == nil ||
		!strings.Contains(err.Error(), "PDF_STRUCTURE_INVALID") {
		t.Fatalf("malformed PDF was not rejected explicitly: %v", err)
	}
}

func TestPDFParserRejectsEncryptedAndTextlessSources(t *testing.T) {
	plain := filepath.Join(t.TempDir(), "blank.pdf")
	writeBlankPDF(t, plain)
	if _, err := NewParser().Parse(plain, FormatPDF); err == nil ||
		!strings.Contains(err.Error(), "PDF_TEXT_EMPTY") {
		t.Fatalf("textless PDF was not rejected explicitly: %v", err)
	}

	encrypted := filepath.Join(t.TempDir(), "encrypted.pdf")
	configuration := pdfmodel.NewAESConfiguration("user-password", "owner-password", 256)
	if err := pdfapi.EncryptFile(plain, encrypted, configuration); err != nil {
		t.Fatal(err)
	}
	if _, err := NewParser().Parse(encrypted, FormatPDF); err == nil ||
		!strings.Contains(err.Error(), "PDF_") {
		t.Fatalf("encrypted PDF was not rejected explicitly: %v", err)
	}
}

func TestChunkSectionsIsDeterministicAndBounded(t *testing.T) {
	versionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
	var source strings.Builder
	for index := 0; index < 180; index++ {
		source.WriteString("这是第 ")
		source.WriteString(strconv.Itoa(index))
		source.WriteString(" 条用于验证确定性分块边界的企业制度句子。")
	}
	text := source.String()
	first, err := ChunkSections(versionID, []Section{{Heading: "安全制度", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	second, err := ChunkSections(versionID, []Section{{Heading: "安全制度", Text: text}})
	if err != nil {
		t.Fatal(err)
	}
	if len(first) < 2 || len(first) != len(second) {
		t.Fatalf("unexpected chunk count: %d and %d", len(first), len(second))
	}
	for index := range first {
		if first[index].ID != second[index].ID || first[index].Checksum != second[index].Checksum {
			t.Fatalf("chunk %d is not deterministic", index)
		}
		if utf8.RuneCountInString(first[index].Content) > MaxChunkRunes || len(first[index].Content) > MaxChunkBytes {
			t.Fatalf("chunk %d exceeds its budget", index)
		}
		if first[index].Lexemes == "" {
			t.Fatalf("chunk %d has no lexical projection", index)
		}
	}
}

func writeTestDOCX(t *testing.T, path, relationships, document string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         relationships,
		"word/document.xml":   document,
	}
	for name, content := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeTestDOCXWithExtra(t *testing.T, path, relationships, document, extraName, extra string) {
	t.Helper()
	file, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	archive := zip.NewWriter(file)
	parts := map[string]string{
		"[Content_Types].xml": `<?xml version="1.0"?><Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types"><Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/></Types>`,
		"_rels/.rels":         relationships,
		"word/document.xml":   document,
		extraName:             extra,
	}
	for name, content := range parts {
		writer, err := archive.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := writer.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := archive.Close(); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
}

func writeBlankPDF(t *testing.T, path string) {
	t.Helper()
	xref, err := pdfcpu.CreateDemoXRef()
	if err != nil {
		t.Fatal(err)
	}
	root, err := xref.Catalog()
	if err != nil {
		t.Fatal(err)
	}
	page := pdfmodel.Page{
		MediaBox: pdftypes.RectForFormat("A4"),
		Fm:       pdfmodel.FontMap{},
		Buf:      new(bytes.Buffer),
	}
	if err := pdfcpu.AddPageTreeWithSamplePage(xref, root, page); err != nil {
		t.Fatal(err)
	}
	if err := pdfapi.CreatePDFFile(xref, path, nil); err != nil {
		t.Fatal(err)
	}
}
