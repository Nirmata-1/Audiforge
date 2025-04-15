# Audiforge - PDF to MusicXML Conversion

A **Go-based web service** that leverages **Audiveris** for converting PDF sheet music to MusicXML format.

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
| `LOG`    | info    | Set to `debug` to show Audiveris logs in console |

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

## API Endpoints

| Method | Endpoint         | Description                    |
|--------|------------------|--------------------------------|
| POST   | `/upload`        | Upload PDF file                |
| GET    | `/status/{id}`   | Check conversion status        |
| GET    | `/download/{id}` | Download ZIP of MusicXML files |

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
