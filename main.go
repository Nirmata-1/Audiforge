package main

// Audiforge: PDF sheet music -> MusicXML, via Audiveris.
//
// Improvements over the original (April 2025) version, Nov 2026:
//   - Audiveris is run as an installed binary (AUDIVERIS_BIN) instead of
//     `gradle run` on every job, with a gradle fallback for local dev.
//     Gradle startup alone used to dominate a 1-page conversion.
//   - English OCR is requested explicitly: -transcribe plus the
//     Language.defaultSpecification constant (default "eng"). Without
//     eng.traineddata, Audiveris still recognises notes but silently
//     drops titles, lyrics and other text; a failure to initialise
//     Tesseract is now surfaced in the job status instead of swallowed.
//   - Audiveris writes a book's output into a sub-folder of the -output
//     base (output/<book>/<book>.mxl). The old code only globbed
//     output/*.mxl, so a successful conversion could be reported as
//     "no movements generated". Output is now found recursively.
//   - The converted MusicXML is actually parsed: note/measure counts and
//     part names are reported, so "completed" means something checkable.
//   - Concurrency is capped (default 1 job; Audiveris wants ~3 GB), each
//     job has a timeout, uploads are size- and type-checked, job IDs are
//     validated as UUIDs, and the ZIP is built in Go instead of by an
//     external `zip` binary that appended to a stale archive.
//   - The cleanup routine actually runs now. It used to return early
//     unless LOG=debug, so /tmp filled up in normal operation.
//   - Directories, port, template dir and the Audiveris location honour
//     the environment variables the Dockerfile already set (UPLOAD_DIR,
//     DOWNLOAD_DIR, AUDIVERIS_HOME, AUDIVERIS_BIN) instead of ignoring
//     them for hardcoded paths. That also makes local runs work: the
//     template path falls back to ./templates when /app/templates
//     does not exist.

