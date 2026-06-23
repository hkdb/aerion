# Aerion Email Client - Build System
# 
# Usage:
#   make build    - Build production binary
#   make dev      - Run in development mode
#   make help     - Show all available targets
#
# OAuth credentials are loaded from .env or .env.local files
# See .env.example for required variables

.PHONY: all build build-linux dev dev-race generate clean test lint help \
        install uninstall install-linux uninstall-linux \
        install-darwin uninstall-darwin build-windows-installer flatpak flatpak-dev

# Load environment variables from .env files.
# .env.local overrides .env. All OAuth credentials live in the root .env —
# extension packages no longer carry their own OAuth client vars.
-include .env
-include .env.local
export

# Go module path
MODULE := github.com/hkdb/aerion

# Build flags for injecting OAuth credentials at compile time.
#
#   GOOGLE_CLIENT_ID/SECRET   — mail's Google-verified client. Also backs
#                               first-party extensions for any scopes their
#                               manifest declares in
#                               first_party_uses_core_for_scopes (today:
#                               contacts.readonly). Surfaced as
#                               "Aerion - Google" in the picker.
#   MICROSOFT_CLIENT_ID       — mail's Azure AD app registration. Also
#                               backs microsoft-contacts and
#                               microsoft-calendar (Microsoft Graph
#                               doesn't gate scopes behind verification).
#                               Surfaced as "Aerion - Microsoft".
#   GOOGLE_TESTING_CLIENT_ID/SECRET — shared un-Google-verified test
#                               project for extensions that need broader
#                               scopes than the mail project carries
#                               (contacts.readwrite, full Calendar).
#                               Single client backs google-contacts AND
#                               google-calendar slots. Surfaced as
#                               "Aerion - Google (Testing)".
LDFLAGS := -X '$(MODULE)/internal/oauth2.GoogleClientID=$(GOOGLE_CLIENT_ID)' \
           -X '$(MODULE)/internal/oauth2.GoogleClientSecret=$(GOOGLE_CLIENT_SECRET)' \
           -X '$(MODULE)/internal/oauth2.MicrosoftClientID=$(MICROSOFT_CLIENT_ID)' \
           -X '$(MODULE)/internal/oauth2.GoogleTestingClientID=$(GOOGLE_TESTING_CLIENT_ID)' \
           -X '$(MODULE)/internal/oauth2.GoogleTestingClientSecret=$(GOOGLE_TESTING_CLIENT_SECRET)'

# Wails build tags
BUILD_TAGS := webkit2_41

# NOTE: AppImage build target has been removed due to webkit bundling incompatibility.
# See archive/AppImage/README.md for details on what was tried and why it didn't work.
# Use Flatpak packaging instead for cross-distro distribution.

# Installation directories (can be overridden)
PREFIX ?= /usr/local
DESTDIR ?=

# Platform detection
UNAME_S := $(shell uname -s)

# Default target
all: build

## Build Targets

# Build production binary
build:
	@echo "Building Aerion..."
	@if [ -z "$(GOOGLE_CLIENT_ID)" ] && [ -z "$(MICROSOFT_CLIENT_ID)" ]; then \
		echo "Warning: No OAuth credentials configured. Gmail/Outlook OAuth will not work."; \
		echo "See .env.example for required variables."; \
	fi
	wails build -ldflags "$(LDFLAGS)" -tags $(BUILD_TAGS)
ifeq ($(UNAME_S),Darwin)
	@echo "Ad-hoc signing Aerion.app (required for macOS notifications)..."
	codesign --force --deep --sign - build/bin/Aerion.app
endif

# Build for Linux specifically
build-linux:
	@echo "Building Aerion for Linux..."
	wails build -ldflags "$(LDFLAGS)" -tags $(BUILD_TAGS),linux,production

# Build Flatpak (recommended for Linux distribution)
flatpak:
	@echo "Building Flatpak..."
	./build/flatpak/build-local.sh

# Build Flatpak from local source (for development/testing)
flatpak-dev:
	@echo "Building Flatpak from local source..."
	./build/flatpak/build-flatpak.sh

