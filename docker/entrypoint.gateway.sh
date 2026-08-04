#!/bin/bash
set -e

# ═══════════════════════════════════════════════════════
# entrypoint.gateway.sh — Multi-tenant assistant gateway
# No bundled Ollama — requires OLLAMA_URL env var.
# ═══════════════════════════════════════════════════════

GINO_HOME="${GINO_HOME:-/home/gino/.gino}"
CONFIG="${GINO_HOME}/config.json"

# ── Signal handling ───────────────────────────────────
cleanup() {
    echo ""
    echo "Shutting down..."
    exit 0
}
trap cleanup SIGTERM SIGINT

# ── Validate required env vars ────────────────────────
if [ -z "${OLLAMA_URL}" ]; then
    echo "⚠️  OLLAMA_URL not set — brain embeddings will not work!"
    echo "   Set OLLAMA_URL=http://your-ollama:11434"
fi

if [ -z "${OPENAI_API_KEY}" ] && [ -z "${GINO_PROVIDER_API_KEY}" ]; then
    echo "⚠️  No API key set — set OPENAI_API_KEY or GINO_PROVIDER_API_KEY"
fi

# ── Config bootstrap ──────────────────────────────────
init_config() {
    if [ ! -f "${CONFIG}" ]; then
        echo "🔧 First run — generating default config..."
        # Start with a minimal multi-tenant config
        cat > "${CONFIG}" <<'DEFAULT_CONFIG'
{
  "agents": {
    "defaults": {
      "model": "glm-4-flash",
      "maxTokens": 8192,
      "maxToolIterations": 30
    }
  },
  "tenant": {
    "enabled": true,
    "workspaceRoot": "/home/gino/.gino/data/workspaces",
    "evictionIdleTimeout": "30m",
    "tiers": [
      {
        "name": "free",
        "maxToolIterations": 15,
        "rateLimitPerHour": 20,
        "rateLimitPerDay": 200,
        "maxConcurrentTurns": 1,
        "maxWorkspaceBytes": 104857600
      },
      {
        "name": "pro",
        "maxToolIterations": 30,
        "rateLimitPerHour": 100,
        "rateLimitPerDay": 1000,
        "maxConcurrentTurns": 2,
        "maxWorkspaceBytes": 524288000
      },
      {
        "name": "admin",
        "maxToolIterations": 100,
        "rateLimitPerHour": 0,
        "rateLimitPerDay": 0,
        "maxConcurrentTurns": 5,
        "maxWorkspaceBytes": 0
      }
    ]
  },
  "audit": {
    "enabled": true,
    "dbPath": "/home/gino/.gino/data/audit.db",
    "messageRetentionDays": 7,
    "usageRetentionDays": 365
  },
  "brain": {
    "enabled": true,
    "embeddingModel": "nomic-embed-text"
  },
  "channels": {
    "api": {
      "enabled": true,
      "addr": ":8080"
    }
  }
}
DEFAULT_CONFIG
        echo "✅ Default multi-tenant config created"
    fi
}

