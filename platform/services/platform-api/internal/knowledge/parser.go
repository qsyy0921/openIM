package knowledge

import (
	"archive/zip"
	"bufio"
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	ledongpdf "github.com/ledongthuc/pdf"
	"github.com/pdfcpu/pdfcpu/pkg/api"
	pdfmodel "github.com/pdfcpu/pdfcpu/pkg/pdfcpu/model"
)

const (
	ParserRevision   = "openim-knowledge-parser-v1"
	maxTextLineRunes = 32_000
	maxDOCXEntries   = 256
	maxDOCXExpanded  = 128 << 20
	maxDOCXRatio     = 100
)

type ProcessingError struct {
	Code      string
	Detail    string
	Retryable bool
}

func (e *ProcessingError) Error() string {
	return e.Code + ": " + e.Detail
}

func processingError(code, detail string, retryable bool) error {
	detail = strings.TrimSpace(detail)
	if detail == "" {
		detail = "processing failed"
	}
	if len(detail) > 512 {
		detail = detail[:512]
	}
	return &ProcessingError{Code: code, Detail: detail, Retryable: retryable}
}

type Parser struct{}

func NewParser() *Parser {
	return &Parser{}
}

func (p *Parser) Validate(path string, format SourceFormat) error {
	_, err := p.Parse(path, format)
	return err
}

func (p *Parser) Parse(path string, format SourceFormat) ([]Section, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, processingError("SOURCE_READ_FAILED", "source file is unavailable", true)
	}
	if !info.Mode().IsRegular() || info.Size() < 1 || info.Size() > MaxSourceBytes {
		return nil, processingError("SOURCE_SIZE_INVALID", "source file size is outside the accepted range", false)
	}
	switch format {
	case FormatMarkdown:
		return parseUTF8File(path, true)
	case FormatText:
		return parseUTF8File(path, false)
	case FormatPDF:
		return parsePDF(path)
	case FormatDOCX:
		return parseDOCX(path)
	default:
		return nil, processingError("SOURCE_FORMAT_UNSUPPORTED", "source format is not supported", false)
	}
}

