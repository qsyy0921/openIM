package retrieval

import (
	"reflect"
	"testing"
)

func TestLexicalTermsAreBoundedAndNormalized(t *testing.T) {
	got := lexicalTerms("  OpenIM, openim 权限 检索！x ")
	want := []string{"openim", "权限", "检索"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("lexicalTerms() = %#v", got)
	}
}