# ── Apply env vars to config ──────────────────────────
apply_env() {
    [ ! -f "${CONFIG}" ] && return 0

    apply() {
        local filter="$1"; local vtype="$2"; local val="$3"
        local tmp
        tmp=$(mktemp)
        if [ "$vtype" = "json" ]; then
            jq --argjson v "${val}" "${filter}" "${CONFIG}" > "$tmp" || { rm -f "$tmp"; return 1; }
        else
            jq --arg v "${val}" "${filter}" "${CONFIG}" > "$tmp" || { rm -f "$tmp"; return 1; }
        fi
        mv "$tmp" "${CONFIG}"
    }

    # LLM Provider
    [ -n "${OPENAI_API_KEY}" ] && apply '.providers.openai.apiKey = $v' str "${OPENAI_API_KEY}"
    [ -n "${OPENAI_API_BASE}" ] && apply '.providers.openai.apiBase = $v' str "${OPENAI_API_BASE}"
    [ -n "${GINO_MODEL}" ] && apply '.agents.defaults.model = $v' str "${GINO_MODEL}"
    [ -n "${GINO_MAX_TOKENS}" ] && apply '.agents.defaults.maxTokens = $v' json "${GINO_MAX_TOKENS}"
    [ -n "${GINO_MAX_TOOL_ITERATIONS}" ] && apply '.agents.defaults.maxToolIterations = $v' json "${GINO_MAX_TOOL_ITERATIONS}"

    # Ollama (external)
    if [ -n "${OLLAMA_URL}" ]; then
        apply '.brain.ollamaUrl = $v' str "${OLLAMA_URL}"
    fi

    # Brain embedding model
    [ -n "${GINO_BRAIN_EMBEDDING_MODEL}" ] && apply '.brain.embeddingModel = $v' str "${GINO_BRAIN_EMBEDDING_MODEL}"

    # API server
    [ -n "${GINO_API_ADDR}" ] && apply '.channels.api.addr = $v' str "${GINO_API_ADDR}"
    [ -n "${GINO_API_ADMIN_SECRET}" ] && apply '.channels.api.adminSecret = $v' str "${GINO_API_ADMIN_SECRET}"

    # Telegram (optional — can coexist with API)
    if [ -n "${TELEGRAM_BOT_TOKEN}" ]; then
        apply '.channels.telegram.enabled = true' json "true"
        apply '.channels.telegram.token = $v' str "${TELEGRAM_BOT_TOKEN}"
    fi
    if [ -n "${TELEGRAM_ALLOW_FROM}" ]; then
        apply '.channels.telegram.allowFrom = $v' json "$(echo "${TELEGRAM_ALLOW_FROM}" | jq -R 'split(",")')"
    fi

    # Discord (optional)
    if [ -n "${DISCORD_BOT_TOKEN}" ]; then
        apply '.channels.discord.enabled = true' json "true"
        apply '.channels.discord.token = $v' str "${DISCORD_BOT_TOKEN}"
    fi

    # Audit
    if [ -n "${GINO_AUDIT_MESSAGE_RETENTION}" ]; then
        apply '.audit.messageRetentionDays = $v' json "${GINO_AUDIT_MESSAGE_RETENTION}"
    fi
    if [ -n "${GINO_AUDIT_USAGE_RETENTION}" ]; then
        apply '.audit.usageRetentionDays = $v' json "${GINO_AUDIT_USAGE_RETENTION}"
    fi

    # Tenant
    if [ -n "${GINO_TENANT_WORKSPACE_ROOT}" ]; then
        apply '.tenant.workspaceRoot = $v' str "${GINO_TENANT_WORKSPACE_ROOT}"
    fi
    if [ -n "${GINO_TENANT_EVCTION_TIMEOUT}" ]; then
        apply '.tenant.evictionIdleTimeout = $v' str "${GINO_TENANT_EVCTION_TIMEOUT}"
    fi

    # Search provider
    if [ -n "${GINO_BRAVE_SEARCH_API_KEY}" ]; then
        apply '.agents.defaults.search.provider = $v' str "brave"
        apply '.agents.defaults.search.braveApiKey = $v' str "${GINO_BRAVE_SEARCH_API_KEY}"
    fi

    echo "✅ Config updated from environment variables"
}

# ── Main ──────────────────────────────────────────────
init_config
apply_env

echo ""
echo "🤖 Starting Gino Gateway (multi-tenant)"
echo "   API:    ${GINO_API_ADDR:-:8080}"
echo "   Admin:  ${GINO_API_ADDR:-:8080}/admin/"
echo "   Tenant: $(jq -r '.tenant.enabled // false' "${CONFIG}" 2>/dev/null || echo 'false')"
echo "   Brain:  $(jq -r '.brain.enabled // false' "${CONFIG}" 2>/dev/null || echo 'false')"
echo "   Ollama: ${OLLAMA_URL:-not configured}"
echo ""

exec gino "$@"
