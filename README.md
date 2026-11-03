# ScoreForge (Audiforge) - PDF to MusicXML Conversion

A **Go-based web service** that leverages **Audiveris** for converting PDF sheet music to MusicXML format.

Note on the name: the UI is branded ScoreForge ("Audi" reads as audio,
and this is sheet-music recognition, not audio). The repository, Go
module and Docker image are still `Audiforge` — renaming those would
break existing links, so that is a separate decision.

---

## Table of Contents

- [Docker Installation](#docker-installation)
- [Local Development Setup](#local-development-setup)
- [Environment Variables](#environment-variables)
- [Configuration](#configuration)
- [API Endpoints](#api-endpoints)
- [Project Structure](#project-structure)
- [Troubleshooting](#troubleshooting)
- [FAQ](#faq)
- [Contributing](#contributing)
- [License](#license)

---

## Docker Installation

### Prerequisites

- Docker installed ([installation guide](https://docs.docker.com/get-docker/))

### Steps

1. **Pull the Docker image:**

   ```bash
   docker pull nirmata1/audiforge:latest
   ```

2. **Run the container:**

   ```bash
   docker run -d -p 8080:8080 \
     -e LOG=debug #optional
     -v /path/to/uploads:/tmp/uploads \
     -v /path/to/downloads:/tmp/downloads \
     nirmata1/audiforge:latest
   ```   
---

## Local Development Setup

### Requirements

- Go 1.20+
- Java 17+ (for Audiveris)
- Gradle 7+

### Installation Guide

#### Clone Repositories

```bash
# Create project directory
mkdir audiforge && cd audiforge

# Clone Audiveris
git clone https://github.com/Audiveris/audiveris.git

# Build Audiveris
cd audiveris
./gradlew build
```

#### Set Up Go Application

```bash
cd ..
git clone https://github.com/Nirmata-1/Audiforge.git
cd audiforge-go
go build
```

#### Run the Service

```bash
LOG=debug ./audiforge
```

---

## Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `PORT` | `8080` | HTTP listen port |
| `LOG`    | info    | Set to `debug` to show Audiveris logs in console |
| `UPLOAD_DIR` | `/tmp/uploads` | Temporary PDF storage (the Dockerfile already set this; the old code ignored it) |
| `DOWNLOAD_DIR` | `/tmp/downloads` | Converted MusicXML files |
| `AUDIVERIS_HOME` | `./audiveris` or `/app/audiveris` | Audiveris checkout (gradle fallback only) |
| `AUDIVERIS_BIN` | `/opt/audiveris/bin/Audiveris` in Docker | Installed Audiveris binary. When set and executable it is used directly; otherwise the server falls back to `gradle run` in `AUDIVERIS_HOME` |
| `AUDIVERIS_OCR_LANG` | `eng` | OCR language passed to Audiveris (`Language.defaultSpecification`). Empty disables the override. English text (titles, lyrics) needs legacy `eng.traineddata` — see below |
| `TESSDATA_PREFIX` | — | Directory containing `eng.traineddata`, passed through to Audiveris |
| `MAX_UPLOAD_MB` | `50` | Upload size limit |
| `AUDIVERIS_JOBS` | `1` | Max concurrent conversions (each Audiveris run wants ~3 GB) |
| `CONVERSION_TIMEOUT_MIN` | `20` | Per-job timeout |

### English transcription (titles, lyrics, other text)

Audiveris recognises notes without OCR data, but its TEXTS step needs
Tesseract language data to read English text. The Docker image now
downloads legacy `eng.traineddata` from `tesseract-ocr/tessdata` and
passes `-transcribe` plus
`-constant org.audiveris.omr.text.Language.defaultSpecification=eng`
on every run. If Tesseract still fails to initialise, the job still
completes on notes, but `/status/{id}` carries a `warning` saying text
may be missing, instead of failing silently. Check `/log/{id}` for the
engine log.

Known gotcha: the LSTM-only "fast" model shipped by some distro
`tesseract-ocr-eng` packages is refused by Audiveris in legacy mode —
use the file from the `tesseract-ocr/tessdata` repository.

---

## Configuration

### Key Directories

| Directory      | Path             | Purpose                      |
|----------------|------------------|------------------------------|
| Uploads        | `/tmp/uploads`   | Temporary PDF storage        |
| Downloads      | `/tmp/downloads` | Converted MusicXML files     |
| Audiveris Home | `./audiveris`    | Audiveris engine installation |

### Cleanup Process

- Automatic cleanup runs every hour
- Files older than 1 hour are deleted
- Executed via background goroutine

---

## Options (upload form)

| Option | Default | What it does |
|--------|---------|--------------|
| Text language | English | Passed to Audiveris as `Language.defaultSpecification`. French, German, Italian and Spanish are also offered. "Off — notes only" omits the override entirely. |
| Pages | All | e.g. `1, 3-5`. Passed as Audiveris `-sheets`. Invalid values are rejected with a 400 before any conversion starts. |
| Also include uncompressed MusicXML | Off | The server unzips each `.mxl` and adds a plain `.musicxml` file to the download ZIP. |
| Also include Audiveris project file | Off | Runs Audiveris with `-save` so the `.omr` project file is included in the download ZIP. |
| Download automatically when done | Off | The browser starts the ZIP download as soon as the job completes. |

## Progress bar

The bar is not a timer. While a job runs, `/status/{id}` reports a
`stage` and `progress` derived from the Audiveris conversion log: the
last sheet step seen in the log (LOAD, BINARY, …, TEXTS, …, PAGE)
maps to a stage name and a percentage, leaving the last 10% for export.
The bar only reaches 100% when the job actually completes. If the log
has no step name yet, the client creeps the bar toward 95% so it never
stalls for minutes on a dense page — again, never to 100% early.

## API Endpoints

| Method | Endpoint         | Description                    |
|--------|------------------|--------------------------------|
| POST   | `/upload`        | Upload PDF file (PDF only, 50 MB default) → `{"id": ...}` |
| GET    | `/status/{id}`   | Check conversion status; on success includes `movementCount`, per-file `notes`/`measures`/`parts` (parsed from the MusicXML), and a `warning` if English OCR failed |
| GET    | `/download/{id}` | Download ZIP of MusicXML files |
| GET    | `/log/{id}`      | Conversion log for a job |
| GET    | `/healthz`       | Server health + which Audiveris binary (if any) will be used |

Status and download IDs must be UUIDs issued by `/upload`; other
values return 404, so IDs cannot be used for path traversal.

---

## Project Structure

```
audiforge/
├── audiveris/          # Audiveris engine
│   ├── build/
│   ├── app/
│   └── gradlew
├── main.go             # Go application
├── go.mod
├── go.sum
├── templates/          # Web UI
└── Dockerfile
```

---

## Troubleshooting

### Common Issues

**Missing Dependencies**

```bash
# Install Java & Gradle
sudo apt install openjdk-17-jdk gradle
```

**Permission Denied**

```bash
chmod +x audiveris/gradlew
```

**Gradle Build Failure**

```bash
cd audiveris && ./gradlew clean build
```

**Missing Files After Conversion**

- Check `/tmp` permissions
- Verify available disk space

---

## FAQ

**Q: Can it handle multi-movement scores?**  
A: The service automatically detects movements and packages them in a ZIP file.

**Q: Can I change the cleanup interval?**  
A: Modify `CleanupInterval` in `main.go` and rebuild.

**Q: How to enable debug logging?**  
A: Run with `LOG=debug ./audiforge`

---

## Contributing

1. Fork the repository  
2. Create a feature branch  
3. Submit a PR with tests  
4. Follow better coding standards than me

---

## License

**Apache 2.0 © 2025 Jermiah Jeffries**
