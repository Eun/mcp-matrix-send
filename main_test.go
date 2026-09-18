package main

import (
	"os"
	"testing"
	"time"
)

func TestMaybeSchedule_ImmediateWhenEmpty(t *testing.T) {
	handled, result := maybeSchedule(nil, "!room:example.com", "hi", "")
	if handled {
		t.Fatalf("expected not handled (immediate send) for empty send_at, got result %+v", result)
	}
}

func TestMaybeSchedule_ImmediateWhenPast(t *testing.T) {
	past := "2000-01-01T00:00:00Z"
	handled, _ := maybeSchedule(nil, "!room:example.com", "hi", past)
	if handled {
		t.Fatal("expected not handled (immediate send) for past send_at")
	}
}

func TestMaybeSchedule_InvalidTimestamp(t *testing.T) {
	handled, result := maybeSchedule(nil, "!room:example.com", "hi", "not-a-time")
	if !handled {
		t.Fatal("expected invalid timestamp to be handled")
	}
	if result == nil || !result.IsError {
		t.Fatalf("expected error result, got %+v", result)
	}
}

func TestMaybeSchedule_FutureQueues(t *testing.T) {
	path := t.TempDir() + "/schedule.json"
	s, err := NewScheduler(path, nil)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	handled, result := maybeSchedule(s, "!room:example.com", "later", future)
	if !handled {
		t.Fatal("expected future send_at to be handled (queued)")
	}
	if result == nil || result.IsError {
		t.Fatalf("expected success result, got %+v", result)
	}
	if s.Pending() != 1 {
		t.Fatalf("expected 1 pending message, got %d", s.Pending())
	}
}

func TestLoadConfig_Defaults(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	// unset optional
	os.Unsetenv("MCP_TRANSPORT")
	os.Unsetenv("MCP_LISTEN_ADDR")
	os.Unsetenv("MCP_BEARER_TOKEN")
	os.Unsetenv("MATRIX_ROOM_WHITELIST")
	os.Unsetenv("MATRIX_DEFAULT_ROOM")

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
	if cfg.DefaultRoom != "" {
		t.Errorf("expected empty default room, got %q", cfg.DefaultRoom)
	}
}

func TestLoadConfig_SchedulePathDefault(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	os.Unsetenv("MCP_TRANSPORT")
	os.Unsetenv("MATRIX_SCHEDULE_FILE")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchedulePath != "scheduled_messages.json" {
		t.Errorf("expected default schedule path, got %q", cfg.SchedulePath)
	}
}

func TestLoadConfig_SchedulePathOverride(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MATRIX_SCHEDULE_FILE", "/tmp/custom-schedule.json")
	os.Unsetenv("MCP_TRANSPORT")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.SchedulePath != "/tmp/custom-schedule.json" {
		t.Errorf("expected overridden schedule path, got %q", cfg.SchedulePath)
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

func TestLoadConfig_DefaultRoom(t *testing.T) {
	t.Setenv("MATRIX_HOMESERVER_URL", "https://matrix.example.com")
	t.Setenv("MATRIX_ACCESS_TOKEN", "token123")
	t.Setenv("MATRIX_USER_ID", "@bot:example.com")
	t.Setenv("MATRIX_DEFAULT_ROOM", "!default:example.com")
	os.Unsetenv("MCP_TRANSPORT")

	cfg, err := loadConfig()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.DefaultRoom != "!default:example.com" {
		t.Errorf("expected default room '!default:example.com', got %q", cfg.DefaultRoom)
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
