# mcp-matrix-send

An MCP (Model Context Protocol) server that sends messages to Matrix rooms. Supports both **stdio** and **HTTP** transports.

## Features

- **Send messages** to any Matrix room via the `send_message` MCP tool
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
| `MATRIX_ROOM_WHITELIST` | No | Comma-separated list of allowed room IDs. Empty = all rooms allowed |

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
- `room_id` (string, required) — The Matrix room ID (e.g. `!abc123:example.com`)
- `message` (string, required) — The message text to send

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
