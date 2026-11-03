package main

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidJobID(t *testing.T) {
	if _, ok := validJobID("550e8400-e29b-41d4-a716-446655440000"); !ok {
		t.Fatal("real UUID rejected")
	}
	for _, bad := range []string{"../etc", "abc", "550e8400-e29b-41d4-a716-446655440000/../../etc", ""} {
		if _, ok := validJobID(bad); ok {
			t.Fatalf("bad ID accepted: %q", bad)
		}
	}
}

func TestInspectMXLCountsNotesAndParts(t *testing.T) {
	xml := `<score-partwise><part-list><score-part id="P1"><part-name>Violin I</part-name></score-part></part-list>` +
		`<part id="P1"><measure number="1"><note/><note/></measure><measure number="2"><note/></measure></part></score-partwise>`
	dir := t.TempDir()
	path := filepath.Join(dir, "test.mxl")
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("score.musicxml")
	_, _ = w.Write([]byte(xml))
	_ = zw.Close()
	_ = f.Close()

	notes, measures, parts, err := inspectMXL(path)
	if err != nil {
		t.Fatal(err)
	}
	if notes != 3 || measures != 2 {
		t.Fatalf("got notes=%d measures=%d, want 3/2", notes, measures)
	}
	if len(parts) != 1 || parts[0] != "Violin I" {
		t.Fatalf("got parts %v, want [Violin I]", parts)
	}
}

func TestConversionProgressUsesLastStep(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversion.log")
	content := "Starting\nLOAD done\nBINARY done\nTEXTS: reading text\n"
	if err := os.WriteFile(logPath, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	stage, progress := conversionProgress(logPath)
	if stage != "Reading text (OCR)…" {
		t.Fatalf("got stage %q", stage)
	}
	if progress != 58 { // 10 + 12*4, TEXTS is step 12
		t.Fatalf("got progress %d, want 58", progress)
	}
}

func TestConversionProgressNoSteps(t *testing.T) {
	dir := t.TempDir()
	logPath := filepath.Join(dir, "conversion.log")
	if err := os.WriteFile(logPath, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, progress := conversionProgress(logPath)
	if progress != 0 {
		t.Fatalf("got progress %d, want 0", progress)
	}
}

func TestFindMusicXMLFindsNestedBookFolder(t *testing.T) {
	// Audiveris layout: output/<book>/<book>.mxl, not output/*.mxl.
	dir := t.TempDir()
	book := filepath.Join(dir, "abc")
	if err := os.MkdirAll(book, 0o755); err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(book, "abc.mxl"))
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	w, _ := zw.Create("score.musicxml")
	_, _ = w.Write([]byte(`<score-partwise><part><measure><note/></measure></part></score-partwise>`))
	_ = zw.Close()
	_ = f.Close()

	found := findMusicXML(dir)
	if len(found) != 1 {
		t.Fatalf("got %d files, want 1", len(found))
	}
	if !strings.HasSuffix(found[0].Name, "abc.mxl") {
		t.Fatalf("unexpected name %q", found[0].Name)
	}
	if found[0].Notes != 1 {
		t.Fatalf("got %d notes, want 1", found[0].Notes)
	}
}