import (
	"archive/zip"
	"compress/flate"
	"context"
	"encoding/json"
	"encoding/xml"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

type config struct {
	Port              string
	UploadDir         string
	DownloadDir       string
	AudiverisDir      string
	AudiverisBin      string
	TemplateDir       string
	TessdataDir       string
	OCRLang           string
	MaxUploadBytes    int64
	ConversionTimeout time.Duration
	MaxJobs           int
	Debug             bool
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func loadConfig() config {
	maxMB, err := strconv.ParseInt(envOr("MAX_UPLOAD_MB", "50"), 10, 64)
	if err != nil || maxMB <= 0 {
		maxMB = 50
	}
	timeoutMin, err := strconv.Atoi(envOr("CONVERSION_TIMEOUT_MIN", "20"))
	if err != nil || timeoutMin <= 0 {
		timeoutMin = 20
	}
	maxJobs, err := strconv.Atoi(envOr("AUDIVERIS_JOBS", "1"))
	if err != nil || maxJobs <= 0 {
		maxJobs = 1
	}
	audiverisDir := envOr("AUDIVERIS_HOME", "")
	if audiverisDir == "" {
		// Local checkout layout first, then the container layout.
		if _, err := os.Stat("./audiveris"); err == nil {
			audiverisDir = "./audiveris"
		} else {
			audiverisDir = "/app/audiveris"
		}
	}
	templateDir := envOr("TEMPLATE_DIR", "")
	if templateDir == "" {
		if _, err := os.Stat("./templates"); err == nil {
			templateDir = "./templates"
		} else {
			templateDir = "/app/templates"
		}
	}
	return config{
		Port:              envOr("PORT", "8080"),
		UploadDir:         envOr("UPLOAD_DIR", "/tmp/uploads"),
		DownloadDir:       envOr("DOWNLOAD_DIR", "/tmp/downloads"),
		AudiverisDir:      audiverisDir,
		AudiverisBin:      os.Getenv("AUDIVERIS_BIN"), // empty = auto-detect, then gradle fallback
		TemplateDir:       templateDir,
		TessdataDir:       os.Getenv("TESSDATA_PREFIX"),
		OCRLang:           envOr("AUDIVERIS_OCR_LANG", "eng"),
		MaxUploadBytes:    maxMB << 20,
		ConversionTimeout: time.Duration(timeoutMin) * time.Minute,
		MaxJobs:           maxJobs,
		Debug:             os.Getenv("LOG") == "debug",
	}
}

type outputFileInfo struct {
	Name     string   `json:"name"`
	Size     int64    `json:"size"`
	Notes    int      `json:"notes,omitempty"`
	Measures int      `json:"measures,omitempty"`
	Parts    []string `json:"parts,omitempty"`
}

type jobOptions struct {
	OCRLang         string `json:"ocrLang"`
	Sheets          string `json:"sheets,omitempty"`
	IncludeMusicXML bool   `json:"includeMusicxml,omitempty"`
	IncludeOMR      bool   `json:"includeOmr,omitempty"`
	AutoDownload    bool   `json:"autoDownload,omitempty"`
}

type processingStatus struct {
	Status        string           `json:"status"`
	Message       string           `json:"message"`
	Timestamp     int64            `json:"timestamp"`
	MovementCount int              `json:"movementCount,omitempty"`
	Files         []outputFileInfo `json:"files,omitempty"`
	Warning       string           `json:"warning,omitempty"`
	LogTail       string           `json:"logTail,omitempty"`
	Progress      int              `json:"progress,omitempty"`
	Stage         string           `json:"stage,omitempty"`
	Options       *jobOptions      `json:"options,omitempty"`
}

var (
	cfg        config
	processing sync.Map
	templates  *template.Template
	jobSlots   chan struct{}
)

func main() {
	cfg = loadConfig()
	jobSlots = make(chan struct{}, cfg.MaxJobs)
	if err := os.MkdirAll(cfg.UploadDir, 0o755); err != nil {
		log.Fatalf("upload dir: %v", err)
	}
	if err := os.MkdirAll(cfg.DownloadDir, 0o755); err != nil {
		log.Fatalf("download dir: %v", err)
	}
	templates = template.Must(template.ParseGlob(filepath.Join(cfg.TemplateDir, "*.html")))
	go startCleanupRoutine()

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/healthz", healthHandler)
	mux.HandleFunc("/upload", uploadHandler)
	mux.HandleFunc("/status/", statusHandler)
	mux.HandleFunc("/download/", downloadHandler)
	mux.HandleFunc("/log/", logHandler)
	mux.Handle("/static/", http.StripPrefix("/static/", http.FileServer(http.Dir(cfg.TemplateDir))))

	log.Printf("Audiforge listening on :%s (audiveris: %s, jobs: %d)", cfg.Port, audiverisCommandDesc(), cfg.MaxJobs)
	log.Fatal(http.ListenAndServe(":"+cfg.Port, mux))
}

func audiverisCommandDesc() string {
	if bin, _ := findAudiverisBin(); bin != "" {
		return bin
	}
	return "gradle run in " + cfg.AudiverisDir + " (fallback)"
}

// findAudiverisBin returns a runnable Audiveris binary, or "".
func findAudiverisBin() (string, bool) {
	candidates := []string{}
	if cfg.AudiverisBin != "" {
		candidates = append(candidates, cfg.AudiverisBin)
	}
	// Install layouts, newest first.
	candidates = append(candidates,
		"/opt/audiveris/bin/Audiveris",
		filepath.Join(cfg.AudiverisDir, "app", "build", "install", "app", "bin", "Audiveris"),
		filepath.Join(cfg.AudiverisDir, "build", "install", "app", "bin", "Audiveris"),
	)
	for _, c := range candidates {
		if info, err := os.Stat(c); err == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return c, true
		}
	}
	return "", false
}

func startCleanupRoutine() {
	for {
		time.Sleep(1 * time.Hour)
		cleanupFiles()
	}
}

func cleanupFiles() {
	if cfg.Debug {
		log.Println("Starting cleanup routine...")
	}
	cleanOldUploads()
	cleanOldDownloads()
}

func cleanOldUploads() {
	entries, err := os.ReadDir(cfg.UploadDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 1*time.Hour {
			_ = os.Remove(filepath.Join(cfg.UploadDir, e.Name()))
		}
	}
}

func cleanOldDownloads() {
	entries, err := os.ReadDir(cfg.DownloadDir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err == nil && time.Since(info.ModTime()) > 1*time.Hour {
			_ = os.RemoveAll(filepath.Join(cfg.DownloadDir, e.Name()))
		}
	}
}

func indexHandler(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" && r.URL.Path != "/index.html" {
		http.NotFound(w, r)
		return
	}
	if err := templates.ExecuteTemplate(w, "index.html", nil); err != nil {
		http.Error(w, "Template error", http.StatusInternalServerError)
	}
}

