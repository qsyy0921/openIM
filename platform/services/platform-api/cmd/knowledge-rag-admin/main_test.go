package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/qsyy0921/openim/platform/services/platform-api/internal/retrieval"
)

func TestFileSHA256AndStrictReportDecode(t *testing.T) {
	directory := t.TempDir()
	dataPath := filepath.Join(directory, "qa.jsonl")
	if err := os.WriteFile(dataPath, []byte("abc"), 0o600); err != nil {
		t.Fatal(err)
	}
	digest, err := fileSHA256(dataPath)
	if err != nil {
		t.Fatal(err)
	}
	if digest != "sha256:ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad" {
		t.Fatalf("fileSHA256() = %q", digest)
	}

	reportPath := filepath.Join(directory, "report.json")
	if err := os.WriteFile(reportPath, []byte(`{"schema_version":4,"cases":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var report retrieval.EvaluationReport
	if err := decodeReport(reportPath, &report); err != nil || report.SchemaVersion != 4 {
		t.Fatalf("decodeReport() = %#v, %v", report, err)
	}
	if err := os.WriteFile(reportPath, []byte(`{"schema_version":4} {}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := decodeReport(reportPath, &report); err == nil {
		t.Fatal("trailing JSON value was accepted")
	}
}

func TestResolveGenerationURLUsesExplicitEndpointWithoutRuntimeFallback(t *testing.T) {
	resolved, err := resolveGenerationURL("http://127.0.0.1:18082", "http://127.0.0.1:18083")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "http://127.0.0.1:18082" {
		t.Fatalf("resolveGenerationURL() = %q", resolved)
	}
	resolved, err = resolveGenerationURL("", "http://127.0.0.1:18083")
	if err != nil {
		t.Fatal(err)
	}
	if resolved != "http://127.0.0.1:18083" {
		t.Fatalf("resolveGenerationURL() = %q", resolved)
	}
	if _, err := resolveGenerationURL("", ""); err == nil {
		t.Fatal("missing generation endpoint was accepted")
	}
}
