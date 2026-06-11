#!/bin/bash
# Test Flatpak from-source build in a container matching GitHub Actions environment
# Usage: ./test-build.sh v0.1.18
#
# This simulates the GA workflow: runs calculate-hashes.sh to resolve the git tag/commit
# and shim binary hashes, then builds the Flatpak from source with vendored dependencies.

set -e

if [ -z "$1" ]; then
    echo "Usage: $0 <version-tag>"
    echo "Example: $0 v0.1.18"
    exit 1
fi

VERSION="$1"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"

echo "=========================================="
echo "Flatpak From-Source Build Test"
echo "=========================================="
echo "Version: $VERSION"
echo "Repo: $REPO_DIR"
echo ""

# Create a test script to run inside the container
cat > /tmp/flatpak-build-test-inner.sh <<'INNERSCRIPT'
#!/bin/bash
set -e

VERSION="$1"

echo "Installing dependencies..."
apt-get update
apt-get install -y flatpak flatpak-builder wget git

echo ""
echo "Adding Flathub repository..."
flatpak remote-add --if-not-exists --user flathub https://flathub.org/repo/flathub.flatpakrepo

echo ""
echo "Installing Flatpak runtimes and SDK extensions..."
flatpak install -y --user flathub \
  org.gnome.Platform//50 \
  org.gnome.Sdk//50 \
  org.freedesktop.Sdk.Extension.golang//25.08 \
  org.freedesktop.Sdk.Extension.node24//25.08

echo ""
echo "=========================================="
echo "Environment ready. Starting build process..."
echo "=========================================="
echo ""

cd /workspace

echo "Cleaning flatpak-builder cache..."
rm -rf .flatpak-builder

echo "Checking if shim binary release assets are available..."
if ! wget -q --spider "https://github.com/hkdb/aerion/releases/download/${VERSION}/flathub-build-env-${VERSION}-linux-x86_64"; then
    echo ""
    echo "ERROR: Shim binary not found for ${VERSION}"
    echo "   Make sure the release exists at:"
    echo "   https://github.com/hkdb/aerion/releases/tag/${VERSION}"
    echo ""
    exit 1
fi

echo ""
echo "Calculating hashes and updating manifest..."
cd build/flatpak/flathub
chmod +x calculate-hashes.sh
./calculate-hashes.sh "$VERSION"

echo ""
echo "=========================================="
echo "Building Flatpak from source..."
echo "=========================================="
cd /workspace

flatpak-builder --user --force-clean --repo=repo \
  --install-deps-from=flathub \
  build-dir build/flatpak/flathub/io.github.hkdb.Aerion.yml

echo ""
echo "Creating bundle..."
mkdir -p build/bin
flatpak build-bundle repo build/bin/Aerion-${VERSION}.flatpak io.github.hkdb.Aerion

echo ""
echo "=========================================="
echo "Build successful!"
echo "=========================================="
echo "Output: build/bin/Aerion-${VERSION}.flatpak"
ls -lh build/bin/Aerion-${VERSION}.flatpak
INNERSCRIPT

chmod +x /tmp/flatpak-build-test-inner.sh

echo "Starting Docker container (ubuntu:24.04)..."
echo ""

docker run --rm -it --privileged \
  -v "$REPO_DIR:/workspace" \
  -v /tmp/flatpak-build-test-inner.sh:/build-script.sh \
  -w /workspace \
  ubuntu:noble-20260113 \
  /build-script.sh "$VERSION"

echo ""
echo "=========================================="
echo "Test completed!"
echo "=========================================="
echo ""
echo "If successful, the flatpak bundle is at:"
echo "  $REPO_DIR/build/bin/Aerion-${VERSION}.flatpak"
echo ""