func healthHandler(w http.ResponseWriter, r *http.Request) {
	bin, ok := findAudiverisBin()
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"ok":           true,
		"audiverisBin": bin,
		"audiverisVia": map[bool]string{true: "binary", false: "gradle"}[ok],
		"ocrLang":      cfg.OCRLang,
	})
}

// validJobID rejects path traversal and anything that is not a UUID
// issued by /upload. The old handlers fed the raw path straight into
// filepath.Join, so /download/../../... could escape the download dir.
func validJobID(raw string) (string, bool) {
	id := strings.Trim(raw, "/")
	if _, err := uuid.Parse(id); err != nil {
		return "", false
	}
	return id, true
}

func uploadHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	// Leave a little headroom so an over-limit file is detected, not truncated silently.
	r.Body = http.MaxBytesReader(w, r.Body, cfg.MaxUploadBytes+1024)
	if err := r.ParseMultipartForm(cfg.MaxUploadBytes + (1 << 20)); err != nil {
		http.Error(w, "File too large or invalid form", http.StatusBadRequest)
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "Invalid file", http.StatusBadRequest)
		return
	}
	defer file.Close()

	if !strings.EqualFold(filepath.Ext(header.Filename), ".pdf") {
		http.Error(w, "Only PDF files allowed", http.StatusBadRequest)
		return
	}
	// User options (all validated; bad values are a 400, not a
	// surprise passed through to Audiveris).
	opts := jobOptions{
		OCRLang:         r.FormValue("ocr_lang"),
		Sheets:          strings.ReplaceAll(r.FormValue("sheets"), " ", ""),
		IncludeMusicXML: r.FormValue("include_musicxml") == "on",
		IncludeOMR:      r.FormValue("include_omr") == "on",
		AutoDownload:    r.FormValue("auto_download") == "on",
	}
	if opts.OCRLang == "" {
		opts.OCRLang = "eng"
	}
	switch opts.OCRLang {
	case "eng", "fra", "deu", "ita", "spa", "off":
	default:
		http.Error(w, "Unknown OCR language", http.StatusBadRequest)
		return
	}
	if opts.Sheets != "" {
		ok, _ := regexp.MatchString(`^\d+(-\d+)?(,\d+(-\d+)?)*$`, opts.Sheets)
		if !ok {
			http.Error(w, "Pages must look like 1, 3-5, or be blank for all pages", http.StatusBadRequest)
			return
		}
	}

	// Check the magic bytes, not just the extension.
	head := make([]byte, 5)
	if _, err := io.ReadFull(file, head); err != nil || string(head) != "%PDF-" {
		http.Error(w, "File does not look like a PDF", http.StatusBadRequest)
		return
	}
	// Reassemble the full stream for saving (header bytes + rest).
	body := io.MultiReader(strings.NewReader(string(head)), file)

	id := uuid.New().String()
	uploadPath := filepath.Join(cfg.UploadDir, id+".pdf")
	out, err := os.Create(uploadPath)
	if err != nil {
		http.Error(w, "Failed to save file", http.StatusInternalServerError)
		return
	}
	written, err := io.Copy(out, io.LimitReader(body, cfg.MaxUploadBytes+1))
	_ = out.Close()
	if err != nil || written > cfg.MaxUploadBytes {
		_ = os.Remove(uploadPath)
		http.Error(w, "File too large", http.StatusRequestEntityTooLarge)
		return
	}

	processing.Store(id, processingStatus{
		Status:    "processing",
		Message:   "File uploaded, starting conversion",
		Timestamp: time.Now().Unix(),
		Options:   &opts,
	})
	go processFile(id, uploadPath, opts)

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"id": id})
}

