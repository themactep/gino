# Gino API Protocol v1

A REST + Server-Sent Events (SSE) protocol for communicating with a Gino agent
instance. Designed for mobile, desktop, and web clients.

## Base URL

All endpoints are prefixed with `/api/v1/`.

```
http://your-server:8443/api/v1/
```

## Authentication

All endpoints except `/health` and `/info` require bearer token authentication.

```
Authorization: Bearer <your-token>
```

Tokens are configured server-side in `config.json` under `channels.api.tokens`.
Each token maps to a user ID for session scoping.

For development, `channels.api.allowAnon: true` disables auth (all requests
are attributed to user "anonymous").

---

## Endpoints

### Health Check

```
GET /api/v1/health
```

No authentication required. Returns server status.

**Response:**
```json
{
  "status": "ok",
  "version": "0.4.0",
  "uptime": "4h32m15s",
  "activeTurns": 3
}
```

---

### Server Info

```
GET /api/v1/info
```

No authentication required. Returns server capabilities.

**Response:**
```json
{
  "model": "glm-5.2",
  "tools": ["exec", "filesystem", "web_search", "brain_search", "write_memory"],
  "maxIterations": 100,
  "visionSupported": true,
  "version": "0.4.0"
}
```

---

### Synchronous Chat

```
POST /api/v1/chat/sync
```

Sends a message and waits for the complete response. Simple but blocking —
no streaming. Use this for quick queries or when SSE is unavailable.

**Request:**
```json
{
  "message": "What files are in my workspace?",
  "session": "my-project",
  "media": []
}
```

| Field    | Type     | Required | Description |
|----------|----------|----------|-------------|
| message  | string   | yes      | User input text |
| session  | string   | no       | Session identifier for multi-turn conversations. Defaults to "default". |
| media    | string[] | no       | Base64-encoded data URLs for images/files |

**Response (200):**
```json
{
  "response": "Here are the files in your workspace:\n- main.go\n- README.md\n...",
  "session": "my-project",
  "iterations": 3,
  "toolCalls": []
}
```

**Errors:**
| Status | Meaning |
|--------|---------|
| 400    | Missing or invalid `message` |
| 401    | Missing or invalid auth token |
| 504    | Agent did not respond within timeout (default 120s) |

---

### Streaming Chat (SSE)

Real-time streaming uses a two-endpoint pattern:

#### 1. Open SSE Connection

```
GET /api/v1/chat
Accept: text/event-stream
```

Opens a persistent SSE connection. The server immediately sends a
`connected` event:

```
event: connected
data: {"connectionId":"conn-1-1700000000000","server":"0.4.0"}

```

Save the `connectionId` — it's required for sending messages.

#### 2. Send Messages

```
POST /api/v1/chat/stream
Content-Type: application/json

{
  "connectionId": "conn-1-1700000000000",
  "message": "Write a Python script to sort a CSV file",
  "session": "coding-session"
}
```

**Response (202):**
```json
{"status": "queued"}
```

The agent's response streams back through the SSE connection as multiple
events (see below).

#### SSE Event Types

##### `connected`
Sent immediately after connection. Contains the connection ID.

```
event: connected
data: {"connectionId":"conn-1-...","server":"0.4.0"}
```

##### `tool` (optional)
Tool activity notification. Sent when the agent starts executing a tool.
Useful for showing "thinking..." indicators in the UI.

```
event: tool
data: {"content":"📁 Running exec...","timestamp":"2026-07-25T10:30:00Z"}
```

##### `message`
Agent's response text. May be sent in chunks or all at once depending on
the agent's turn structure.

```
event: message
data: {"content":"Here's the script you requested...","timestamp":"2026-07-25T10:30:05Z"}
```

##### `error`
Error from the agent or a tool execution.

```
event: error
data: {"content":"Failed to execute command: exit code 1","timestamp":"2026-07-25T10:30:03Z"}
```

##### `done`
Signals the end of a turn. The agent has finished processing and is awaiting
the next message. The client can send another message via POST or close the
connection.

```
event: done
data: {"content":"Turn complete","timestamp":"2026-07-25T10:30:10Z"}
```

##### Heartbeats
The server sends comment lines (`: heartbeat`) every 30 seconds to keep the
connection alive through proxies. These are not events and should be ignored
by the client.

#### Connection Lifecycle

```
Client                          Server
  │                               │
  │──── GET /api/v1/chat ────────▶│  (open SSE)
  │◀─── event: connected ─────────│
  │                               │
  │──── POST /api/v1/chat/stream ─▶│  (send message)
  │◀─── 202 Accepted ─────────────│
  │                               │
  │◀─── event: tool ──────────────│  (optional, 0+ times)
  │◀─── event: tool ──────────────│
  │◀─── event: message ───────────│  (1+ response chunks)
  │◀─── event: done ──────────────│  (turn finished)
  │                               │
  │──── POST /api/v1/chat/stream ─▶│  (next message)
  │◀─── event: tool ──────────────│
  │◀─── event: message ───────────│
  │◀─── event: done ──────────────│
  │                               │
  │──── (close connection) ──────▶│
  │                               │
```

---

### Session Management

#### List Sessions

```
GET /api/v1/sessions
```

Returns sessions for the authenticated user only.

**Response:**
```json
{
  "sessions": [
    {
      "key": "api:josh:default",
      "title": "CSV sorting task",
      "messages": 12,
      "createdAt": "2026-07-25T10:00:00Z",
      "updatedAt": "2026-07-25T10:30:00Z"
    }
  ]
}
```

#### Create Session

```
POST /api/v1/sessions

{"key": "my-project"}
```

**Response (201):**
```json
{"key": "api:josh:my-project"}
```

#### Delete Session

```
DELETE /api/v1/sessions/my-project
```

**Response (200):**
```json
{"status": "deleted"}
```

---

## Configuration

### Server-side (config.json)

```json
{
  "channels": {
    "api": {
      "enabled": true,
      "addr": ":8443",
      "tokens": {
        "secret-token-1": "josh",
        "secret-token-2": "alice"
      },
      "allowAnon": false,
      "requestTimeoutS": 120
    }
  }
}
```

### Behind a Reverse Proxy

For production, run behind Caddy/nginx with TLS termination:

**Caddy example:**
```
api.yourdomain.com {
    reverse_proxy localhost:8443
}
```

The API server sets `X-Accel-Buffering: no` on SSE responses to disable
nginx buffering automatically.

---

## Client Implementation Notes

### Reconnection

SSE has built-in reconnection. The browser `EventSource` API handles this
automatically. For native clients:

1. On disconnect, wait 3 seconds then reconnect
2. Use `Last-Event-ID` header to resume (server tracks message IDs)
3. Queue any messages sent during disconnection and retry after reconnect

### Message Ordering

Messages within a single SSE connection are guaranteed ordered. Messages
across different connections (different tabs/devices) are not coordinated.

### Rate Limiting (Future)

Rate limiting will be enforced per-token based on tier configuration. The
server will return `429 Too Many Requests` with a `Retry-After` header.

### Mobile Considerations

- SSE works on iOS and Android via background tasks
- For true background push, consider a push notification service (FCM/APNs)
  layered on top of the API
- Battery: close the SSE connection when the app is backgrounded and poll
  `/health` on foreground to check for queued messages
- Use gzip compression for POST bodies (especially with media attachments)

### Desktop Considerations

- SSE connections are long-lived; handle system sleep/wake by reconnecting
- For clipboard/file integration, use the `/media` field to send file contents
- Consider WebSocket upgrade if bidirectional streaming is needed (future)
