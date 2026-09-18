package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

const (
	transportStdio = "stdio"
	transportHTTP  = "http"
)

var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Config holds the server configuration parsed from environment variables.
type Config struct {
	// Matrix settings.
	HomeserverURL string
	AccessToken   string
	UserID        string
	DefaultRoom   string // optional default room ID from MATRIX_DEFAULT_ROOM

	// MCP transport settings.
	Transport  string // "stdio" or "http"
	ListenAddr string // address for HTTP transport (e.g. ":8080")

	// Security.
	BearerToken string // required for HTTP transport

	// Room whitelist (comma-separated). Empty means all rooms are allowed.
	RoomWhitelist []string

	// SchedulePath is the JSON file where scheduled (future) messages are
	// persisted so they survive a process crash or restart.
	SchedulePath string
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		HomeserverURL: os.Getenv("MATRIX_HOMESERVER_URL"),
		AccessToken:   os.Getenv("MATRIX_ACCESS_TOKEN"),
		UserID:        os.Getenv("MATRIX_USER_ID"),
		DefaultRoom:   os.Getenv("MATRIX_DEFAULT_ROOM"),
		Transport:     os.Getenv("MCP_TRANSPORT"),
		ListenAddr:    os.Getenv("MCP_LISTEN_ADDR"),
		BearerToken:   os.Getenv("MCP_BEARER_TOKEN"),
		SchedulePath:  os.Getenv("MATRIX_SCHEDULE_FILE"),
	}

	if cfg.SchedulePath == "" {
		cfg.SchedulePath = "scheduled_messages.json"
	}

	if cfg.HomeserverURL == "" {
		return nil, fmt.Errorf("MATRIX_HOMESERVER_URL is required")
	}
	if cfg.AccessToken == "" {
		return nil, fmt.Errorf("MATRIX_ACCESS_TOKEN is required")
	}
	if cfg.UserID == "" {
		return nil, fmt.Errorf("MATRIX_USER_ID is required")
	}

	if cfg.Transport == "" {
		cfg.Transport = transportStdio
	}
	if cfg.Transport != transportStdio && cfg.Transport != transportHTTP {
		return nil, fmt.Errorf("MCP_TRANSPORT must be 'stdio' or 'http'")
	}

	if cfg.Transport == transportHTTP {
		if cfg.ListenAddr == "" {
			cfg.ListenAddr = ":8080"
		}
		if cfg.BearerToken == "" {
			return nil, fmt.Errorf("MCP_BEARER_TOKEN is required for HTTP transport")
		}
	}

	if whitelist := os.Getenv("MATRIX_ROOM_WHITELIST"); whitelist != "" {
		for _, r := range strings.Split(whitelist, ",") {
			r = strings.TrimSpace(r)
			if r != "" {
				cfg.RoomWhitelist = append(cfg.RoomWhitelist, r)
			}
		}
	}

	return cfg, nil
}

func isRoomAllowed(cfg *Config, roomID string) bool {
	if len(cfg.RoomWhitelist) == 0 {
		return true
	}
	for _, allowed := range cfg.RoomWhitelist {
		if allowed == roomID {
			return true
		}
	}
	return false
}

func newMatrixClient(cfg *Config) (*mautrix.Client, error) {
	client, err := mautrix.NewClient(cfg.HomeserverURL, id.UserID(cfg.UserID), cfg.AccessToken)
	if err != nil {
		return nil, fmt.Errorf("creating matrix client: %w", err)
	}
	return client, nil
}

func buildMCPServer(cfg *Config, matrixClient *mautrix.Client, scheduler *Scheduler) *server.MCPServer {
	mcpServer := server.NewMCPServer(
		"mcp-matrix-send",
		version,
		server.WithToolCapabilities(true),
	)

	sendMessageTool := mcp.NewTool("send_message",
		mcp.WithDescription("Send a message to a Matrix room, optionally at a later time"),
		mcp.WithString("room_id",
			mcp.Description(
				"The Matrix room ID (e.g. !abc123:example.com). "+
					"Optional if a default room is configured on the server.",
			),
		),
		mcp.WithString("message",
			mcp.Description("The message text to send"),
			mcp.Required(),
		),
		mcp.WithString("send_at",
			mcp.Description(
				"Optional RFC3339 timestamp (e.g. 2025-01-02T15:04:05Z) at which to "+
					"send the message. When omitted or in the past, the message is sent "+
					"immediately. Scheduled messages are persisted to disk and survive "+
					"a process restart.",
			),
		),
	)

	mcpServer.AddTool(sendMessageTool, sendMessageHandler(cfg, matrixClient, scheduler))

	return mcpServer
}

