#!/bin/sh
set -e

REPO="dolphprefect/pawsift-mcp"
INSTALL_DIR="$HOME/.local/bin"
BINARY="pawsift"

# Detect OS
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
    linux|darwin) ;;
    *) echo "Unsupported OS: $OS" && exit 1 ;;
esac

# Detect arch
case $(uname -m) in
    x86_64)        ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *)             echo "Unsupported architecture: $(uname -m)" && exit 1 ;;
esac

ASSET="${BINARY}-${OS}-${ARCH}"
URL="https://github.com/${REPO}/releases/latest/download/${ASSET}"

echo "Installing PawSift for ${OS}/${ARCH}..."
mkdir -p "$INSTALL_DIR"
curl -fsSL "$URL" -o "$INSTALL_DIR/$BINARY"
chmod +x "$INSTALL_DIR/$BINARY"
echo "  Binary installed to $INSTALL_DIR/$BINARY"

# Register in Claude Code
CLAUDE_CONFIG="$HOME/.claude.json"
if [ -f "$CLAUDE_CONFIG" ] && command -v jq >/dev/null 2>&1; then
    jq --arg cmd "$INSTALL_DIR/$BINARY" '.mcpServers.pawsift = {"command": $cmd}' \
        "$CLAUDE_CONFIG" > "$CLAUDE_CONFIG.tmp" && mv "$CLAUDE_CONFIG.tmp" "$CLAUDE_CONFIG"
    echo "  Registered in Claude Code ($CLAUDE_CONFIG)"
fi

# Register in Gemini CLI
GEMINI_CONFIG="$HOME/.gemini/settings.json"
if [ -f "$GEMINI_CONFIG" ] && command -v jq >/dev/null 2>&1; then
    jq --arg cmd "$INSTALL_DIR/$BINARY" '.mcpServers.pawsift = {"command": $cmd}' \
        "$GEMINI_CONFIG" > "$GEMINI_CONFIG.tmp" && mv "$GEMINI_CONFIG.tmp" "$GEMINI_CONFIG"
    echo "  Registered in Gemini CLI ($GEMINI_CONFIG)"
fi

echo ""
echo "PawSift $("$INSTALL_DIR/$BINARY" -version) installed successfully! 🐾"
echo ""

# Warn if install dir is not in PATH
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) echo "  Note: $INSTALL_DIR is not in your PATH. Add it to your shell profile." ;;
esac