func processFile(id, inputPath string, opts jobOptions) {
	select {
	case jobSlots <- struct{}{}:
		defer func() { <-jobSlots }()
	default:
		// Semaphore is full; block instead of spawning an unbounded
		// pile of 3 GB JVMs. This wait is bounded by the job timeout
		// of the jobs ahead of it only in practice; jobs are FIFO-ish
		// enough for a single-user service, and the status page says
		// "queued" while waiting.
		processing.Store(id, processingStatus{
			Status: "processing", Message: "Queued, waiting for a free conversion slot",
			Timestamp: time.Now().Unix(),
		})
		jobSlots <- struct{}{}
		defer func() { <-jobSlots }()
	}

	ctx, cancel := context.WithTimeout(context.Background(), cfg.ConversionTimeout)
	defer cancel()

	outputDir := filepath.Join(cfg.DownloadDir, id)
	if err := os.MkdirAll(outputDir, 0o755); err != nil {
		failJob(id, fmt.Sprintf("Failed to create output directory: %v", err))
		return
	}

	logPath := filepath.Join(outputDir, "conversion.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		failJob(id, fmt.Sprintf("Failed to create log file: %v", err))
		return
	}
	defer logFile.Close()

	var logWriter io.Writer = logFile
	if cfg.Debug {
		logWriter = io.MultiWriter(logFile, os.Stdout)
	}

	processing.Store(id, processingStatus{
		Status: "processing", Message: "Converting PDF to MusicXML",
		Timestamp: time.Now().Unix(), Options: &opts,
	})
	log.Printf("=== START processing %s ===", id)
	defer log.Printf("=== END processing %s ===", id)

	cmd := buildAudiverisCommand(ctx, outputDir, inputPath, opts)
	cmd.Stdout = logWriter
	cmd.Stderr = logWriter
	if cfg.TessdataDir != "" {
		cmd.Env = append(os.Environ(), "TESSDATA_PREFIX="+cfg.TessdataDir)
	}
	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		runErr = fmt.Errorf("conversion timed out after %s", cfg.ConversionTimeout)
	}

	files := findMusicXML(outputDir)
	if len(files) == 0 {
		msg := "Conversion failed - no MusicXML generated"
		if runErr != nil {
			msg += fmt.Sprintf(" (error: %v)", runErr)
		}
		st := processingStatus{Status: "error", Message: msg, Timestamp: time.Now().Unix()}
		if tail := logTail(logPath); tail != "" {
			st.LogTail = tail
		}
		processing.Store(id, st)
		return
	}

	if opts.IncludeMusicXML {
		files = append(files, extractPlainMusicXML(outputDir, files)...)
	}
	st := processingStatus{
		Status: "completed", Message: "Conversion completed",
		Timestamp: time.Now().Unix(), Files: files,
		Progress: 100, Stage: "Done", Options: &opts,
	}
	// MovementCount counts .mxl movements, not the extra .musicxml
	// copies appended above.
	mxlCount := 0
	for _, f := range files {
		if strings.EqualFold(filepath.Ext(f.Name), ".mxl") {
			mxlCount++
		}
	}
	st.MovementCount = mxlCount
	if runErr != nil {
		st.Message = fmt.Sprintf("Conversion completed with errors (%v)", runErr)
	}
	if warn := ocrWarning(logPath); warn != "" {
		st.Warning = warn
	}
	processing.Store(id, st)
}

func failJob(id, msg string) {
	log.Printf("Job %s failed: %s", id, msg)
	processing.Store(id, processingStatus{Status: "error", Message: msg, Timestamp: time.Now().Unix()})
}

