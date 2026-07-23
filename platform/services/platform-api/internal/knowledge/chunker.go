package knowledge

import (
	"crypto/sha256"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/google/uuid"
)

const maxChunksPerVersion = 10_000

var sentenceBoundary = regexp.MustCompile(`(?m)([.!?。！？；;]+["'”’）)]*)\s*`)

func ChunkSections(versionID string, sections []Section) ([]Chunk, error) {
	namespace, err := uuid.Parse(versionID)
	if err != nil {
		return nil, fmt.Errorf("%w: version ID is not a UUID", ErrInvalidInput)
	}
	result := make([]Chunk, 0)
	for _, section := range sections {
		heading := normalizeVisibleText(section.Heading)
		body := normalizeVisibleText(section.Text)
		if body == "" {
			continue
		}
		prefix := ""
		if heading != "" {
			prefix = heading + "\n\n"
		}
		if utf8.RuneCountInString(prefix) >= MaxChunkRunes || len(prefix) >= MaxChunkBytes {
			return nil, processingError("CHUNK_HEADING_TOO_LARGE", "section heading exceeds the chunk budget", false)
		}
		segments := splitSectionBody(body, MaxChunkRunes-utf8.RuneCountInString(prefix), MaxChunkBytes-len(prefix))
		if len(segments) == 0 {
			continue
		}
		var current string
		flush := func() error {
			content := normalizeVisibleText(prefix + current)
			if content == "" {
				return nil
			}
			if utf8.RuneCountInString(content) > MaxChunkRunes || len(content) > MaxChunkBytes {
				return processingError("CHUNK_BUDGET_EXCEEDED", "chunk exceeds the configured size budget", false)
			}
			if len(result) > 0 && result[len(result)-1].Content == content {
				return processingError("CHUNK_DUPLICATE", "adjacent chunks are identical", false)
			}
			checksum := checksumText(content)
			ordinal := len(result)
			id := uuid.NewSHA1(namespace, []byte(fmt.Sprintf("%d\x00%s", ordinal, checksum))).String()
			result = append(result, Chunk{
				ID: id, Ordinal: ordinal, Content: content, Checksum: checksum, Lexemes: lexicalDocument(content),
			})
			if len(result) > maxChunksPerVersion {
				return processingError("CHUNK_COUNT_EXCEEDED", "document produces too many chunks", false)
			}
			return nil
		}
		for _, segment := range segments {
			candidate := segment
			if current != "" {
				candidate = current + "\n\n" + segment
			}
			if fitsChunk(prefix + candidate) {
				current = candidate
				continue
			}
			previous := current
			if err := flush(); err != nil {
				return nil, err
			}
			overlap := trailingRunes(previous, ChunkOverlap)
			current = segment
			if overlap != "" && fitsChunk(prefix+overlap+"\n\n"+segment) {
				current = overlap + "\n\n" + segment
			}
		}
		if current != "" {
			if err := flush(); err != nil {
				return nil, err
			}
		}
	}
	if len(result) == 0 {
		return nil, processingError("CHUNK_EMPTY", "document produces no searchable chunks", false)
	}
	return result, nil
}

func splitSectionBody(body string, maxRunes, maxBytes int) []string {
	paragraphs := strings.Split(body, "\n\n")
	result := make([]string, 0, len(paragraphs))
	for _, paragraph := range paragraphs {
		paragraph = normalizeVisibleText(paragraph)
		if paragraph == "" {
			continue
		}
		if utf8.RuneCountInString(paragraph) <= maxRunes && len(paragraph) <= maxBytes {
			result = append(result, paragraph)
			continue
		}
		sentences := splitSentences(paragraph)
		var current string
		flush := func() {
			if current != "" {
				result = append(result, current)
				current = ""
			}
		}
		for _, sentence := range sentences {
			if utf8.RuneCountInString(sentence) > maxRunes || len(sentence) > maxBytes {
				flush()
				result = append(result, splitByBudget(sentence, maxRunes, maxBytes)...)
				continue
			}
			candidate := sentence
			if current != "" {
				candidate = current + " " + sentence
			}
			if utf8.RuneCountInString(candidate) <= maxRunes && len(candidate) <= maxBytes {
				current = candidate
			} else {
				flush()
				current = sentence
			}
		}
		flush()
	}
	return result
}

func splitSentences(value string) []string {
	indices := sentenceBoundary.FindAllStringIndex(value, -1)
	if len(indices) == 0 {
		return []string{value}
	}
	result := make([]string, 0, len(indices)+1)
	start := 0
	for _, index := range indices {
		end := index[1]
		if sentence := strings.TrimSpace(value[start:end]); sentence != "" {
			result = append(result, sentence)
		}
		start = end
	}
	if tail := strings.TrimSpace(value[start:]); tail != "" {
		result = append(result, tail)
	}
	return result
}

func splitByBudget(value string, maxRunes, maxBytes int) []string {
	runes := []rune(value)
	result := make([]string, 0, (len(runes)+maxRunes-1)/maxRunes)
	for len(runes) > 0 {
		end := min(maxRunes, len(runes))
		for end > 0 && len(string(runes[:end])) > maxBytes {
			end--
		}
		if end == 0 {
			return nil
		}
		part := strings.TrimSpace(string(runes[:end]))
		if part != "" {
			result = append(result, part)
		}
		runes = runes[end:]
	}
	return result
}

func fitsChunk(value string) bool {
	return utf8.RuneCountInString(value) <= MaxChunkRunes && len(value) <= MaxChunkBytes
}

func trailingRunes(value string, count int) string {
	runes := []rune(strings.TrimSpace(value))
	if len(runes) <= count {
		return string(runes)
	}
	return strings.TrimSpace(string(runes[len(runes)-count:]))
}

func checksumText(value string) string {
	digest := sha256.Sum256([]byte(value))
	return fmt.Sprintf("sha256:%x", digest)
}

func lexicalDocument(value string) string {
	seen := make(map[string]struct{})
	terms := make([]string, 0, 128)
	add := func(term string) {
		term = strings.ToLower(strings.TrimSpace(term))
		if term == "" {
			return
		}
		if _, exists := seen[term]; exists {
			return
		}
		seen[term] = struct{}{}
		terms = append(terms, term)
	}
	fields := strings.FieldsFunc(value, func(char rune) bool {
		return unicode.IsSpace(char) || unicode.IsPunct(char) || unicode.IsSymbol(char)
	})
	for _, field := range fields {
		var latin strings.Builder
		var han []rune
		flushLatin := func() {
			if utf8.RuneCountInString(latin.String()) >= 2 {
				add(latin.String())
			}
			latin.Reset()
		}
		for _, char := range field {
			switch {
			case unicode.Is(unicode.Han, char):
				flushLatin()
				han = append(han, char)
			case unicode.IsLetter(char) || unicode.IsDigit(char):
				latin.WriteRune(char)
			default:
				flushLatin()
			}
		}
		flushLatin()
		for index := 0; index+1 < len(han); index++ {
			add(string(han[index : index+2]))
		}
		if len(han) == 1 {
			add(string(han))
		}
	}
	sort.Strings(terms)
	return strings.Join(terms, " ")
}