func sendMessageHandler(cfg *Config, matrixClient *mautrix.Client, scheduler *Scheduler) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		roomID, _ := request.GetArguments()["room_id"].(string)
		if roomID == "" {
			roomID = cfg.DefaultRoom
		}
		if roomID == "" {
			return mcp.NewToolResultError("room_id is required (no default room configured)"), nil
		}
		message, ok := request.GetArguments()["message"].(string)
		if !ok || message == "" {
			return mcp.NewToolResultError("message is required"), nil
		}

		if !isRoomAllowed(cfg, roomID) {
			return mcp.NewToolResultError(fmt.Sprintf("room %s is not in the whitelist", roomID)), nil
		}

		// Parse the optional schedule time. When absent (or already in the
		// past) the message is sent immediately.
		sendAtRaw, _ := request.GetArguments()["send_at"].(string)
		if handled, result := maybeSchedule(scheduler, roomID, message, sendAtRaw); handled {
			return result, nil
		}

		_, err := matrixClient.SendMessageEvent(ctx, id.RoomID(roomID), event.EventMessage, &event.MessageEventContent{
			MsgType: event.MsgText,
			Body:    message,
		})
		if err != nil {
			return mcp.NewToolResultError(fmt.Sprintf("failed to send message: %v", err)), nil
		}

		return mcp.NewToolResultText("message sent successfully"), nil
	}
}

// maybeSchedule inspects the optional send_at argument. It returns handled=true
// with a tool result when the request should not be sent immediately: either an
// invalid timestamp, or a future timestamp that has been queued for later
// delivery. When send_at is empty or already in the past it returns
// handled=false so the caller sends the message right away.
func maybeSchedule(scheduler *Scheduler, roomID, message, sendAtRaw string) (bool, *mcp.CallToolResult) {
	sendAtRaw = strings.TrimSpace(sendAtRaw)
	if sendAtRaw == "" {
		return false, nil
	}

	sendAt, err := time.Parse(time.RFC3339, sendAtRaw)
	if err != nil {
		return true, mcp.NewToolResultError(
			fmt.Sprintf("invalid send_at %q: expected RFC3339 timestamp (e.g. 2025-01-02T15:04:05Z)", sendAtRaw),
		)
	}
	if !sendAt.After(time.Now()) {
		// Timestamp is in the past: send immediately.
		return false, nil
	}
	if scheduler == nil {
		return true, mcp.NewToolResultError("scheduling is not available")
	}

	msg, err := scheduler.Schedule(roomID, message, sendAt)
	if err != nil {
		return true, mcp.NewToolResultError(fmt.Sprintf("failed to schedule message: %v", err))
	}
	return true, mcp.NewToolResultText(fmt.Sprintf(
		"message scheduled for %s (id %s)", msg.SendAt.Format(time.RFC3339), msg.ID,
	))
}

// bearerAuthMiddleware validates the Authorization header for HTTP transport.
func bearerAuthMiddleware(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		expected := "Bearer " + token
		if auth != expected {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func run() error {
	cfg, err := loadConfig()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	matrixClient, err := newMatrixClient(cfg)
	if err != nil {
		return err
	}

	scheduler, err := NewScheduler(cfg.SchedulePath, matrixClient)
	if err != nil {
		return fmt.Errorf("initializing scheduler: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	scheduler.Start(ctx)
	defer scheduler.Stop()

	if n := scheduler.Pending(); n > 0 {
		//nolint:gosec // schedule path is operator-supplied startup info
		log.Printf("restored %d scheduled message(s) from %s", n, cfg.SchedulePath)
	}

	mcpServer := buildMCPServer(cfg, matrixClient, scheduler)

	switch cfg.Transport {
	case transportStdio:
		log.Printf("starting mcp-matrix-send %s (commit=%s, date=%s) on stdio", version, commit, date)
		if err := server.ServeStdio(mcpServer); err != nil {
			return fmt.Errorf("stdio server: %w", err)
		}
	case transportHTTP:
		//nolint:gosec // log injection not a concern for server startup info
		log.Printf("starting mcp-matrix-send %s (commit=%s, date=%s) on %s",
			version, commit, date, cfg.ListenAddr)

		httpServer := server.NewStreamableHTTPServer(mcpServer)

		mux := http.NewServeMux()
		mux.Handle("/mcp", bearerAuthMiddleware(cfg.BearerToken, httpServer))

		srv := &http.Server{
			Addr:              cfg.ListenAddr,
			Handler:           mux,
			ReadHeaderTimeout: 10 * time.Second,
		}

		go func() {
			<-ctx.Done()
			log.Println("shutting down HTTP server")
			if err := srv.Shutdown(context.Background()); err != nil {
				log.Printf("HTTP server shutdown error: %v", err)
			}
		}()

		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return fmt.Errorf("HTTP server: %w", err)
		}
	}

	return nil
}

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
