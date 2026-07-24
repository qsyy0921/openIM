package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	ledongpdf "github.com/ledongthuc/pdf"
)

func TestGenerateFixturesProducesManifestAndSearchablePDF(t *testing.T) {
	output := filepath.Join(t.TempDir(), "fixtures")
	if err := generateFixtures(output, "test-batch-1234"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(output, "manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	var manifest fixtureManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatal(err)
	}
	if manifest.SchemaVersion != 1 || len(manifest.Documents) != 4 {
		t.Fatalf("unexpected manifest: %#v", manifest)
	}
	for _, document := range manifest.Documents {
		if _, err := os.Stat(filepath.Join(output, document.Filename)); err != nil {
			t.Fatalf("%s fixture: %v", document.Format, err)
		}
	}
	pdfPath := filepath.Join(output, manifest.Documents[2].Filename)
	file, reader, err := ledongpdf.Open(pdfPath)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	plain, err := reader.GetPlainText()
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(filepath.Join(output, manifest.Documents[0].Filename))
	if err != nil {
		t.Fatal(err)
	}
	if len(content) == 0 || plain == nil {
		t.Fatal("generated fixtures are empty")
	}
}

func TestGenerateFixturesRefusesReuseAndInvalidBatch(t *testing.T) {
	output := filepath.Join(t.TempDir(), "fixtures")
	if err := generateFixtures(output, "short"); err == nil {
		t.Fatal("invalid batch ID was accepted")
	}
	if err := generateFixtures(output, "test-batch-5678"); err != nil {
		t.Fatal(err)
	}
	if err := generateFixtures(output, "test-batch-5678"); err == nil {
		t.Fatal("non-empty fixture directory was reused")
	}
}
