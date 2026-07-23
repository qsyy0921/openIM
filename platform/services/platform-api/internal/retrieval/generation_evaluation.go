package retrieval

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"unicode"
)

const (
	GenerationGrounded             = "grounded"
	GenerationInsufficientEvidence = "insufficient_evidence"
)

type GenerationSearcher interface {
	Search(context.Context, Query) ([]Evidence, error)
}

type GenerationProvider interface {
	Generate(context.Context, GenerationRequest) (GenerationCandidate, error)
}

type GenerationRequest struct {
	CaseID   string
	Question string
	Evidence []Evidence
}

type GenerationCandidate struct {
	Text               string   `json:"text"`
	Model              string   `json:"model"`
	ProviderResponseID string   `json:"provider_response_id"`
	CitationIDs        []string `json:"citation_ids"`
	GroundingStatus    string   `json:"grounding_status"`
}

type GenerationEvaluationConfig struct {
	TenantID          string
	MemberID          string
	Model             string
	Seed              string
	AnswerableCases   int
	UnanswerableCases int
	RetrievalLimit    int
}

type GenerationEvaluationReport struct {
	SchemaVersion             int                           `json:"schema_version"`
	EvaluatorVersion          string                        `json:"evaluator_version"`
	Model                     string                        `json:"model"`
	SampleDigest              string                        `json:"sample_digest"`
	Cases                     int                           `json:"cases"`
	AnswerableCases           int                           `json:"answerable_cases"`
	UnanswerableCases         int                           `json:"unanswerable_cases"`
	ModelCalls                int                           `json:"model_calls"`
	ProviderFailures          int                           `json:"provider_failures"`
	RetrievalMisses           int                           `json:"retrieval_misses"`
	GeneratedCandidates       int                           `json:"generated_candidates"`
	RequiredFactMatches       int                           `json:"required_fact_matches"`
	RequiredFactTotal         int                           `json:"required_fact_total"`
	CorrectGeneratedCitations int                           `json:"correct_generated_citations"`
	GeneratedCitations        int                           `json:"generated_citations"`
	CitedExpectedChunks       int                           `json:"cited_expected_chunks"`
	ExpectedCitationChunks    int                           `json:"expected_citation_chunks"`
	CandidateContractSuccess  float64                       `json:"candidate_contract_success_rate"`
	GroundingDecisionAccuracy float64                       `json:"grounding_decision_accuracy"`
	AbstentionAccuracy        float64                       `json:"abstention_accuracy"`
	RequiredFactCoverage      float64                       `json:"required_fact_coverage"`
	CitationPrecision         float64                       `json:"generated_citation_precision"`
	CitationRecall            float64                       `json:"generated_citation_recall"`
	CitationChecksumIntegrity float64                       `json:"citation_checksum_integrity"`
	AnswerCorrectness         float64                       `json:"answer_correctness"`
	Faithfulness              float64                       `json:"faithfulness"`
	CitationSyntaxIntegrity   float64                       `json:"citation_syntax_integrity"`
	EndToEndSuccessRate       float64                       `json:"end_to_end_success_rate"`
	ProductionGateEvaluated   bool                          `json:"production_gate_evaluated"`
	ProductionGatePassed      bool                          `json:"production_gate_passed"`
	Failures                  []GenerationEvaluationFailure `json:"failures"`
}

type GenerationEvaluationFailure struct {
	QAID   string `json:"qa_id"`
	Reason string `json:"reason"`
}

