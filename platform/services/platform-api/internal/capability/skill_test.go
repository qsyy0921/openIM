package capability

import "testing"

func TestSkillDigestIsStableAndScopeAware(t *testing.T) {
	skill := Skill{
		SkillID: "research.paper_summary", Version: "1", Name: "Paper summary",
		Summary: "Summarize a paper", Instructions: "Summarize only the supplied paper.",
		ToolOperations: []string{"mcp.arxiv.search"}, Audience: "passive",
	}
	digest, err := skill.Digest()
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:16798177d97c709374c963e69a87d1552356049062bcf67b2d46ed5bc3edfcde" {
		t.Fatalf("digest=%q", digest)
	}
	skill.ToolOperations = append(skill.ToolOperations, "mcp.arxiv.search")
	if _, err := skill.Digest(); err == nil {
		t.Fatal("duplicate Skill operation accepted")
	}
}
