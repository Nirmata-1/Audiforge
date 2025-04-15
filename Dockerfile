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
    libpng16-16 \
    libjpeg62-turbo \
    libtiff5 \
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

# Copy Go artifacts from first stage
COPY --from=go-builder /app/audiforge /app/
COPY --from=go-builder /app/templates /app/templates

# Setup environment
RUN mkdir -p /tmp/uploads /tmp/downloads && \
    chmod -R 755 /tmp/uploads /tmp/downloads /app/templates

ENV AUDIVERIS_HOME=/app/audiveris \
    UPLOAD_DIR=/tmp/uploads \
    DOWNLOAD_DIR=/tmp/downloads \
    LOG=""

EXPOSE 8080
ENTRYPOINT ["/app/audiforge"]