# Run in development mode with hot reload
dev:
	@echo "Starting Aerion in development mode..."
	wails dev -ldflags "$(LDFLAGS)" -tags $(BUILD_TAGS)

# Run in development mode with Go's race detector enabled. Builds significantly
# slower and adds ~5-10x runtime overhead, but instruments every memory access
# and prints exactly which line + goroutines collide on any unsynchronized
# shared-memory access. Use this when chasing a suspected data race —
# reproduce the crash and the detector report points right at it.
dev-race:
	@echo "Starting Aerion in development mode with -race..."
	wails dev -ldflags "$(LDFLAGS)" -tags $(BUILD_TAGS) -race

# Generate Wails TypeScript bindings
generate:
	@echo "Generating Wails bindings..."
	wails generate module

## Code Quality

# Run Go tests
test:
	@echo "Running tests..."
	go test ./...

# Run all linters (Go + frontend)
lint: lint-go lint-frontend

# Run Go linter (requires golangci-lint)
lint-go:
	@echo "Running Go linter..."
	golangci-lint run

# Run frontend linter (ESLint)
lint-frontend:
	@echo "Running frontend linter..."
	cd frontend && npm run lint

# Format Go code
fmt:
	@echo "Formatting Go code..."
	go fmt ./...

## Maintenance

# Clean build artifacts
clean:
	@echo "Cleaning build artifacts..."
	rm -rf build/bin
	rm -rf frontend/dist
	rm -rf AppDir
	rm -f aerion

# Clean downloaded tools (deprecated - AppImage removed)
tools-clean:
	@echo "Note: AppImage support has been removed. See archive/AppImage/ for details."
	@echo "Use 'make clean' to clean build artifacts."

# Install frontend dependencies
frontend-deps:
	@echo "Installing frontend dependencies..."
	cd frontend && npm install

# Update frontend dependencies
frontend-update:
	@echo "Updating frontend dependencies..."
	cd frontend && npm update

## Installation (Cross-Platform)

# Auto-detect platform and install
install:
ifeq ($(UNAME_S),Linux)
	$(MAKE) install-linux
else ifeq ($(UNAME_S),Darwin)
	$(MAKE) install-darwin
else
	@echo "For Windows, use 'make build-windows-installer' and run the generated installer."
	@echo "Or manually copy build/bin/aerion.exe to your preferred location."
endif

# Auto-detect platform and uninstall
uninstall:
ifeq ($(UNAME_S),Linux)
	$(MAKE) uninstall-linux
else ifeq ($(UNAME_S),Darwin)
	$(MAKE) uninstall-darwin
else
	@echo "For Windows, use Add/Remove Programs in Windows Settings."
endif

## Linux Installation

# Install Aerion on Linux
install-linux: build
	@echo "Installing Aerion to $(DESTDIR)$(PREFIX)..."
	install -Dm755 build/bin/aerion "$(DESTDIR)$(PREFIX)/bin/aerion"
	install -Dm644 build/appicon.png "$(DESTDIR)$(PREFIX)/share/icons/hicolor/256x256/apps/io.github.hkdb.Aerion.png"
	install -Dm644 build/linux/aerion.desktop "$(DESTDIR)$(PREFIX)/share/applications/io.github.hkdb.Aerion.desktop"
	@echo "Updating icon cache..."
	-gtk-update-icon-cache -f -t "$(DESTDIR)$(PREFIX)/share/icons/hicolor" 2>/dev/null || true
	@echo ""
	@echo "Installation complete!"
	@echo "You may need to log out and back in for the application to appear in your menu."
	@echo ""
	@echo "To set Aerion as your default email client:"
	@echo "  xdg-mime default io.github.hkdb.Aerion.desktop x-scheme-handler/mailto"

