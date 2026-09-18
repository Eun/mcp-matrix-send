# mcp-matrix-send

An MCP (Model Context Protocol) server that sends messages to Matrix rooms. Supports both **stdio** and **HTTP** transports.

## Features

- **Send messages** to any Matrix room via the `send_message` MCP tool
- **Schedule messages** for later delivery with the optional `send_at` parameter
- **Crash-safe scheduling** — pending scheduled messages are persisted to a JSON file and re-sent after a restart
- **Stdio transport** for local agent integration
- **HTTP transport** with bearer token authentication
- **Room whitelist** to restrict which rooms agents can message

## Configuration

All configuration is via environment variables:

| Variable | Required | Description |
|---|---|---|
| `MATRIX_HOMESERVER_URL` | Yes | Matrix homeserver URL (e.g. `https://matrix.example.com`) |
| `MATRIX_ACCESS_TOKEN` | Yes | Matrix bot access token |
| `MATRIX_USER_ID` | Yes | Matrix bot user ID (e.g. `@bot:example.com`) |
| `MCP_TRANSPORT` | No | `stdio` (default) or `http` |
| `MCP_LISTEN_ADDR` | No | HTTP listen address (default `:8080`) |
| `MCP_BEARER_TOKEN` | HTTP only | Bearer token for HTTP authentication (required for HTTP transport) |
| `MATRIX_DEFAULT_ROOM` | No | Default Matrix room ID. When set, `room_id` becomes optional in tool calls |
| `MATRIX_ROOM_WHITELIST` | No | Comma-separated list of allowed room IDs. Empty = all rooms allowed |
| `MATRIX_SCHEDULE_FILE` | No | Path to the JSON file used to persist scheduled messages (default `scheduled_messages.json`) |

## Usage

### Stdio mode (default)

```bash
export MATRIX_HOMESERVER_URL="https://matrix.example.com"
export MATRIX_ACCESS_TOKEN="syt_..."
export MATRIX_USER_ID="@bot:example.com"

./mcp-matrix-send
```

### HTTP mode

```bash
export MATRIX_HOMESERVER_URL="https://matrix.example.com"
export MATRIX_ACCESS_TOKEN="syt_..."
export MATRIX_USER_ID="@bot:example.com"
export MCP_TRANSPORT="http"
export MCP_BEARER_TOKEN="my-secret-token"
export MCP_LISTEN_ADDR=":8080"

./mcp-matrix-send
```

The MCP endpoint is available at `/mcp`. All requests require an `Authorization: Bearer <token>` header.

### Room whitelist

```bash
export MATRIX_ROOM_WHITELIST="!room1:example.com,!room2:example.com"
```

When set, the server only allows sending messages to the listed room IDs. If unset or empty, all rooms are allowed.

## MCP Tool

### `send_message`

Send a text message to a Matrix room.

**Parameters:**
- `room_id` (string, required) — The Matrix room ID (e.g. `!abc123:example.com`). Optional if `MATRIX_DEFAULT_ROOM` is configured.
- `message` (string, required) — The message text to send
- `send_at` (string, optional) — RFC3339 timestamp (e.g. `2025-01-02T15:04:05Z`) at which to send the message. When omitted or in the past, the message is sent immediately.

#### Scheduling

When `send_at` is set to a future time, the message is queued and delivered by a
background scheduler at that time. Scheduled messages are written to the JSON
file configured by `MATRIX_SCHEDULE_FILE` (default `scheduled_messages.json`)
**before** the tool call returns, so they are not lost if the process crashes or
restarts — on startup the server reloads the file and re-arms any pending
messages. A delivered message is removed from the file; a message that fails to
send is retried on the next scheduler tick.

The file is a JSON array of objects:

```json
[
  {
    "id": "b1c2...",
    "room_id": "!abc123:example.com",
    "message": "hello later",
    "send_at": "2025-01-02T15:04:05Z"
  }
]
```

## Docker

```bash
docker run \
  -e MATRIX_HOMESERVER_URL="https://matrix.example.com" \
  -e MATRIX_ACCESS_TOKEN="syt_..." \
  -e MATRIX_USER_ID="@bot:example.com" \
  -e MCP_TRANSPORT="http" \
  -e MCP_BEARER_TOKEN="my-secret-token" \
  -p 8080:8080 \
  ghcr.io/eun/mcp-matrix-send:latest
```

## Development

This project uses [mise](https://mise.jdx.dev/) for tooling.

```bash
mise run build   # build the binary
mise run test    # run tests
mise run lint    # run linter
mise run release # snapshot release
mise run clean   # remove build artifacts
```

## License

MIT
