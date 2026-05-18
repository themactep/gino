# syntax=docker/dockerfile:1.7

# ---- Build Stage: Picobot ----
FROM golang:1.26-alpine AS picobot-builder

ARG TARGETOS=linux
ARG TARGETARCH=arm64
ARG TARGETVARIANT

RUN apk add --no-cache git ca-certificates tzdata

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN set -eux; \
    GOOS="${TARGETOS:-linux}"; \
    GOARCH="${TARGETARCH:-arm64}"; \
    if [ "$GOARCH" = "arm" ] && [ -n "${TARGETVARIANT}" ]; then \
        GOARM="${TARGETVARIANT#v}"; \
    fi; \
    CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" ${GOARM:+GOARM=$GOARM} \
    go build -ldflags="-s -w" -o /picobot ./cmd/picobot

# ---- Runtime Stage ----
FROM debian:13-slim

RUN apt-get update && apt-get install -y --no-install-recommends \
    ca-certificates tzdata bash curl jq libstdc++6 && \
    rm -rf /var/lib/apt/lists/*

# Create non-root user
RUN groupadd -r picobot && useradd -r -g picobot -m picobot

# Install Picobot
COPY --from=picobot-builder /picobot /usr/local/bin/picobot
RUN chmod +x /usr/local/bin/picobot

# Install Ollama (built from source at /home/molt/ollama/)
COPY ollama-bin/ollama /usr/bin/ollama
COPY ollama-bin/lib/ /lib/ollama/
RUN chmod +x /usr/bin/ollama && \
    ldconfig /lib/ollama 2>/dev/null || true

# Picobot home directory
ENV PICOBOT_HOME=/home/picobot/.picobot
RUN mkdir -p ${PICOBOT_HOME} && chown -R picobot:picobot /home/picobot

USER picobot
WORKDIR /home/picobot

COPY --chown=picobot:picobot docker/entrypoint.sh /entrypoint.sh
RUN chmod +x /entrypoint.sh

ENTRYPOINT ["/entrypoint.sh"]
CMD ["gateway"]
