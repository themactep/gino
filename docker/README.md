# Docker Deployment

Run Gino as a Docker container — one command to start, with optional built-in Ollama.

## Quick Start

### 1. Configure

```sh
cd docker
cp .env.example .env
# Edit .env — at minimum set OPENAI_API_KEY and a channel token
```

### 2. Start

```sh
docker compose up -d
```

### 3. Check logs

```sh
docker compose logs -f
```

That's it. The container handles everything else.

## What's Included

- **Gino** — the full agent runtime
- **Ollama** — bundled for knowledge brain embeddings (only starts when `GINO_BRAIN_ENABLED=true`)
- **Auto-config** — environment variables in `.env` are applied to config automatically on each start

## Using the Knowledge Brain

The brain provides hybrid search and a knowledge graph over your workspace. It needs an embedding service.

**Option A — Bundled Ollama (simplest)**

```env
GINO_BRAIN_ENABLED=true
# That's it — Ollama starts automatically inside the container
```

**Option B — External Ollama** (e.g. installed natively on host)

```env
GINO_BRAIN_ENABLED=true
OLLAMA_URL=http://host.docker.internal:11434
# Or use your server IP: http://192.168.1.100:11434
```

When `OLLAMA_URL` is set, the bundled Ollama is skipped and Gino connects to the external one.

**Option C — Remote embedding API** (no Ollama at all)

```env
GINO_BRAIN_ENABLED=true
GINO_BRAIN_REMOTE_API_BASE=https://api.openai.com/v1
GINO_BRAIN_REMOTE_API_KEY=sk-...
GINO_BRAIN_REMOTE_MODEL=text-embedding-3-small
```

## Environment Variables

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OPENAI_API_KEY` | **Yes** | — | OpenAI-compatible API key |
| `OPENAI_API_BASE` | No | `https://openrouter.ai/api/v1` | API base URL |
| `GINO_MODEL` | No | `google/gemini-2.5-flash` | LLM model identifier |
| `GINO_MAX_TOKENS` | No | `8192` | Max response tokens |
| `GINO_MAX_TOOL_ITERATIONS` | No | `100` | Max tool iterations per message |
| `TELEGRAM_BOT_TOKEN` | No | — | Telegram bot token |
| `TELEGRAM_ALLOW_FROM` | No | — | Comma-separated Telegram user IDs |
| `DISCORD_BOT_TOKEN` | No | — | Discord bot token |
| `DISCORD_ALLOW_FROM` | No | — | Comma-separated Discord user IDs |
| `SLACK_APP_TOKEN` | No | — | Slack app token (`xapp-...`) |
| `SLACK_BOT_TOKEN` | No | — | Slack bot token (`xoxb-...`) |
| `SLACK_ALLOW_USERS` | No | — | Comma-separated Slack user IDs |
| `SLACK_ALLOW_CHANNELS` | No | — | Comma-separated Slack channel IDs |
| `GINO_BRAIN_ENABLED` | No | `false` | Enable knowledge brain |
| `GINO_BRAIN_EMBEDDING_MODEL` | No | `nomic-embed-text` | Ollama embedding model |
| `OLLAMA_URL` | No | *(bundled)* | External Ollama URL — skips bundled Ollama |
| `GINO_BRAIN_REMOTE_API_BASE` | No | — | Remote embedding API base URL |
| `GINO_BRAIN_REMOTE_API_KEY` | No | — | Remote embedding API key |
| `GINO_BRAIN_REMOTE_MODEL` | No | — | Remote embedding model name |
| `GINO_DATA_PATH` | No | `./gino-data` | Host path for data persistence |

## Data Persistence

All data persists in the `GINO_DATA_PATH` bind mount (default: `./gino-data`):

```
gino-data/
  config.json        — configuration
  workspace/         — agent workspace
    memory/          — daily notes and long-term memory
    skills/          — custom skills
  brain.db           — knowledge brain database (when enabled)
  .ollama/models/    — Ollama models (when using bundled Ollama)
```

## Docker Run (without Compose)

```sh
docker build -f docker/Dockerfile -t gino .

docker run -d \
  --name gino \
  --restart unless-stopped \
  -e OPENAI_API_KEY="sk-..." \
  -e TELEGRAM_BOT_TOKEN="123:ABC..." \
  -v /path/to/gino-data:/home/gino/.gino \
  gino
```

## Architecture

```
┌─────────────────────────────────────┐
│           Docker Container          │
│                                     │
│  ┌──────────┐    ┌───────────────┐  │
│  │  Ollama  │    │     Gino      │  │
│  │ (bundled)│◄──►│   (agent)     │  │
│  └──────────┘    └───────────────┘  │
│       │              │              │
│       ▼              ▼              │
│  .ollama/        .gino/             │
│  models/         workspace/         │
│                  brain.db           │
│                                     │
│  ◄── bind mount from host ──►     │
└─────────────────────────────────────┘
```