func EvaluateGeneration(ctx context.Context, searcher GenerationSearcher, provider GenerationProvider, cases []QACase, config GenerationEvaluationConfig) (GenerationEvaluationReport, error) {
	if searcher == nil || provider == nil || config.TenantID == "" || config.MemberID == "" || strings.TrimSpace(config.Model) == "" {
		return GenerationEvaluationReport{}, errors.New("generation evaluation dependencies are invalid")
	}
	if config.AnswerableCases < 1 || config.UnanswerableCases < 1 || config.RetrievalLimit < 1 || config.RetrievalLimit > 8 || strings.TrimSpace(config.Seed) == "" {
		return GenerationEvaluationReport{}, errors.New("generation evaluation sample configuration is invalid")
	}
	selected, digest, err := selectGenerationCases(cases, config.AnswerableCases, config.UnanswerableCases, config.Seed)
	if err != nil {
		return GenerationEvaluationReport{}, err
	}
	report := GenerationEvaluationReport{
		SchemaVersion: 1, EvaluatorVersion: "grounded-generation-v1", Model: config.Model,
		SampleDigest: digest, Cases: len(selected), Failures: make([]GenerationEvaluationFailure, 0),
	}
	var decisions, abstentions, matchedFacts, totalFacts, correctCitations, totalCitations float64
	var citedExpected, totalExpected, syntaxValid, checksumValid, checksumTotal float64
	var faithful, generated, successes float64
	for _, item := range selected {
		if item.Answerable {
			report.AnswerableCases++
			totalFacts += float64(len(item.RequiredFacts))
			expectedChunks := make(map[string]struct{}, len(item.Evidence))
			for _, expected := range item.Evidence {
				expectedChunks[expected.ChunkID] = struct{}{}
			}
			totalExpected += float64(len(expectedChunks))
		} else {
			report.UnanswerableCases++
		}
		evidence, searchErr := searcher.Search(ctx, Query{
			TenantID: config.TenantID, MemberID: config.MemberID, Purpose: "agent_answer",
			Text: item.Question, Limit: config.RetrievalLimit,
		})
		if searchErr != nil {
			return report, fmt.Errorf("retrieve generation evaluation case %s: %w", item.QAID, searchErr)
		}
		if len(evidence) == 0 {
			report.RetrievalMisses++
			if !item.Answerable {
				decisions++
				abstentions++
				successes++
			} else {
				report.Failures = append(report.Failures, GenerationEvaluationFailure{QAID: item.QAID, Reason: "retrieval_miss"})
			}
			continue
		}

		candidate, generateErr := provider.Generate(ctx, GenerationRequest{CaseID: item.QAID, Question: item.Question, Evidence: evidence})
		report.ModelCalls++
		if generateErr != nil {
			report.ProviderFailures++
			report.Failures = append(report.Failures, GenerationEvaluationFailure{QAID: item.QAID, Reason: "provider_or_schema_failure"})
			continue
		}
		if candidate.Model != config.Model || candidate.ProviderResponseID == "" || candidate.Text == "" {
			report.ProviderFailures++
			report.Failures = append(report.Failures, GenerationEvaluationFailure{QAID: item.QAID, Reason: "incomplete_candidate"})
			continue
		}
		generated++
		expectedStatus := GenerationInsufficientEvidence
		if item.Answerable {
			expectedStatus = GenerationGrounded
		}
		decisionOK := candidate.GroundingStatus == expectedStatus
		if decisionOK {
			decisions++
		}
		if !item.Answerable && candidate.GroundingStatus == GenerationInsufficientEvidence {
			abstentions++
		}

		factsOK := true
		if item.Answerable {
			for _, fact := range item.RequiredFacts {
				if containsNormalized(candidate.Text, fact) {
					matchedFacts++
				} else {
					factsOK = false
				}
			}
		}
		citationOK, correct, cited, expectedCited, _ := evaluateGeneratedCitations(candidate, evidence, item.Evidence)
		validChecksums, checkedChecksums := evaluateGeneratedCitationChecksums(candidate, evidence)
		checksumValid += float64(validChecksums)
		checksumTotal += float64(checkedChecksums)
		if citationOK {
			syntaxValid++
		}
		faithfulCase := generationCaseFaithful(candidate, evidence, item)
		if faithfulCase {
			faithful++
		}
		if item.Answerable {
			correctCitations += float64(correct)
			totalCitations += float64(cited)
			citedExpected += float64(expectedCited)
		}
		caseSuccess := decisionOK && citationOK && faithfulCase && validChecksums == checkedChecksums
		if item.Answerable {
			caseSuccess = caseSuccess && factsOK && correct > 0
		}
		if caseSuccess {
			successes++
		} else {
			report.Failures = append(report.Failures, GenerationEvaluationFailure{QAID: item.QAID, Reason: generationFailureReason(decisionOK, factsOK, citationOK, correct, item.Answerable)})
		}
	}

	if report.Cases > 0 {
		report.GroundingDecisionAccuracy = decisions / float64(report.Cases)
		report.EndToEndSuccessRate = successes / float64(report.Cases)
	}
	if report.ModelCalls > 0 {
		report.CandidateContractSuccess = generated / float64(report.ModelCalls)
	}
	report.GeneratedCandidates = int(generated)
	report.RequiredFactMatches = int(matchedFacts)
	report.RequiredFactTotal = int(totalFacts)
	report.CorrectGeneratedCitations = int(correctCitations)
	report.GeneratedCitations = int(totalCitations)
	report.CitedExpectedChunks = int(citedExpected)
	report.ExpectedCitationChunks = int(totalExpected)
	if report.UnanswerableCases > 0 {
		report.AbstentionAccuracy = abstentions / float64(report.UnanswerableCases)
	}
	if totalFacts > 0 {
		report.RequiredFactCoverage = matchedFacts / totalFacts
		report.AnswerCorrectness = report.RequiredFactCoverage
	}
	if totalCitations > 0 {
		report.CitationPrecision = correctCitations / totalCitations
	}
	if totalExpected > 0 {
		report.CitationRecall = citedExpected / totalExpected
	}
	if generated > 0 {
		report.CitationSyntaxIntegrity = syntaxValid / generated
		report.Faithfulness = faithful / generated
	}
	if checksumTotal > 0 {
		report.CitationChecksumIntegrity = checksumValid / checksumTotal
	} else if generated > 0 && report.GeneratedCitations == 0 {
		report.CitationChecksumIntegrity = 1
	}
	report.ProductionGateEvaluated = report.Cases >= 120 && config.Model == LockedGenerationModel
	report.ProductionGatePassed = report.ProductionGateEvaluated &&
		report.CandidateContractSuccess == 1 &&
		report.AbstentionAccuracy >= 0.95 &&
		report.CitationPrecision >= 0.95 &&
		report.CitationChecksumIntegrity == 1 &&
		report.Faithfulness >= 0.95
	return report, nil
}

