# Stage 1: Build Go application
FROM golang:1.24 AS go-builder

WORKDIR /app
COPY . .
COPY templates/ ./templates/
RUN go build -o audiforge .

# Stage 2: Build Audiveris and final image
FROM debian:bookworm-slim

# Install system dependencies
RUN apt-get update && \
    apt-get install -y \
    git \
    wget \
    unzip \
    zip \
    ca-certificates \
    fontconfig \
    fonts-dejavu \
    libfreetype6 \
    && apt-get clean \
    && rm -rf /var/lib/apt/lists/*

# Install Java 21 JDK
RUN mkdir -p /etc/apt/keyrings && \
    wget -O /etc/apt/keyrings/adoptium.asc https://packages.adoptium.net/artifactory/api/gpg/key/public && \
    echo "deb [signed-by=/etc/apt/keyrings/adoptium.asc] https://packages.adoptium.net/artifactory/deb $(awk -F= '/^VERSION_CODENAME/{print$2}' /etc/os-release) main" | \
    tee /etc/apt/sources.list.d/adoptium.list && \
    apt-get update && \
    apt-get install -y temurin-21-jdk

# Install Gradle 8.7
RUN wget https://services.gradle.org/distributions/gradle-8.7-bin.zip -O /tmp/gradle.zip \
    && unzip -d /opt /tmp/gradle.zip \
    && rm /tmp/gradle.zip
ENV PATH="/opt/gradle-8.7/bin:${PATH}"

# Build Audiveris
WORKDIR /app
RUN git clone https://github.com/Nirmata-1/audiveris.git
WORKDIR /app/audiveris
RUN ./gradlew build
# Install a runnable copy so the Go server does NOT have to run
# `gradle run` (JVM + Gradle startup) on every conversion. If the
# install layout differs from the one expected here, this step
# copies nothing and the server falls back to gradle at runtime.
RUN ./gradlew installDist -x test || true; \
    mkdir -p /opt/audiveris; \
    if [ -x app/build/install/app/bin/Audiveris ]; then \
      cp -r app/build/install/app/. /opt/audiveris/; \
    elif ls build/install/*/bin/Audiveris >/dev/null 2>&1; then \
      cp -r build/install/*/. /opt/audiveris/; \
    fi; \
    ls -la /opt/audiveris/bin/ 2>/dev/null || echo "no installed Audiveris binary, gradle fallback will be used"
# English OCR data for the TEXTS step. Without eng.traineddata,
# Audiveris recognises notes but silently drops titles/lyrics.
# This is the legacy tessdata file upstream CI installs (see
# Audiveris .github/install-tessdata.sh), not the LSTM-only
# "fast" model shipped by some distro tesseract-ocr-eng packages.
RUN mkdir -p /opt/tessdata /root/.config/AudiverisLtd/audiveris/tessdata && \
    wget -O /opt/tessdata/eng.traineddata https://github.com/tesseract-ocr/tessdata/raw/main/eng.traineddata && \
    cp /opt/tessdata/eng.traineddata /root/.config/AudiverisLtd/audiveris/tessdata/

# Copy Go artifacts from first stage
COPY --from=go-builder /app/audiforge /app/
COPY --from=go-builder /app/templates /app/templates

# Setup environment
RUN mkdir -p /tmp/uploads /tmp/downloads && \
    chmod -R 755 /tmp/uploads /tmp/downloads /app/templates

ENV AUDIVERIS_HOME=/app/audiveris \
    AUDIVERIS_BIN=/opt/audiveris/bin/Audiveris \
    TESSDATA_PREFIX=/opt/tessdata \
    UPLOAD_DIR=/tmp/uploads \
    DOWNLOAD_DIR=/tmp/downloads \
    LOG=""

EXPOSE 8080
ENTRYPOINT ["/app/audiforge"]
