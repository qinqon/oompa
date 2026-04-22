#!/bin/bash

set -euo pipefail

# Oompa systemd installer
# Usage: ./install.sh [--user] <instance-name>

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
USER_MODE=false
INSTANCE_NAME=""

# Parse arguments
while [[ $# -gt 0 ]]; do
    case $1 in
        --user)
            USER_MODE=true
            shift
            ;;
        *)
            INSTANCE_NAME="$1"
            shift
            ;;
    esac
done

if [ -z "$INSTANCE_NAME" ]; then
    echo "Usage: $0 [--user] <instance-name>"
    echo ""
    echo "Examples:"
    echo "  $0 --user issue-resolver    # Install for current user"
    echo "  sudo $0 issue-resolver       # Install system-wide (requires root)"
    exit 1
fi

if [ "$USER_MODE" = true ]; then
    echo "Installing oompa@${INSTANCE_NAME} for user: $(whoami)"
    
    # User-specific paths
    UNIT_DIR="${HOME}/.config/systemd/user"
    ENV_DIR="${HOME}/.config/oompa"
    BIN_DIR="${HOME}/var/lib/oompa/bin"
    SYSTEMCTL="systemctl --user"
    
    mkdir -p "$UNIT_DIR"
    mkdir -p "$ENV_DIR"
    mkdir -p "$BIN_DIR"
    
    # Copy unit file
    cp "${SCRIPT_DIR}/oompa@.service" "$UNIT_DIR/"
    
    # Create environment file if it doesn't exist
    if [ ! -f "${ENV_DIR}/${INSTANCE_NAME}.env" ]; then
        cp "${SCRIPT_DIR}/example.env" "${ENV_DIR}/${INSTANCE_NAME}.env"
        echo "Created environment file: ${ENV_DIR}/${INSTANCE_NAME}.env"
        echo "Please edit this file to add your GITHUB_TOKEN and OOMPA_FLAGS"
    else
        echo "Environment file already exists: ${ENV_DIR}/${INSTANCE_NAME}.env"
    fi
    
    # Update binary directory in user service
    # (User mode needs a writable location)
    echo "Note: Edit ${UNIT_DIR}/oompa@.service to set OOMPA_BINARY_DIR=${BIN_DIR}"
    
    $SYSTEMCTL daemon-reload
    
    echo ""
    echo "Installation complete!"
    echo ""
    echo "Next steps:"
    echo "1. Edit environment file: nano ${ENV_DIR}/${INSTANCE_NAME}.env"
    echo "2. Start service: systemctl --user start oompa@${INSTANCE_NAME}"
    echo "3. View logs: journalctl --user -u oompa@${INSTANCE_NAME} -f"
    echo "4. Enable on login: systemctl --user enable oompa@${INSTANCE_NAME}"
    
else
    # System-wide installation (requires root)
    if [ "$EUID" -ne 0 ]; then
        echo "Error: System-wide installation requires root privileges"
        echo "Please run with sudo or use --user flag for user installation"
        exit 1
    fi
    
    echo "Installing oompa@${INSTANCE_NAME} system-wide"
    
    # System-wide paths
    UNIT_DIR="/etc/systemd/system"
    ENV_DIR="/etc/oompa"
    BIN_DIR="/var/lib/oompa/bin"
    SYSTEMCTL="systemctl"
    
    mkdir -p "$ENV_DIR"
    mkdir -p "$BIN_DIR"
    
    # Copy unit file
    cp "${SCRIPT_DIR}/oompa@.service" "$UNIT_DIR/"
    
    # Create environment file if it doesn't exist
    if [ ! -f "${ENV_DIR}/${INSTANCE_NAME}.env" ]; then
        cp "${SCRIPT_DIR}/example.env" "${ENV_DIR}/${INSTANCE_NAME}.env"
        chmod 600 "${ENV_DIR}/${INSTANCE_NAME}.env"
        echo "Created environment file: ${ENV_DIR}/${INSTANCE_NAME}.env"
        echo "Please edit this file to add your GITHUB_TOKEN and OOMPA_FLAGS"
    else
        echo "Environment file already exists: ${ENV_DIR}/${INSTANCE_NAME}.env"
    fi
    
    $SYSTEMCTL daemon-reload
    
    echo ""
    echo "Installation complete!"
    echo ""
    echo "Next steps:"
    echo "1. Edit environment file: nano ${ENV_DIR}/${INSTANCE_NAME}.env"
    echo "2. Start service: systemctl start oompa@${INSTANCE_NAME}"
    echo "3. View logs: journalctl -u oompa@${INSTANCE_NAME} -f"
    echo "4. Enable on boot: systemctl enable oompa@${INSTANCE_NAME}"
fi