// buildAudiverisCommand prefers the installed binary. The gradle path
// is kept for local development, where no install exists yet.
func buildAudiverisCommand(ctx context.Context, outputDir, inputPath string, opts jobOptions) *exec.Cmd {
	lang := opts.OCRLang
	if lang == "" {
		lang = cfg.OCRLang
	}
	args := []string{"-batch", "-transcribe", "-export"}
	if opts.IncludeOMR {
		// -save writes the .omr project file, which the download
		// then includes. Without it, Audiveris only writes .mxl.
		args = append(args, "-save")
	}
	args = append(args, "-output", outputDir)
	if lang != "" && lang != "off" {
		// English by default: this is what makes titles, lyrics
		// and other text actually get recognised, instead of
		// Audiveris falling back to whatever language data it
		// happens to find (often none).
		args = append(args, "-constant",
			"org.audiveris.omr.text.Language.defaultSpecification="+lang)
	}
	if opts.Sheets != "" {
		// Binary CLI: -sheets takes space-separated page numbers
		// and ranges as separate arguments. In the gradle fallback
		// below they become comma-separated elements, which is
		// the closest equivalent in that form.
		args = append(args, "-sheets")
		args = append(args, strings.Split(opts.Sheets, ",")...)
	}
	args = append(args, "--", inputPath)

	if bin, ok := findAudiverisBin(); ok {
		return exec.CommandContext(ctx, bin, args...)
	}
	// Gradle fallback: Audiveris's own documented form is a single
	// comma-separated -PcmdLineArgs value. Paths here are UUID-based,
	// so they contain no spaces or commas to escape.
	gradleArgs := []string{"run", "-PjvmLineArgs=-Xmx3g",
		"-PcmdLineArgs=" + strings.Join(args, ",")}
	cmd := exec.CommandContext(ctx, "gradle", gradleArgs...)
	cmd.Dir = cfg.AudiverisDir
	return cmd
}

// findMusicXML walks the output dir recursively. Audiveris stores a
// book under output/<book>/, not directly in the output base.
func findMusicXML(outputDir string) []outputFileInfo {
	var out []outputFileInfo
	_ = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".mxl") {
			return nil
		}
		rel, _ := filepath.Rel(outputDir, path)
		fi := outputFileInfo{Name: rel, Size: info.Size()}
		if notes, measures, parts, err := inspectMXL(path); err == nil {
			fi.Notes, fi.Measures, fi.Parts = notes, measures, parts
		}
		out = append(out, fi)
		return nil
	})
	return out
}

// findRootXMLFile returns the score entry inside a .mxl zip: usually
// *.xml or *.musicxml, never META-INF/container.xml.
func findRootXMLFile(zr *zip.Reader) *zip.File {
	for _, f := range zr.File {
		name := strings.ToLower(f.Name)
		if strings.HasPrefix(name, "meta-inf/") {
			continue
		}
		if strings.HasSuffix(name, ".xml") || strings.HasSuffix(name, ".musicxml") {
			return f
		}
	}
	return nil
}

// inspectMXL opens a compressed MusicXML file, finds its root .xml
// entry, and counts notes/measures and part names with a streaming
// decoder (no full MusicXML schema needed).
func inspectMXL(path string) (notes, measures int, parts []string, err error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return 0, 0, nil, err
	}
	defer zr.Close()
	xmlFile := findRootXMLFile(&zr.Reader)
	if xmlFile == nil {
		return 0, 0, nil, fmt.Errorf("no MusicXML entry in %s", path)
	}
	rc, err := xmlFile.Open()
	if err != nil {
		return 0, 0, nil, err
	}
	defer rc.Close()
	return inspectMusicXMLStream(rc)
}

