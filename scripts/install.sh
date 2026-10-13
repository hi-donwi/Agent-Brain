#!/usr/bin/env sh
# lokal universal installation script
# https://github.com/hi-donwi/Agent-Local

set -e

VERSION="${LOKAL_VERSION:-latest}"
INSTALL_DIR="${LOKAL_INSTALL_DIR:-}"

# Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux*)   OS="linux" ;;
  darwin*)  OS="darwin" ;;
  msys*|mingw*|cygwin*) OS="windows" ;;
  *)
    echo "Error: unsupported operating system '$OS'." >&2
    exit 1
    ;;
esac

# Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64)   ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "Error: unsupported architecture '$ARCH'." >&2
    exit 1
    ;;
esac

BINARY="lokal"
EXT=""
if [ "$OS" = "windows" ]; then
  EXT=".exe"
  BINARY="lokal.exe"
fi

# Determine destination directory
if [ -z "$INSTALL_DIR" ]; then
  if [ -w "/usr/local/bin" ]; then
    INSTALL_DIR="/usr/local/bin"
  elif [ -d "$HOME/.local/bin" ] || mkdir -p "$HOME/.local/bin" 2>/dev/null; then
    INSTALL_DIR="$HOME/.local/bin"
  else
    INSTALL_DIR="/usr/local/bin"
  fi
fi

TARGET_FILE="$INSTALL_DIR/$BINARY"
RELEASE_URL="https://github.com/hi-donwi/Agent-Local/releases/latest/download/lokal-${OS}-${ARCH}${EXT}"

echo "Installing lokal for ${OS}/${ARCH} into ${INSTALL_DIR}..."

# Check if prebuilt binary is available locally in bin/ (for development installs)
if [ -f "bin/lokal-${OS}-${ARCH}${EXT}" ]; then
  echo "Found local build artifact: bin/lokal-${OS}-${ARCH}${EXT}"
  cp "bin/lokal-${OS}-${ARCH}${EXT}" "$TARGET_FILE"
elif [ -f "bin/lokal" ] && [ "$OS" != "windows" ]; then
  echo "Found local binary: bin/lokal"
  cp "bin/lokal" "$TARGET_FILE"
else
  # Download from release
  echo "Downloading ${RELEASE_URL}..."
  TMP_FILE="$(mktemp)"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$RELEASE_URL" -o "$TMP_FILE"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$TMP_FILE" "$RELEASE_URL"
  else
    echo "Error: curl or wget is required to download lokal." >&2
    exit 1
  fi
  mv "$TMP_FILE" "$TARGET_FILE"
fi

chmod 755 "$TARGET_FILE"

echo "Successfully installed lokal to $TARGET_FILE"
echo ""
echo "Verify installation:"
echo "  $TARGET_FILE version"
echo "  $TARGET_FILE doctor"