func ValidateUploadMetadata(file UploadFile) error {
	if file.Size < 1 || file.Size > MaxSourceBytes || !validSHA256(file.Checksum) {
		return fmt.Errorf("%w: file size or checksum is invalid", ErrInvalidInput)
	}
	name := strings.TrimSpace(file.OriginalFilename)
	if name == "" || len(name) > 255 || filepath.Base(name) != name ||
		strings.ContainsAny(name, `/\`) || strings.IndexFunc(name, unicode.IsControl) >= 0 {
		return fmt.Errorf("%w: original filename is unsafe", ErrInvalidInput)
	}
	extension := strings.ToLower(filepath.Ext(name))
	declared, _, err := mime.ParseMediaType(strings.TrimSpace(file.DeclaredType))
	if err != nil {
		return fmt.Errorf("%w: declared media type is invalid", ErrInvalidInput)
	}
	detected, _, err := mime.ParseMediaType(strings.TrimSpace(file.DetectedType))
	if err != nil {
		return fmt.Errorf("%w: detected media type is invalid", ErrInvalidInput)
	}
	valid := false
	switch file.Format {
	case FormatMarkdown:
		valid = (extension == ".md" || extension == ".markdown") &&
			(declared == "text/markdown" || declared == "text/plain") &&
			detected == "text/plain"
	case FormatText:
		valid = extension == ".txt" && declared == "text/plain" && detected == "text/plain"
	case FormatPDF:
		valid = extension == ".pdf" && declared == "application/pdf" && detected == "application/pdf"
	case FormatDOCX:
		valid = extension == ".docx" &&
			declared == "application/vnd.openxmlformats-officedocument.wordprocessingml.document" &&
			(detected == "application/zip" || detected == "application/octet-stream")
	}
	if !valid {
		return fmt.Errorf("%w: extension, declared media type, detected media type, and format disagree", ErrInvalidInput)
	}
	return nil
}

func parseUTF8File(path string, markdown bool) ([]Section, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, processingError("SOURCE_READ_FAILED", "source file cannot be read", true)
	}
	if bytes.ContainsRune(raw, '\x00') || !utf8.Valid(raw) {
		return nil, processingError("TEXT_ENCODING_INVALID", "text source must be valid UTF-8 without NUL bytes", false)
	}
	text := strings.TrimPrefix(string(raw), "\ufeff")
	text = normalizeNewlines(text)
	for _, line := range strings.Split(text, "\n") {
		if utf8.RuneCountInString(line) > maxTextLineRunes {
			return nil, processingError("TEXT_LINE_TOO_LONG", "text source contains an overlong line", false)
		}
	}
	sections := sectionsFromText(text, markdown)
	if len(sections) == 0 {
		return nil, processingError("TEXT_EMPTY", "text source contains no searchable text", false)
	}
	return sections, nil
}

func parsePDF(path string) ([]Section, error) {
	configuration := pdfmodel.NewDefaultConfiguration()
	configuration.ValidationMode = pdfmodel.ValidationStrict
	if err := api.ValidateFile(path, configuration); err != nil {
		return nil, processingError("PDF_STRUCTURE_INVALID", "PDF failed strict structural validation", false)
	}
	file, reader, err := ledongpdf.Open(path)
	if err != nil {
		return nil, processingError("PDF_OPEN_FAILED", "validated PDF cannot be opened for text extraction", false)
	}
	defer file.Close()
	plain, err := reader.GetPlainText()
	if err != nil {
		return nil, processingError("PDF_TEXT_EXTRACTION_FAILED", "PDF text layer could not be extracted", false)
	}
	var output bytes.Buffer
	if _, err := io.CopyN(&output, plain, int64(maxDOCXExpanded)+1); err != nil && !errors.Is(err, io.EOF) {
		return nil, processingError("PDF_TEXT_EXTRACTION_FAILED", "PDF text layer could not be read", false)
	}
	if output.Len() > maxDOCXExpanded {
		return nil, processingError("PDF_TEXT_TOO_LARGE", "PDF extracted text exceeds the safety limit", false)
	}
	text := normalizeNewlines(output.String())
	sections := sectionsFromText(text, false)
	if len(sections) == 0 {
		return nil, processingError("PDF_TEXT_EMPTY", "PDF has no usable text layer; OCR is not enabled", false)
	}
	return sections, nil
}

func parseDOCX(path string) ([]Section, error) {
	archive, err := zip.OpenReader(path)
	if err != nil {
		return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX ZIP container is invalid", false)
	}
	defer archive.Close()
	if len(archive.File) == 0 || len(archive.File) > maxDOCXEntries {
		return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX entry count exceeds the safety limit", false)
	}
	var expanded uint64
	parts := make(map[string]*zip.File, len(archive.File))
	for _, entry := range archive.File {
		name := filepath.ToSlash(entry.Name)
		if name != entry.Name || strings.HasPrefix(name, "/") || strings.Contains(name, "../") {
			return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX contains an unsafe part name", false)
		}
		lower := strings.ToLower(name)
		if strings.HasSuffix(lower, ".bin") || strings.Contains(lower, "vbaproject") ||
			strings.Contains(lower, "encryptedpackage") || strings.Contains(lower, "encryptioninfo") {
			return nil, processingError("DOCX_ACTIVE_CONTENT_REJECTED", "DOCX macros or encrypted content are not accepted", false)
		}
		expanded += entry.UncompressedSize64
		if expanded > maxDOCXExpanded {
			return nil, processingError("DOCX_EXPANSION_LIMIT", "DOCX expanded size exceeds the safety limit", false)
		}
		if entry.CompressedSize64 > 0 && entry.UncompressedSize64/entry.CompressedSize64 > maxDOCXRatio {
			return nil, processingError("DOCX_EXPANSION_LIMIT", "DOCX compression ratio exceeds the safety limit", false)
		}
		parts[name] = entry
	}
	for _, required := range []string{"[Content_Types].xml", "_rels/.rels", "word/document.xml"} {
		if parts[required] == nil {
			return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX is missing a required OOXML part", false)
		}
	}
	for name, entry := range parts {
		if strings.HasSuffix(strings.ToLower(name), ".rels") {
			raw, err := readZIPPart(entry, 2<<20)
			if err != nil {
				return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX relationships cannot be read", false)
			}
			if err := validateRelationships(raw); err != nil {
				return nil, err
			}
		}
	}
	contentTypes, err := readZIPPart(parts["[Content_Types].xml"], 2<<20)
	if err != nil || !bytes.Contains(contentTypes, []byte("application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml")) {
		return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX content types are invalid", false)
	}
	document, err := readZIPPart(parts["word/document.xml"], 64<<20)
	if err != nil {
		return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX document part cannot be read", false)
	}
	if bytes.Contains(bytes.ToLower(document), []byte("<w:altchunk")) {
		return nil, processingError("DOCX_ACTIVE_CONTENT_REJECTED", "DOCX altChunk content is not accepted", false)
	}
	sections, err := decodeWordDocument(document)
	if err != nil {
		return nil, err
	}
	if len(sections) == 0 {
		return nil, processingError("DOCX_TEXT_EMPTY", "DOCX contains no usable text", false)
	}
	return sections, nil
}

func readZIPPart(entry *zip.File, limit int64) ([]byte, error) {
	if entry.UncompressedSize64 > uint64(limit) {
		return nil, errors.New("part exceeds limit")
	}
	reader, err := entry.Open()
	if err != nil {
		return nil, err
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil || int64(len(data)) > limit {
		return nil, errors.New("part exceeds limit")
	}
	return data, nil
}

func validateRelationships(raw []byte) error {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			return processingError("DOCX_STRUCTURE_INVALID", "DOCX relationships XML is invalid", false)
		}
		switch value := token.(type) {
		case xml.Directive:
			return processingError("DOCX_ACTIVE_CONTENT_REJECTED", "DOCX XML directives are not accepted", false)
		case xml.StartElement:
			if value.Name.Local != "Relationship" {
				continue
			}
			for _, attribute := range value.Attr {
				if attribute.Name.Local == "TargetMode" && strings.EqualFold(attribute.Value, "External") {
					return processingError("DOCX_EXTERNAL_RELATIONSHIP_REJECTED", "DOCX external relationships are not accepted", false)
				}
			}
		}
	}
}

func decodeWordDocument(raw []byte) ([]Section, error) {
	decoder := xml.NewDecoder(bytes.NewReader(raw))
	decoder.Strict = true
	sections := make([]Section, 0)
	currentHeading := ""
	var paragraph strings.Builder
	style := ""
	inParagraph := false
	for {
		token, err := decoder.Token()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX document XML is invalid", false)
		}
		switch value := token.(type) {
		case xml.Directive:
			return nil, processingError("DOCX_ACTIVE_CONTENT_REJECTED", "DOCX XML directives are not accepted", false)
		case xml.StartElement:
			switch value.Name.Local {
			case "p":
				inParagraph = true
				paragraph.Reset()
				style = ""
			case "pStyle":
				if inParagraph {
					for _, attribute := range value.Attr {
						if attribute.Name.Local == "val" {
							style = attribute.Value
						}
					}
				}
			case "t":
				if inParagraph {
					var text string
					if err := decoder.DecodeElement(&text, &value); err != nil {
						return nil, processingError("DOCX_STRUCTURE_INVALID", "DOCX text node is invalid", false)
					}
					paragraph.WriteString(text)
				}
			case "tab":
				if inParagraph {
					paragraph.WriteByte('\t')
				}
			case "br", "cr":
				if inParagraph {
					paragraph.WriteByte('\n')
				}
			}
		case xml.EndElement:
			if value.Name.Local != "p" || !inParagraph {
				continue
			}
			inParagraph = false
			text := normalizeVisibleText(paragraph.String())
			if text == "" {
				continue
			}
			if strings.HasPrefix(strings.ToLower(style), "heading") || strings.HasPrefix(strings.ToLower(style), "title") {
				currentHeading = text
				continue
			}
			sections = appendOrMergeSection(sections, currentHeading, text)
		}
	}
	return sections, nil
}

var markdownHeading = regexp.MustCompile(`^(#{1,6})[ \t]+(.+?)\s*$`)

func sectionsFromText(text string, markdown bool) []Section {
	scanner := bufio.NewScanner(strings.NewReader(text))
	scanner.Buffer(make([]byte, 64<<10), MaxSourceBytes)
	sections := make([]Section, 0)
	heading := ""
	var paragraph []string
	flush := func() {
		value := normalizeVisibleText(strings.Join(paragraph, "\n"))
		if value != "" {
			sections = appendOrMergeSection(sections, heading, value)
		}
		paragraph = paragraph[:0]
	}
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if markdown {
			if match := markdownHeading.FindStringSubmatch(line); match != nil {
				flush()
				heading = normalizeVisibleText(match[2])
				continue
			}
		}
		if line == "" {
			flush()
			continue
		}
		paragraph = append(paragraph, line)
	}
	flush()
	return sections
}

func appendOrMergeSection(sections []Section, heading, text string) []Section {
	heading, text = normalizeVisibleText(heading), normalizeVisibleText(text)
	if text == "" {
		return sections
	}
	if len(sections) > 0 && sections[len(sections)-1].Heading == heading {
		sections[len(sections)-1].Text += "\n\n" + text
		return sections
	}
	return append(sections, Section{Heading: heading, Text: text})
}

func normalizeNewlines(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}

func normalizeVisibleText(value string) string {
	value = normalizeNewlines(value)
	lines := strings.Split(value, "\n")
	for index := range lines {
		lines[index] = strings.Join(strings.Fields(lines[index]), " ")
	}
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func validSHA256(value string) bool {
	if len(value) != 71 || !strings.HasPrefix(value, "sha256:") {
		return false
	}
	for _, char := range value[7:] {
		if (char < '0' || char > '9') && (char < 'a' || char > 'f') {
			return false
		}
	}
	return true
}
