#!/usr/bin/env bash
set -e

usage() {
    cat <<EOF
Usage: $0 [options]

Build claude-review from this source checkout, install it to ~/.local/bin
and install its slash commands. Builds with Go (CGO enabled) when it is
installed, otherwise on Linux in a Docker container.

Options:
  --docker      Build in a Docker container even if Go is installed (Linux only)
  -h, --help    Show this help and exit
EOF
}

USE_DOCKER=false

while [ $# -gt 0 ]; do
    case "$1" in
    --docker)
        USE_DOCKER=true
        ;;
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

if [ "$USE_DOCKER" = false ] && ! command -v go >/dev/null 2>&1; then
    USE_DOCKER=true
    echo "Go not found, building in a Docker container instead"
fi

if [ "$USE_DOCKER" = true ]; then
    # The container produces a Linux binary, which only runs on a Linux host
    if [ "$(uname -s)" != "Linux" ]; then
        echo "Error: Go is required to build claude-review on $(uname -s) (https://go.dev/doc/install)"
        exit 1
    fi
    if ! command -v docker >/dev/null 2>&1; then
        echo "Error: Go or Docker is required to build claude-review (https://go.dev/doc/install)"
        exit 1
    fi
    # Same Go image as the dev container, so the Go version is defined in one place
    GO_IMAGE=$(awk '/^FROM/ { print $2; exit }' "$SRC_DIR/Dockerfile.dev")
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
BUILD_FLAGS=(-trimpath -ldflags="-s -w -X main.Version=$NEW_VERSION")
if [ "$USE_DOCKER" = true ]; then
    echo "Using Docker image $GO_IMAGE"
    # Run as the invoking user so the binary and the caches in .cache/ are not root-owned.
    # VCS stamping is off because git refuses a checkout owned by another user inside the container.
    docker run --rm -u "$(id -u):$(id -g)" \
        -v "$SRC_DIR":/src -v "$BUILD_DIR":/out -w /src \
        -e CGO_ENABLED=1 -e HOME=/src/.cache/home -e GOCACHE=/src/.cache/go-build \
        -e GOMODCACHE=/src/.cache/go-mod -e GOFLAGS=-modcacherw \
        "$GO_IMAGE" go build -buildvcs=false "${BUILD_FLAGS[@]}" -o /out/claude-review .
else
    (cd "$SRC_DIR" && CGO_ENABLED=1 go build "${BUILD_FLAGS[@]}" -o "$BUILD_DIR/claude-review" .)
fi

# Make sure the binary runs here (e.g. the host's glibc is new enough) before replacing anything
if ! "$BUILD_DIR/claude-review" version >/dev/null 2>&1; then
    echo "Error: the built binary does not run on this system"
    exit 1
fi
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
