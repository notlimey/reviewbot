#!/usr/bin/env bash
set -euo pipefail

INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"

echo "Building reviewbot..."
go build -o reviewbot .

echo "Installing to ${INSTALL_DIR}/reviewbot..."
if [ -w "$INSTALL_DIR" ]; then
    mv reviewbot "$INSTALL_DIR/reviewbot"
else
    sudo mv reviewbot "$INSTALL_DIR/reviewbot"
fi

echo "Done. Run 'reviewbot all .' in any project directory."