type rankedGenerationCase struct {
	item QACase
	key  string
}

func selectGenerationCases(cases []QACase, answerableCount, unanswerableCount int, seed string) ([]QACase, string, error) {
	answerable := stratifiedGenerationCases(cases, true, answerableCount, seed)
	unanswerable := stratifiedGenerationCases(cases, false, unanswerableCount, seed)
	if len(answerable) != answerableCount || len(unanswerable) != unanswerableCount {
		return nil, "", errors.New("generation evaluation dataset cannot satisfy the requested balance")
	}
	selected := make([]QACase, 0, answerableCount+unanswerableCount)
	for _, item := range answerable {
		selected = append(selected, item.item)
	}
	for _, item := range unanswerable {
		selected = append(selected, item.item)
	}
	sort.Slice(selected, func(i, j int) bool { return selected[i].QAID < selected[j].QAID })
	hash := sha256.New()
	for _, item := range selected {
		_, _ = hash.Write([]byte(item.QAID + "\n"))
	}
	return selected, "sha256:" + hex.EncodeToString(hash.Sum(nil)), nil
}

func stratifiedGenerationCases(cases []QACase, answerable bool, count int, seed string) []rankedGenerationCase {
	groups := make(map[string][]rankedGenerationCase)
	for _, item := range cases {
		if item.Answerable != answerable {
			continue
		}
		digest := sha256.Sum256([]byte(seed + "\x00" + item.QAID))
		stratum := item.DomainCode + "\x00" + item.Type
		groups[stratum] = append(groups[stratum], rankedGenerationCase{
			item: item, key: hex.EncodeToString(digest[:]),
		})
	}
	keys := make([]string, 0, len(groups))
	for key := range groups {
		keys = append(keys, key)
		sort.Slice(groups[key], func(i, j int) bool { return groups[key][i].key < groups[key][j].key })
	}
	sort.Strings(keys)
	selected := make([]rankedGenerationCase, 0, count)
	for round := 0; len(selected) < count; round++ {
		added := false
		for _, key := range keys {
			if round < len(groups[key]) {
				selected = append(selected, groups[key][round])
				added = true
				if len(selected) == count {
					break
				}
			}
		}
		if !added {
			break
		}
	}
	return selected
}

