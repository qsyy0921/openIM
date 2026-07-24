package knowledgeprojection

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestBuildUsesOneDeterministicTitleContentContract(t *testing.T) {
	first, err := Build(" 第三方安全评估制度 ", " 核心要求包括统一归口管理。 ")
	if err != nil {
		t.Fatal(err)
	}
	second, err := Build("第三方安全评估制度", "核心要求包括统一归口管理。")
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("projection is not deterministic: %#v != %#v", first, second)
	}
	if first.Text != "第三方安全评估制度\n\n核心要求包括统一归口管理。" {
		t.Fatalf("projection text = %q", first.Text)
	}
	for _, term := range []string{"第三", "方安", "统一", "管理"} {
		if !strings.Contains(first.Lexemes, term) {
			t.Fatalf("projection lexemes %q do not contain %q", first.Lexemes, term)
		}
	}
}

func TestBuildRejectsIncompleteOrInvalidInput(t *testing.T) {
	for _, input := range []struct {
		title   string
		content string
	}{
		{"", "content"},
		{"title", ""},
		{"title\x00", "content"},
		{string([]byte{0xff}), "content"},
	} {
		if _, err := Build(input.title, input.content); err == nil {
			t.Fatalf("Build(%q, %q) succeeded", input.title, input.content)
		}
	}
}

func TestRerankerTextKeepsTitleAndValidUTF8WithinLimit(t *testing.T) {
	title := "访问控制制度"
	content := strings.Repeat("权限复核", 3000)
	result, err := RerankerText(title, content, 8000)
	if err != nil {
		t.Fatal(err)
	}
	if len(result) > 8000 || !utf8.ValidString(result) {
		t.Fatalf("invalid bounded reranker projection: bytes=%d valid=%v", len(result), utf8.ValidString(result))
	}
	if !strings.HasPrefix(result, title+"\n\n") {
		t.Fatalf("reranker projection lost its title prefix: %q", result[:min(len(result), 64)])
	}
}
