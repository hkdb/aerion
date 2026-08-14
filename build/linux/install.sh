#!/bin/bash

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
PURPLE='\033[0;35m'
NC='\033[0m' # No Color

echo -e "${PURPLE}
 ░▒▓██████▓▒░░▒▓████████▓▒░▒▓███████▓▒░░▒▓█▓▒░░▒▓██████▓▒░░▒▓███████▓▒░  
░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░      ░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░ 
░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░      ░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░ 
░▒▓████████▓▒░▒▓██████▓▒░ ░▒▓███████▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░ 
░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░      ░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░ 
░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░      ░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓█▓▒░ 
░▒▓█▓▒░░▒▓█▓▒░▒▓████████▓▒░▒▓█▓▒░░▒▓█▓▒░▒▓█▓▒░░▒▓██████▓▒░░▒▓█▓▒░░▒▓█▓▒░ 
                                                                         
${NC}
"

# Print colored output
print_error() {
    echo -e "${RED}Error: $1${NC}" >&2
}

print_success() {
    echo -e "${GREEN}$1${NC}"
}

print_info() {
    echo -e "${YELLOW}$1${NC}"
}

# Check if running on Linux
if [[ "$(uname -s)" != "Linux" ]]; then
    print_error "This script is only for Linux systems"
    exit 1
fi

# Check if binary exists
if [[ ! -f "aerion" ]]; then
    print_error "aerion binary not found in current directory"
    echo "Please run this script from the directory containing the aerion binary"
    exit 1
fi

# Check if desktop file exists
if [[ ! -f "io.github.hkdb.Aerion.desktop" ]]; then
    print_error "io.github.hkdb.Aerion.desktop file not found in current directory"
    echo "Please ensure the desktop file is in the same directory as this script"
    exit 1
fi

# hicolor sizes launchers actually request. 256-only is skipped by
# [256x256/apps] MinSize=64 when the lookup asks for ~48px (see #395).
ICON_SIZES=(32 48 64 128 256)

icon_source_for_size() {
    local sz="$1"
    local sized="icons/${sz}x${sz}/io.github.hkdb.Aerion.png"
    if [[ -f "$sized" ]]; then
        printf '%s\n' "$sized"
        return 0
    fi
    if [[ "$sz" == "256" && -f "io.github.hkdb.Aerion.png" ]]; then
        printf '%s\n' "io.github.hkdb.Aerion.png"
        return 0
    fi
    return 1
}

if [[ -d icons ]]; then
    for sz in "${ICON_SIZES[@]}"; do
        if [[ ! -f "icons/${sz}x${sz}/io.github.hkdb.Aerion.png" ]]; then
            print_error "missing icons/${sz}x${sz}/io.github.hkdb.Aerion.png"
            exit 1
        fi
    done
fi

if ! icon_source_for_size 256 >/dev/null; then
    print_error "Aerion icon not found (io.github.hkdb.Aerion.png or icons/256x256/)"
    echo "Please run this script from the directory containing the release files"
    exit 1
fi

echo ""
print_info "Aerion Email Client - Installation Script"
echo ""
echo "This script will install Aerion on your system."
echo ""
echo "Choose installation type:"
echo "  1) System-wide (requires sudo, installs to /usr/local)"
echo "  2) User only (installs to ~/.local)"
echo ""

while true; do
    read -p "Enter your choice (1 or 2): " choice
    case $choice in
        1)
            INSTALL_TYPE="system"
            BIN_DIR="/usr/local/bin"
            APPS_DIR="/usr/share/applications"
            HICOLOR_DIR="/usr/share/icons/hicolor"
            NEEDS_SUDO=true
            break
            ;;
        2)
            INSTALL_TYPE="user"
            BIN_DIR="$HOME/.local/bin"
            APPS_DIR="$HOME/.local/share/applications"
            HICOLOR_DIR="$HOME/.local/share/icons/hicolor"
            NEEDS_SUDO=false
            break
            ;;
        *)
            print_error "Invalid choice. Please enter 1 or 2."
            ;;
    esac
done

echo ""
print_info "Installing Aerion ($INSTALL_TYPE)..."
echo ""

# Function to run command with or without sudo
run_cmd() {
    if [[ "$NEEDS_SUDO" == true ]]; then
        sudo "$@"
    else
        "$@"
    fi
}

# Check for old desktop file and rename it (backwards compatibility)
OLD_DESKTOP_FILE="$APPS_DIR/aerion.desktop"
if [[ -f "$OLD_DESKTOP_FILE" ]]; then
    print_info "Found old aerion.desktop, renaming to aerion.desktop.backup..."
    run_cmd mv "$OLD_DESKTOP_FILE" "$APPS_DIR/aerion.desktop.backup"
    print_success "Old desktop file renamed to aerion.desktop.backup"
fi

# Create directories if they don't exist
print_info "Creating directories..."
run_cmd mkdir -p "$BIN_DIR"
run_cmd mkdir -p "$APPS_DIR"

# Install binary
print_info "Installing binary to $BIN_DIR..."
run_cmd install -Dm755 aerion "$BIN_DIR/aerion"

# Install desktop file
print_info "Installing desktop file to $APPS_DIR..."
run_cmd install -Dm644 io.github.hkdb.Aerion.desktop "$APPS_DIR/io.github.hkdb.Aerion.desktop"

# Install themed icons (sized PNGs + 256 fallback for older tarballs).
# install -D creates the hicolor/<size>/apps directory.
print_info "Installing icons to $HICOLOR_DIR..."
for sz in "${ICON_SIZES[@]}"; do
    if src=$(icon_source_for_size "$sz"); then
        run_cmd install -Dm644 "$src" "$HICOLOR_DIR/${sz}x${sz}/apps/io.github.hkdb.Aerion.png"
    fi
done

# Update icon cache
print_info "Updating icon cache..."
run_cmd gtk-update-icon-cache -f -t "$HICOLOR_DIR" 2>/dev/null || true

# Update desktop database
print_info "Updating desktop database..."
if [[ "$INSTALL_TYPE" == "system" ]]; then
    run_cmd update-desktop-database /usr/share/applications 2>/dev/null || true
else
    update-desktop-database "$HOME/.local/share/applications" 2>/dev/null || true
fi

echo ""
print_success "✓ Installation complete!"
echo ""

# Additional setup instructions
if [[ "$INSTALL_TYPE" == "user" ]]; then
    if [[ ":$PATH:" != *":$HOME/.local/bin:"* ]]; then
        print_info "Note: $HOME/.local/bin is not in your PATH"
        echo "You may need to add it to your PATH by adding this line to your ~/.bashrc or ~/.zshrc:"
        echo "  export PATH=\"\$HOME/.local/bin:\$PATH\""
        echo ""
    fi
fi

echo "You may need to log out and back in for the application to appear in your menu."
echo ""
echo "To set Aerion as your default email client, run:"
echo "  xdg-mime default io.github.hkdb.Aerion.desktop x-scheme-handler/mailto"
echo ""
echo "To start Aerion, run:"
echo "  aerion --dbus-notify"
echo ""