var generatedCitationPattern = regexp.MustCompile(`\[(C[0-9]+)\]`)

func evaluateGeneratedCitations(candidate GenerationCandidate, evidence []Evidence, expected []QAEvidence) (bool, int, int, int, int) {
	available := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		available[item.CitationID] = item
	}
	expectedChunks := make(map[string]struct{}, len(expected))
	for _, item := range expected {
		expectedChunks[item.ChunkID] = struct{}{}
	}
	declared := make(map[string]struct{}, len(candidate.CitationIDs))
	correctChunks := make(map[string]struct{})
	valid := true
	correct := 0
	for _, id := range candidate.CitationIDs {
		if _, duplicate := declared[id]; duplicate {
			continue
		}
		declared[id] = struct{}{}
		item, ok := available[id]
		if !ok || !strings.Contains(candidate.Text, "["+id+"]") {
			valid = false
			continue
		}
		if _, ok := expectedChunks[item.ChunkID]; ok {
			correct++
			correctChunks[item.ChunkID] = struct{}{}
		}
	}
	for _, match := range generatedCitationPattern.FindAllStringSubmatch(candidate.Text, -1) {
		if _, ok := declared[match[1]]; !ok {
			valid = false
		}
	}
	if candidate.GroundingStatus == GenerationGrounded && len(declared) == 0 {
		valid = false
	}
	return valid, correct, len(declared), len(correctChunks), len(expectedChunks)
}

func evaluateGeneratedCitationChecksums(candidate GenerationCandidate, evidence []Evidence) (valid, total int) {
	available := make(map[string]Evidence, len(evidence))
	for _, item := range evidence {
		available[item.CitationID] = item
	}
	seen := make(map[string]struct{}, len(candidate.CitationIDs))
	for _, citationID := range candidate.CitationIDs {
		if _, duplicate := seen[citationID]; duplicate {
			continue
		}
		seen[citationID] = struct{}{}
		item, exists := available[citationID]
		if !exists {
			continue
		}
		total++
		checksum := sha256.Sum256([]byte(item.Content))
		if item.Checksum == fmt.Sprintf("sha256:%x", checksum[:]) {
			valid++
		}
	}
	return valid, total
}

func generationCaseFaithful(candidate GenerationCandidate, evidence []Evidence, item QACase) bool {
	if !item.Answerable {
		return candidate.GroundingStatus == GenerationInsufficientEvidence &&
			len(candidate.CitationIDs) == 0
	}
	available := make(map[string]string, len(evidence))
	for _, item := range evidence {
		available[item.CitationID] = item.Content
	}
	var cited strings.Builder
	for _, citationID := range candidate.CitationIDs {
		content, exists := available[citationID]
		if !exists {
			return false
		}
		cited.WriteString(content)
		cited.WriteByte('\n')
	}
	if cited.Len() == 0 {
		return false
	}
	for _, fact := range item.RequiredFacts {
		if !containsNormalized(candidate.Text, fact) || !containsNormalized(cited.String(), fact) {
			return false
		}
	}
	return true
}

func containsNormalized(text, fact string) bool {
	return strings.Contains(normalizeEvaluationText(text), normalizeEvaluationText(fact))
}

func normalizeEvaluationText(value string) string {
	var result strings.Builder
	for _, char := range strings.ToLower(value) {
		if unicode.IsSpace(char) || unicode.IsPunct(char) || unicode.IsSymbol(char) {
			continue
		}
		result.WriteRune(char)
	}
	return result.String()
}

func generationFailureReason(decisionOK, factsOK, citationOK bool, correct int, answerable bool) string {
	switch {
	case !decisionOK:
		return "grounding_decision"
	case answerable && !factsOK:
		return "required_fact_coverage"
	case !citationOK:
		return "citation_integrity"
	case answerable && correct == 0:
		return "citation_correctness"
	default:
		return "end_to_end_failure"
	}
}
