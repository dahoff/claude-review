#!/usr/bin/env bash
set -e

usage() {
    cat <<EOF
Usage: $0 [options]

Build claude-review from this source checkout, install it to ~/.local/bin
and install its slash commands. Requires Go with CGO enabled.

Options:
  -h, --help    Show this help and exit
EOF
}

while [ $# -gt 0 ]; do
    case "$1" in
    -h | --help)
        usage
        exit 0
        ;;
    *)
        echo "Error: Unknown option: $1"
        echo
        usage
        exit 1
        ;;
    esac
    shift
done

BIN_DIR="$HOME/.local/bin"
SRC_DIR=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)

if [ ! -f "$SRC_DIR/go.mod" ]; then
    echo "Error: install.sh must be run from a claude-review source checkout"
    exit 1
fi

if ! command -v go >/dev/null 2>&1; then
    echo "Error: Go is required to build claude-review (https://go.dev/doc/install)"
    exit 1
fi

# Detect installation mode
UPGRADE_MODE=false
CURRENT_VERSION="unknown"

if [ -f "$BIN_DIR/claude-review" ]; then
    UPGRADE_MODE=true
    CURRENT_VERSION=$("$BIN_DIR/claude-review" version 2>/dev/null || echo "unknown")
    echo "Existing claude-review installation detected (version: $CURRENT_VERSION)"
fi

NEW_VERSION=$(git -C "$SRC_DIR" describe --tags --always --dirty 2>/dev/null || echo "dev")

# Build before touching the existing installation so a failed build leaves it intact
echo "Building claude-review $NEW_VERSION from $SRC_DIR..."
BUILD_DIR=$(mktemp -d)
trap 'rm -rf "$BUILD_DIR"' EXIT
(cd "$SRC_DIR" && CGO_ENABLED=1 go build -trimpath -ldflags="-s -w -X main.Version=$NEW_VERSION" -o "$BUILD_DIR/claude-review" .)
echo

# Check server status before upgrade
SERVER_WAS_RUNNING=false
if [ "$UPGRADE_MODE" = true ]; then
    echo "Checking if server is running..."
    if "$BIN_DIR/claude-review" server --status >/dev/null 2>&1; then
        SERVER_WAS_RUNNING=true
        echo "Stopping claude-review server for upgrade..."
        "$BIN_DIR/claude-review" server --stop || true
        sleep 1
    fi
fi

# Create installation directory
echo "Creating $BIN_DIR"
mkdir -p "$BIN_DIR"

# Install binary
echo "Installing claude-review binary..."
install -m 0755 "$BUILD_DIR/claude-review" "$BIN_DIR/claude-review"

# Install slash commands using the binary
echo "Installing slash commands..."
"$BIN_DIR/claude-review" install

# Restart server if it was running before upgrade
if [ "$SERVER_WAS_RUNNING" = true ]; then
    echo "Restarting server..."
    "$BIN_DIR/claude-review" server --daemon
    sleep 1
    if "$BIN_DIR/claude-review" server --status >/dev/null 2>&1; then
        echo "Server restarted successfully"
    else
        echo "Warning: Failed to restart server"
    fi
fi

# Show completion message
echo
if [ "$UPGRADE_MODE" = true ]; then
    echo "claude-review upgraded: $CURRENT_VERSION → $NEW_VERSION"
else
    echo "claude-review $NEW_VERSION installed successfully"
fi
echo

# Check if PATH includes install directory
if [[ ":$PATH:" != *":$BIN_DIR:"* ]]; then
    echo "Add $BIN_DIR to your PATH:"
    # shellcheck disable=SC2016
    echo '   export PATH='"$BIN_DIR"':$PATH'
    echo
fi

# Show usage instructions
echo "Next Steps:"
echo "  1. Ask Claude Code to create a plan in PLAN.md"
echo "  2. Run '/cr-review PLAN.md' in Claude Code to get a URL"
echo "  3. Open the URL in your browser and add comments"
echo "  4. Run '/cr-address PLAN.md' to get your comments addressed"
echo
