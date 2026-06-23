# Flatpak Packaging for Aerion

This directory contains files for building and distributing Aerion as a Flatpak.

## Files

- `io.github.hkdb.Aerion-dev.yml` - Dev manifest (packages pre-built host binary, no compilation)
- `io.github.hkdb.Aerion.metainfo.xml` - AppStream metadata
- `build-flatpak.sh` - Dev build script (uses `-dev.yml`)
- `build-local.sh` - From-source local build script (uses flathub manifest)
- `test-build.sh` - CI build test script (Docker container)
- `build-flatpak-docker.sh`, `Dockerfile` - Docker-based build
- `flathub/` - Flathub submission files (from-source manifests + vendored deps)

## Prerequisites

Install flatpak-builder:

```bash
# Fedora
sudo dnf install flatpak-builder

# Ubuntu/Debian
sudo apt install flatpak-builder

# Arch
sudo pacman -S flatpak-builder
```

Add Flathub repository (if not already added):

```bash
flatpak remote-add --if-not-exists flathub https://flathub.org/repo/flathub.flatpakrepo
```

Install required runtimes and SDKs:

```bash
flatpak install flathub org.gnome.Platform//50 org.gnome.Sdk//50
flatpak install flathub org.freedesktop.Sdk.Extension.golang//25.08
flatpak install flathub org.freedesktop.Sdk.Extension.node24//25.08
```

## Building Locally

There are two build paths:

### 1. Dev build (`build-flatpak.sh`) - Fast

Builds the binary on the host, then packages it into a Flatpak. Best for development iteration.

```bash
./build/flatpak/build-flatpak.sh

# Or via make
make flatpak
```

### 2. From-source build (`build-local.sh`) - Full

Builds everything from source inside the Flatpak sandbox, matching how Flathub builds it. Uses the `node24` SDK extension.

```bash
./build/flatpak/build-local.sh
```

## Running

After building, run the Flatpak:

```bash
flatpak run io.github.hkdb.Aerion
```

## Validation

Before submitting to Flathub, validate the metainfo file:

```bash
# Install appstream-util
sudo dnf install libappstream-glib  # Fedora
sudo apt install appstream-util      # Ubuntu/Debian

# Validate (from project root)
appstream-util validate build/flatpak/io.github.hkdb.Aerion.metainfo.xml
```

Validate the desktop file:

```bash
desktop-file-validate build/linux/aerion.desktop
```

## Submitting to Flathub

See [`flathub/README.md`](flathub/README.md) for complete Flathub submission instructions.

## Additional Resources

- [Flatpak Documentation](https://docs.flatpak.org/)
- [Flathub Submission Guide](https://github.com/flathub/flathub/wiki/App-Submission)
- [AppStream Guidelines](https://www.freedesktop.org/software/appstream/docs/)
- [Flatpak Builder Manifest](https://docs.flatpak.org/en/latest/flatpak-builder-command-reference.html)

## Advantages Over AppImage

- WebKit provided by GNOME runtime (no bundling needed)
- Works on ALL Linux distros consistently
- Sandboxing is properly implemented
- Automatic updates via Flatpak
- Centralized distribution through Flathub
- Better integration with desktop environments
- Shared runtime = smaller download size
