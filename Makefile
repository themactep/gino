VERSION ?= $(shell grep 'const version' cmd/gino/main.go | awk -F'"' '{print $$2}')
LDFLAGS := -ldflags="-s -w"

.PHONY: build clean build-all help build-telegram build-discord build-slack build-twilio build-lite

help:
	@echo "Build targets:"
	@echo "  build             Default (telegram + discord)"
	@echo "  build-telegram    Telegram only"
	@echo "  build-discord     Discord only"
	@echo "  build-twilio      Telegram + Discord + Twilio SMS (tags: with_twilio)"
	@echo "  build-slack       (placeholder)"
	@echo "  build-lite        Same as default (legacy)"
	@echo "  build-all         Cross-compile linux_amd64, linux_arm64, mac_arm64"
	@echo ""
	@echo "Cross-compile targets:"
	@echo "  linux_amd64       Full build for linux/amd64"
	@echo "  linux_arm64       Full build for linux/arm64"
	@echo "  mac_arm64         Full build for darwin/arm64"
	@echo "  linux_amd64_telegram  Telegram-only for linux/amd64"
	@echo "  linux_arm64_telegram  Telegram-only for linux/arm64"
	@echo "  mac_arm64_telegram    Telegram-only for darwin/arm64"
	@echo "  linux_amd64_lite     Lite for linux/amd64"
	@echo "  linux_arm64_lite     Lite for linux/arm64"
	@echo "  mac_arm64_lite       Lite for darwin/arm64"
	@echo ""
	@echo "Build tags:"
	@echo "  only_telegram     Embed only the Telegram channel"
	@echo "  only_discord      Embed only the Discord channel"
	@echo "  with_twilio       Include Twilio SMS channel"
	@echo ""
	@echo "Other:"
	@echo "  clean             Remove built binaries"

# Default: full build for current platform
build:
	CGO_ENABLED=0 go build -v $(LDFLAGS) -o gino ./cmd/gino

# Single-channel builds (smaller binaries)
build-telegram:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o gino ./cmd/gino

build-discord:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_discord -o gino ./cmd/gino

build-slack:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags only_slack -o gino ./cmd/gino

build-twilio:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags with_twilio -o gino ./cmd/gino

# Lite: no WhatsApp (backward compat)
build-lite:
	CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o gino ./cmd/gino

# Cross-compile all variants
build-all: \
	linux_amd64 linux_arm64 mac_arm64 \
	linux_amd64_telegram linux_arm64_telegram mac_arm64_telegram \
	linux_amd64_lite linux_arm64_lite mac_arm64_lite
	@echo "All builds completed."

# Full builds (all channels)
linux_amd64:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -o gino_$(VERSION)_linux_amd64 ./cmd/gino

linux_arm64:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o gino_$(VERSION)_linux_arm64 ./cmd/gino

mac_arm64:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -o gino_$(VERSION)_mac_arm64 ./cmd/gino

# Telegram-only builds (~10MB)
linux_amd64_telegram:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o gino_$(VERSION)_linux_amd64_telegram ./cmd/gino

linux_arm64_telegram:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o gino_$(VERSION)_linux_arm64_telegram ./cmd/gino

mac_arm64_telegram:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags only_telegram -o gino_$(VERSION)_mac_arm64_telegram ./cmd/gino

# Lite builds (no WhatsApp, backward compat)
linux_amd64_lite:
	GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o gino_$(VERSION)_linux_amd64_lite ./cmd/gino

linux_arm64_lite:
	GOOS=linux GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o gino_$(VERSION)_linux_arm64_lite ./cmd/gino

mac_arm64_lite:
	GOOS=darwin GOARCH=arm64 CGO_ENABLED=0 go build $(LDFLAGS) -tags lite -o gino_$(VERSION)_mac_arm64_lite ./cmd/gino

clean:
	rm -f gino gino_*