// extractPlainMusicXML unzips each .mxl's root score to a plain
// .musicxml file next to it, so the "also include MusicXML" option
// can offer an uncompressed file MuseScore/Finale can open directly.
// It returns info entries for the files it created.
func extractPlainMusicXML(outputDir string, mxlFiles []outputFileInfo) []outputFileInfo {
	var out []outputFileInfo
	for _, f := range mxlFiles {
		if !strings.EqualFold(filepath.Ext(f.Name), ".mxl") {
			continue
		}
		mxlPath := filepath.Join(outputDir, f.Name)
		xmlPath := strings.TrimSuffix(mxlPath, filepath.Ext(mxlPath)) + ".musicxml"
		zr, err := zip.OpenReader(mxlPath)
		if err != nil {
			continue
		}
		xmlFile := findRootXMLFile(&zr.Reader)
		if xmlFile == nil {
			_ = zr.Close()
			continue
		}
		rc, err := xmlFile.Open()
		if err != nil {
			_ = zr.Close()
			continue
		}
		dst, err := os.Create(xmlPath)
		if err != nil {
			_ = rc.Close()
			_ = zr.Close()
			continue
		}
		_, _ = io.Copy(dst, rc)
		_ = dst.Close()
		_ = rc.Close()
		_ = zr.Close()
		info, err := os.Stat(xmlPath)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(outputDir, xmlPath)
		fi := outputFileInfo{Name: rel, Size: info.Size(), Notes: f.Notes, Measures: f.Measures, Parts: f.Parts}
		out = append(out, fi)
	}
	return out
}

// Audiveris sheet steps, in pipeline order (see its CLI docs). The
// conversion log mentions the current step, so the last step name
// seen in the log is a genuine progress signal — not a timer.
var audiverisSteps = []string{
	"LOAD", "BINARY", "SCALE", "GRID", "HEADERS", "STEM_SEEDS",
	"BEAMS", "LEDGERS", "HEADS", "STEMS", "REDUCTION", "CUE_BEAMS",
	"TEXTS", "MEASURES", "CHORDS", "CURVES", "SYMBOLS", "LINKS",
	"RHYTHMS", "PAGE",
}

var audiverisStepNames = map[string]string{
	"LOAD": "Loading page", "BINARY": "Binarising", "SCALE": "Measuring staff scale",
	"GRID": "Finding staves and barlines", "HEADERS": "Reading clefs, keys and time",
	"STEM_SEEDS": "Finding stems", "BEAMS": "Finding beams", "LEDGERS": "Finding ledger lines",
	"HEADS": "Finding note heads", "STEMS": "Linking stems", "REDUCTION": "Resolving conflicts",
	"CUE_BEAMS": "Finding cue beams", "TEXTS": "Reading text (OCR)",
	"MEASURES": "Finding measures", "CHORDS": "Grouping chords",
	"CURVES": "Finding slurs and wedges", "SYMBOLS": "Finding symbols",
	"LINKS": "Linking symbols", "RHYTHMS": "Working out rhythms",
	"PAGE": "Connecting systems",
}

func conversionProgress(logPath string) (stage string, progress int) {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return "Starting Audiveris…", 0
	}
	if len(data) > 2<<20 {
		data = data[len(data)-(2<<20):]
	}
	re := regexp.MustCompile(`\b(LOAD|BINARY|SCALE|GRID|HEADERS|STEM_SEEDS|BEAMS|LEDGERS|HEADS|STEMS|REDUCTION|CUE_BEAMS|TEXTS|MEASURES|CHORDS|CURVES|SYMBOLS|LINKS|RHYTHMS|PAGE)\b`)
	matches := re.FindAllString(string(data), -1)
	if len(matches) == 0 {
		return "Converting…", 0
	}
	last := matches[len(matches)-1]
	for i, s := range audiverisSteps {
		if s == last {
			name := audiverisStepNames[s]
			if name == "" {
				name = s
			}
			// 10% for startup, then ~4% per step, leaving the
			// last 10% for export — the bar only hits 100 when
			// the job actually completes.
			return name + "…", 10 + i*4
		}
	}
	return "Converting…", 0
}

func inspectMusicXMLStream(r io.Reader) (notes, measures int, parts []string, err error) {
	dec := xml.NewDecoder(r)
	inPartName := false
	seen := map[string]bool{}
	for {
		tok, err := dec.Token()
		if err != nil {
			if err == io.EOF {
				return notes, measures, parts, nil
			}
			return notes, measures, parts, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			switch t.Name.Local {
			case "note":
				notes++
			case "measure":
				measures++
			case "part-name":
				inPartName = true
			}
		case xml.CharData:
			if inPartName {
				name := strings.TrimSpace(string(t))
				if name != "" && !seen[name] {
					seen[name] = true
					parts = append(parts, name)
				}
			}
		case xml.EndElement:
			if t.Name.Local == "part-name" {
				inPartName = false
			}
		}
	}
}

