package knowledgeprojection

import (
	"errors"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	Revision           = "document-title-content-v1"
	HistoricalRevision = "chunk-content-v1"
)

type Projection struct {
	Text    string
	Lexemes string
}

func Build(title, content string) (Projection, error) {
	title = strings.TrimSpace(title)
	content = strings.TrimSpace(content)
	if title == "" || content == "" ||
		!utf8.ValidString(title) || !utf8.ValidString(content) ||
		strings.IndexByte(title, 0) >= 0 || strings.IndexByte(content, 0) >= 0 {
		return Projection{}, errors.New("knowledge retrieval projection input is invalid")
	}
	text := title + "\n\n" + content
	lexemes := lexicalDocument(text)
	if lexemes == "" {
		return Projection{}, errors.New("knowledge retrieval projection has no lexical terms")
	}
	return Projection{Text: text, Lexemes: lexemes}, nil
}

func RerankerText(title, content string, maxBytes int) (string, error) {
	if maxBytes < 1 {
		return "", errors.New("knowledge reranker projection byte limit is invalid")
	}
	projection, err := Build(title, content)
	if err != nil {
		return "", err
	}
	if len(projection.Text) <= maxBytes {
		return projection.Text, nil
	}
	end := maxBytes
	for end > 0 && !utf8.ValidString(projection.Text[:end]) {
		end--
	}
	result := strings.TrimSpace(projection.Text[:end])
	if result == "" {
		return "", errors.New("knowledge reranker projection is empty after truncation")
	}
	return result, nil
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
