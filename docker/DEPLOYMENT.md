# Gino Gateway — Multi-Tenant Assistant Deployment

This guide covers deploying the Gino multi-tenant assistant gateway. For single-agent (YOLO) deployments, use the [slim Dockerfile](Dockerfile.slim) instead.

## Architecture

```
                      ┌─────────────────────────────────┐
                      │          Caddy (TLS)            │
                      │   your-domain.com → :443        │
                      └──────────────┬──────────────────┘
                                     │
                    ┌────────────────▼──────────────────┐
                    │      Gino Gateway (:8080)         │
                    │                                   │
                    │  /api/v1/chat/sync   — Chat API   │
                    │  /api/v1/chat/stream — SSE stream │
                    │  /api/v1/admin/*     — Admin API  │
                    │  /admin/             — Admin UI   │
                    │                                   │
                    │  Per-user workspaces              │
                    │  Per-user brain.db                │
                    │  Per-user memory store            │
                    │  tenant.db (users/tiers/MCP)      │
                    │  audit.db (messages/usage)        │
                    └────────────────┬──────────────────┘
                                     │
                    ┌────────────────▼──────────────────┐
                    │     Ollama (external, shared)     │
                    │     port 11434                    │
                    │     Model: nomic-embed-text       │
                    └───────────────────────────────────┘
```

## Components

### 1. Gino Gateway Container

Single process, multi-tenant. Handles API requests, admin UI, audit trail, and per-user isolation.

- **Image**: `gino:gateway` (built from `Dockerfile.gateway`)
- **Port**: 8080 (API + Admin UI)
- **Volume**: `/home/gino/.gino` — all persistent data
- **No bundled Ollama** — requires external instance

### 2. Ollama (External)

Shared embedding service for all users' brain databases. Must be reachable from the gateway container.

- Pull the embedding model: `ollama pull nomic-embed-text`
- Can run as a separate container, on the host, or on another machine

### 3. Caddy (External)

Your existing Caddy reverse proxy handles TLS termination and proxies to the gateway.

#### Caddyfile example

```
gino.your-domain.com {
    reverse_proxy gino-gateway:8080
}
```

If Caddy runs on the host (not in Docker):

```
gino.your-domain.com {
    reverse_proxy localhost:8080
}
```

## Quick Start

1. **Clone and build:**

   ```bash
   git clone https://github.com/wltechblog/gino.git
   cd gino/docker
   ```

2. **Configure:**

   ```bash
   cp .env.gateway.example .env
   # Edit .env — set OPENAI_API_KEY, OLLAMA_URL, GINO_API_ADMIN_SECRET
   ```

3. **Deploy:**

   ```bash
   docker compose -f docker-compose.gateway.yml up -d --build
   ```

4. **Create your first admin user:**

   ```bash
   # Access the admin UI at https://gino.your-domain.com/admin/
   # Log in with GINO_API_ADMIN_SECRET
   # Create a user with tier "admin"
   ```

5. **Test the API:**

   ```bash
   curl https://gino.your-domain.com/api/v1/chat/sync \
     -H "Authorization: Bearer <user-token>" \
     -H "Content-Type: application/json" \
     -d '{"message": "Hello!"}'
   ```

## Configuration

All configuration is via environment variables (`.env` file) or `config.json` on the data volume.

### Key environment variables

| Variable | Required | Description |
|---|---|---|
| `OPENAI_API_KEY` | Yes | LLM provider API key |
| `OPENAI_API_BASE` | Yes | LLM provider base URL |
| `GINO_MODEL` | Yes | Default model identifier |
| `OLLAMA_URL` | Yes | External Ollama URL for embeddings |
| `GINO_API_ADMIN_SECRET` | Yes | Secret for admin UI cookie signing |
| `GINO_API_PORT` | No | Host port mapping (default: 8080) |
| `GINO_DATA_PATH` | No | Host data path (default: /opt/gino/gateway-data) |

### Config file

On first start, a default multi-tenant `config.json` is generated with:
- Three tiers: `free`, `pro`, `admin`
- API channel enabled on `:8080`
- Audit enabled (7-day messages, 365-day usage)
- Brain enabled with `nomic-embed-text`

You can edit `config.json` directly on the data volume for advanced configuration (MCP servers, tier model overrides, etc.) or use the admin UI.

## Admin UI

Accessible at `/admin/` on the gateway port. Log in with the `GINO_API_ADMIN_SECRET`.

Features:
- **Dashboard** — user count, tier count, MCP server count, token usage summary
- **Users** — create/edit/delete users, assign tiers, manage channel mappings
- **Tiers** — create/edit tiers with model overrides, tool whitelists, rate limits
- **MCP Servers** — add/remove MCP servers with env vars and API keys (per-user or global)

## Admin API

All endpoints under `/api/v1/admin/` require an admin-tier user token.

### Users

```
GET    /api/v1/admin/users              # List all users
POST   /api/v1/admin/users              # Create user
GET    /api/v1/admin/users/{id}         # Get user
PUT    /api/v1/admin/users/{id}         # Update user
DELETE /api/v1/admin/users/{id}         # Delete user
```

### Tiers

```
GET    /api/v1/admin/tiers              # List all tiers
POST   /api/v1/admin/tiers              # Create/update tier
DELETE /api/v1/admin/tiers/{name}       # Delete tier
```

### MCP Servers

```
GET    /api/v1/admin/mcp                # List all MCP servers
POST   /api/v1/admin/mcp                # Add MCP server (global or per-user)
GET    /api/v1/admin/mcp/{userID}       # List MCP servers for a user
DELETE /api/v1/admin/mcp/{name}         # Delete MCP server
```

## Data Persistence

All data lives under the volume mount (`/home/gino/.gino`):

```
/home/gino/.gino/
├── config.json              # Main configuration
├── data/
│   ├── tenant.db            # Users, tiers, MCP servers
│   ├── audit.db             # Message log + token usage
│   └── workspaces/          # Per-user isolated workspaces
│       ├── user1/
│       │   ├── brain.db     # User's private knowledge brain
│       │   ├── memory/      # User's memory files
│       │   └── sessions/    # User's session history
│       ├── user2/
│       │   └── ...
│       └── ...
├── workspace/
│   ├── memory/              # Default user's memory
│   └── skills/              # Shared skills
```

## Security Model

### Isolation guarantees

- **Sessions**: User ID is injected into every session key. Users cannot access each other's history.
- **Workspaces**: `exec` and `filesystem` tools resolve paths from the user's workspace root. Path traversal is blocked.
- **Brain/Memory**: Each user gets their own SQLite brain DB and memory store via `ResourcePool`.
- **Rate limiting**: Per-tier limits enforced on both API middleware and agent loop.
- **Tool filtering**: Tiers define allowed tool whitelists. Users only see tools their tier permits.

### What is NOT isolated

- The LLM provider is shared. All users hit the same API key. Per-tier model selection controls which model, not which provider account.
- Ollama is shared. All users' brain DBs send embedding requests to the same instance.

## Backup

```bash
# Stop the container, copy the data volume, restart
docker compose -f docker-compose.gateway.yml stop
cp -r /opt/gino/gateway-data /backup/gino-gateway-$(date +%Y%m%d)
docker compose -f docker-compose.gateway.yml start
```

## Upgrading

```bash
cd gino
git pull origin feat/api-gateway
cd docker
docker compose -f docker-compose.gateway.yml up -d --build
```

The binary is stateless. All state is on the volume. Upgrades are zero-downtime if you use a rolling deploy.
