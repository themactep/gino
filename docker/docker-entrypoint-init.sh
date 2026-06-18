#!/bin/bash
set -e

# Runs as root to fix volume permissions, then execs the real entrypoint as gino.

GINO_HOME="${GINO_HOME:-/home/gino/.gino}"

# Ensure required directories exist and are owned by gino
mkdir -p "${GINO_HOME}/.ollama/models"
mkdir -p "${GINO_HOME}/workspace/memory"
mkdir -p "${GINO_HOME}/workspace/skills"
chown -R gino:gino "${GINO_HOME}"

# Drop to gino user and run the real entrypoint
exec gosu gino /entrypoint.sh "$@"
