VERSION ?= $(shell grep 'const version' cmd/picobot/main.go | awk -F'"' '{print $$2}')
LDFLAGS := -ldflags="-s -w"

.PHONY: build clean build-all build-telegram build-discord build-slack build-lite

# Default: full build for current platform
build:
	CGO_ENABLED=0 go build -v $(LDFLAGS) -o picobot ./cmd/picobot

# Single-channel builds (smaller binaries)
build-telegram:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o picobot ./cmd/picobot

build-discord:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_discord -o picobot ./cmd/picobot

build-slack:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_slack -o picobot ./cmd/picobot

# Lite: no WhatsApp (backward compat)
build-lite:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o picobot ./cmd/picobot

# Cross-compile all variants
build-all: \
	linux_amd64 linux_arm64 mac_arm64 \
	linux_amd64_telegram linux_arm64_telegram mac_arm64_telegram \
	linux_amd64_lite linux_arm64_lite mac_arm64_lite
	@echo "All builds completed."

# Full builds (all channels)
linux_amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o picobot_$(VERSION)_linux_amd64 ./cmd/picobot

linux_arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o picobot_$(VERSION)_linux_arm64 ./cmd/picobot

mac_arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o picobot_$(VERSION)_mac_arm64 ./cmd/picobot

# Telegram-only builds (~10MB)
linux_amd64_telegram:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o picobot_$(VERSION)_linux_amd64_telegram ./cmd/picobot

linux_arm64_telegram:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o picobot_$(VERSION)_linux_arm64_telegram ./cmd/picobot

mac_arm64_telegram:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o picobot_$(VERSION)_mac_arm64_telegram ./cmd/picobot

# Lite builds (no WhatsApp, backward compat)
linux_amd64_lite:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o picobot_$(VERSION)_linux_amd64_lite ./cmd/picobot

linux_arm64_lite:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o picobot_$(VERSION)_linux_arm64_lite ./cmd/picobot

mac_arm64_lite:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o picobot_$(VERSION)_mac_arm64_lite ./cmd/picobot

clean:
	rm -f picobot picobot_*
