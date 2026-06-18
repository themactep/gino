#!/bin/bash
set -e

# init.sh — runs as root, fixes volume permissions, then drops to gino user
GINO_HOME="${GINO_HOME:-/home/gino/.gino}"

# Ensure required directories exist and are owned by gino
mkdir -p "${GINO_HOME}/.ollama/models" \
         "${GINO_HOME}/workspace/memory" \
         "${GINO_HOME}/workspace/skills"
chown -R gino:gino "${GINO_HOME}"

# Drop to gino user and run the real entrypoint
exec gosu gino /entrypoint.sh "$@"
