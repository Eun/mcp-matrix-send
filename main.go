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

	// MCP transport settings.
	Transport  string // "stdio" or "http"
	ListenAddr string // address for HTTP transport (e.g. ":8080")

	// Security.
	BearerToken string // required for HTTP transport

	// Room whitelist (comma-separated). Empty means all rooms are allowed.
	RoomWhitelist []string
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		HomeserverURL: os.Getenv("MATRIX_HOMESERVER_URL"),
		AccessToken:   os.Getenv("MATRIX_ACCESS_TOKEN"),
		UserID:        os.Getenv("MATRIX_USER_ID"),
		Transport:     os.Getenv("MCP_TRANSPORT"),
		ListenAddr:    os.Getenv("MCP_LISTEN_ADDR"),
		BearerToken:   os.Getenv("MCP_BEARER_TOKEN"),
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

func buildMCPServer(cfg *Config, matrixClient *mautrix.Client) *server.MCPServer {
	mcpServer := server.NewMCPServer(
		"mcp-matrix-send",
		version,
		server.WithToolCapabilities(true),
	)

	sendMessageTool := mcp.NewTool("send_message",
		mcp.WithDescription("Send a message to a Matrix room"),
		mcp.WithString("room_id",
			mcp.Description("The Matrix room ID (e.g. !abc123:example.com)"),
			mcp.Required(),
		),
		mcp.WithString("message",
			mcp.Description("The message text to send"),
			mcp.Required(),
		),
	)

	mcpServer.AddTool(sendMessageTool, sendMessageHandler(cfg, matrixClient))

	return mcpServer
}

func sendMessageHandler(cfg *Config, matrixClient *mautrix.Client) server.ToolHandlerFunc {
	return func(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		roomID, ok := request.GetArguments()["room_id"].(string)
		if !ok || roomID == "" {
			return mcp.NewToolResultError("room_id is required"), nil
		}
		message, ok := request.GetArguments()["message"].(string)
		if !ok || message == "" {
			return mcp.NewToolResultError("message is required"), nil
		}

		if !isRoomAllowed(cfg, roomID) {
			return mcp.NewToolResultError(fmt.Sprintf("room %s is not in the whitelist", roomID)), nil
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

	mcpServer := buildMCPServer(cfg, matrixClient)

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

		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()

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