// ocrWarning scans the engine log for the tell-tale Tesseract failure.
// Without eng.traineddata Audiveris succeeds on notes and silently
// drops text, which is exactly the "English transcription missing"
// symptom.
func ocrWarning(logPath string) string {
	data, err := os.ReadFile(logPath)
	if err != nil {
		return ""
	}
	s := string(data)
	if strings.Contains(s, "Could not initialize TessBaseAPI") ||
		strings.Contains(s, "Could not initialize Tesseract") {
		return "English OCR data was not usable (Tesseract failed to initialise): notes were converted, but titles, lyrics and other text may be missing. Install legacy eng.traineddata and set TESSDATA_PREFIX."
	}
	return ""
}

func logTail(logPath string) string {
	data, err := os.ReadFile(logPath)
	if err != nil || len(data) == 0 {
		return ""
	}
	const max = 2000
	if len(data) > max {
		data = data[len(data)-max:]
	}
	return string(data)
}

func statusHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := validJobID(strings.TrimPrefix(r.URL.Path, "/status/"))
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}
	raw, ok := processing.Load(id)
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}
	status := raw.(processingStatus)
	if status.Status == "processing" {
		// Live progress: derived from the engine log, not a timer.
		// If the log has no step names yet, Progress stays 0 and
		// the client creeps the bar toward 95% until done.
		stage, progress := conversionProgress(filepath.Join(cfg.DownloadDir, id, "conversion.log"))
		status.Stage = stage
		status.Progress = progress
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(status)
}

func logHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := validJobID(strings.TrimPrefix(r.URL.Path, "/log/"))
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}
	if _, ok := processing.Load(id); !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	http.ServeFile(w, r, filepath.Join(cfg.DownloadDir, id, "conversion.log"))
}

func downloadHandler(w http.ResponseWriter, r *http.Request) {
	id, ok := validJobID(strings.TrimPrefix(r.URL.Path, "/download/"))
	if !ok {
		http.Error(w, "Invalid ID", http.StatusNotFound)
		return
	}
	outputDir := filepath.Join(cfg.DownloadDir, id)

	var files []string
	_ = filepath.Walk(outputDir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext == ".mxl" || ext == ".musicxml" || ext == ".omr" {
			files = append(files, path)
		}
		return nil
	})
	if len(files) == 0 {
		http.Error(w, "No movements found", http.StatusNotFound)
		return
	}

	// Build a fresh ZIP in Go (the old code shelled out to `zip`,
	// which appended to an existing archive on repeat downloads).
	tmpZip := filepath.Join(outputDir, "converted.zip.tmp")
	zipPath := filepath.Join(outputDir, "converted.zip")
	zf, err := os.Create(tmpZip)
	if err != nil {
		http.Error(w, "Failed to create ZIP archive", http.StatusInternalServerError)
		return
	}
	zw := zip.NewWriter(zf)
	zw.RegisterCompressor(zip.Deflate, func(w io.Writer) (io.WriteCloser, error) {
		return flate.NewWriter(w, flate.DefaultCompression)
	})
	for _, f := range files {
		rel, _ := filepath.Rel(outputDir, f)
		rel = filepath.ToSlash(rel)
		hdr := &zip.FileHeader{Name: rel, Method: zip.Deflate}
		dst, err := zw.CreateHeader(hdr)
		if err != nil {
			continue
		}
		src, err := os.Open(f)
		if err != nil {
			continue
		}
		_, _ = io.Copy(dst, src)
		_ = src.Close()
	}
	_ = zw.Close()
	_ = zf.Close()
	if err := os.Rename(tmpZip, zipPath); err != nil {
		http.Error(w, "Failed to create ZIP archive", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=\"%s.zip\"", id))
	http.ServeFile(w, r, zipPath)
}
