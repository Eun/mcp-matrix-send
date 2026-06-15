package main

import (
	"os"
	"testing"
)

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	// unset optional
	os.Unsetenv("MCP_TRANSPORT")
	os.Unsetenv("MCP_LISTEN_ADDR")
	os.Unsetenv("MCP_BEARER_TOKEN")
	os.Unsetenv("MATRIX_ROOM_WHITELIST")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Transport != transportStdio {
		t.Errorf("expected transport 'stdio', got %q", cfg.Transport)
	}
	if len(cfg.RoomWhitelist) != 0 {
		t.Errorf("expected empty whitelist, got %v", cfg.RoomWhitelist)
	}
}

func TestLoadConfig_HTTPRequiresBearer(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MCP_TRANSPORT", "http")
	os.Unsetenv("MCP_BEARER_TOKEN")

	_, err := loadConfig()
	if err == nil {
		t.Fatal("expected error for missing MCP_BEARER_TOKEN")
	}
}

func TestLoadConfig_RoomWhitelist(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MATRIX_ROOM_WHITELIST", "!room1:example.com, !room2:example.com")
	os.Unsetenv("MCP_TRANSPORT")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.RoomWhitelist) != 2 {
		t.Fatalf("expected 2 whitelist entries, got %d", len(cfg.RoomWhitelist))
	}
	if cfg.RoomWhitelist[0] != "!room1:example.com" {
		t.Errorf("unexpected whitelist[0]: %q", cfg.RoomWhitelist[0])
	}
	if cfg.RoomWhitelist[1] != "!room2:example.com" {
		t.Errorf("unexpected whitelist[1]: %q", cfg.RoomWhitelist[1])
	}
}

func TestIsRoomAllowed_EmptyWhitelist(t *testing.T) {
	cfg := &Config{}
	if !isRoomAllowed(cfg, "!any:example.com") {
		t.Error("expected all rooms allowed with empty whitelist")
	}
}

func TestIsRoomAllowed_WithWhitelist(t *testing.T) {
	cfg := &Config{
		RoomWhitelist: []string{"!allowed:example.com"},
	}
	if !isRoomAllowed(cfg, "!allowed:example.com") {
		t.Error("expected allowed room to pass")
	}
	if isRoomAllowed(cfg, "!denied:example.com") {
		t.Error("expected denied room to fail")
	}
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	tests := []struct {
		name  string
		unset string
	}{
		{"missing homeserver", "MATRIX_HOMESERVER_URL"},
		{"missing token", "MATRIX_ACCESS_TOKEN"},
		{"missing user id", "MATRIX_USER_ID"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
			t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
			t.Setenv("MATRIX_USER_ID", "@bot:example.com")
			os.Unsetenv("MCP_TRANSPORT")
			os.Unsetenv(tt.unset)

			_, err := loadConfig()
			if err == nil {
				t.Fatalf("expected error when %s is missing", tt.unset)
			}
		})
	}
}