# Uninstall Aerion from Linux
uninstall-linux:
	@echo "Uninstalling Aerion from $(DESTDIR)$(PREFIX)..."
	rm -f "$(DESTDIR)$(PREFIX)/bin/aerion"
	rm -f "$(DESTDIR)$(PREFIX)/share/icons/hicolor/256x256/apps/io.github.hkdb.Aerion.png"
	rm -f "$(DESTDIR)$(PREFIX)/share/icons/hicolor/256x256/apps/aerion.png"  # Remove old name if it exists
	rm -f "$(DESTDIR)$(PREFIX)/share/applications/io.github.hkdb.Aerion.desktop"
	rm -f "$(DESTDIR)$(PREFIX)/share/applications/aerion.desktop"  # Remove old name if it exists
	-gtk-update-icon-cache -f -t "$(DESTDIR)$(PREFIX)/share/icons/hicolor" 2>/dev/null || true
	@echo "Uninstallation complete!"

## macOS Installation

# Install Aerion on macOS
install-darwin: build
	@echo "Installing Aerion.app to /Applications..."
	@if [ -d "/Applications/Aerion.app" ]; then \
		echo "Removing existing installation..."; \
		rm -rf "/Applications/Aerion.app"; \
	fi
	cp -R "build/bin/Aerion.app" "/Applications/"
	@echo "Re-signing installed copy..."
	codesign --force --deep --sign - "/Applications/Aerion.app"
	@echo ""
	@echo "Installation complete!"
	@echo "Aerion is now available in /Applications."

# Uninstall Aerion from macOS
uninstall-darwin:
	@echo "Uninstalling Aerion from /Applications..."
	rm -rf "/Applications/Aerion.app"
	@echo "Uninstallation complete!"

## Windows Installation

# Build Windows installer (requires NSIS)
build-windows-installer:
	@echo "Building Windows installer..."
	wails build -ldflags "$(LDFLAGS)" -tags $(BUILD_TAGS) -nsis
	@echo ""
	@echo "Installer created at build/bin/aerion-amd64-installer.exe"

## Help

# Show available targets
help:
	@echo "Aerion Email Client - Build System"
	@echo ""
	@echo "Build Targets:"
	@echo "  make build        - Build production binary"
	@echo "  make build-linux  - Build for Linux with production tags"
	@echo "  make flatpak      - Build Flatpak package (recommended for Linux)"
	@echo "  make flatpak-dev  - Build Flatpak from local source (for testing)"
	@echo "  make dev          - Run in development mode with hot reload"
	@echo "  make generate     - Generate Wails TypeScript bindings"
	@echo ""
	@echo "Installation (auto-detects platform):"
	@echo "  make install      - Install Aerion (Linux/macOS)"
	@echo "  make uninstall    - Uninstall Aerion (Linux/macOS)"
	@echo ""
	@echo "Platform-Specific Installation:"
	@echo "  make install-linux      - Install on Linux to $(PREFIX)"
	@echo "  make uninstall-linux    - Uninstall from Linux"
	@echo "  make install-darwin     - Install on macOS to /Applications"
	@echo "  make uninstall-darwin   - Uninstall from macOS"
	@echo "  make build-windows-installer - Build NSIS installer for Windows"
	@echo ""
	@echo "Code Quality:"
	@echo "  make test          - Run Go tests"
	@echo "  make lint          - Run all linters (Go + frontend)"
	@echo "  make lint-go       - Run Go linter only (requires golangci-lint)"
	@echo "  make lint-frontend - Run frontend linter only (ESLint)"
	@echo "  make fmt           - Format Go code"
	@echo ""
	@echo "Maintenance:"
	@echo "  make clean        - Clean build artifacts"
	@echo "  make frontend-deps   - Install frontend dependencies"
	@echo "  make frontend-update - Update frontend dependencies"
	@echo ""
	@echo "Environment Variables:"
	@echo "  PREFIX             - Installation prefix (default: /usr/local)"
	@echo "  DESTDIR            - Staging directory for packaging"
	@echo "  GOOGLE_CLIENT_ID     - Google OAuth Client ID"
	@echo "  GOOGLE_CLIENT_SECRET - Google OAuth Client Secret (optional)"
	@echo "  MICROSOFT_CLIENT_ID  - Microsoft OAuth Client ID"
	@echo ""
	@echo "See .env.example for details on obtaining OAuth credentials."
