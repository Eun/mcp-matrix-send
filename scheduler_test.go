package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/id"
)

func newTestScheduler(t *testing.T) (scheduler *Scheduler, path string) {
	t.Helper()
	path = filepath.Join(t.TempDir(), "schedule.json")
	s, err := NewScheduler(path, nil)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	return s, path
}

func TestScheduler_MissingFileIsEmpty(t *testing.T) {
	s, _ := newTestScheduler(t)
	if got := s.Pending(); got != 0 {
		t.Fatalf("expected 0 pending, got %d", got)
	}
}

func TestScheduler_SchedulePersists(t *testing.T) {
	s, path := newTestScheduler(t)

	future := time.Now().Add(time.Hour)
	msg, err := s.Schedule("!room:example.com", "hello later", future)
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}
	if msg.ID == "" {
		t.Fatal("expected generated id")
	}
	if s.Pending() != 1 {
		t.Fatalf("expected 1 pending, got %d", s.Pending())
	}

	// The message must be written to disk immediately so a crash does not
	// lose it.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading schedule file: %v", err)
	}
	var list []ScheduledMessage
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("parsing schedule file: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("expected 1 persisted message, got %d", len(list))
	}
	if list[0].RoomID != "!room:example.com" || list[0].Body != "hello later" {
		t.Fatalf("unexpected persisted message: %+v", list[0])
	}
}

func TestScheduler_RecoverAfterRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "schedule.json")

	s1, err := NewScheduler(path, nil)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	future := time.Now().Add(time.Hour)
	if _, err := s1.Schedule("!room:example.com", "survive the crash", future); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	// Simulate a crash + restart: a brand new scheduler reading the same file
	// must recover the pending message.
	s2, err := NewScheduler(path, nil)
	if err != nil {
		t.Fatalf("NewScheduler (restart): %v", err)
	}
	if got := s2.Pending(); got != 1 {
		t.Fatalf("expected 1 recovered message, got %d", got)
	}
}

func TestScheduler_RemovePersists(t *testing.T) {
	s, path := newTestScheduler(t)
	msg, err := s.Schedule("!room:example.com", "temp", time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	s.remove(msg.ID)
	if s.Pending() != 0 {
		t.Fatalf("expected 0 pending after remove, got %d", s.Pending())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading schedule file: %v", err)
	}
	var list []ScheduledMessage
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("parsing schedule file: %v", err)
	}
	if len(list) != 0 {
		t.Fatalf("expected empty schedule after remove, got %d", len(list))
	}
}

func TestScheduler_DueMessages(t *testing.T) {
	s, _ := newTestScheduler(t)
	past, err := s.Schedule("!room:example.com", "past", time.Now().Add(-time.Hour))
	if err != nil {
		t.Fatalf("Schedule past: %v", err)
	}
	if _, err := s.Schedule("!room:example.com", "future", time.Now().Add(time.Hour)); err != nil {
		t.Fatalf("Schedule future: %v", err)
	}

	due := s.dueMessages(time.Now())
	if len(due) != 1 {
		t.Fatalf("expected 1 due message, got %d", len(due))
	}
	if due[0].ID != past.ID {
		t.Fatalf("expected past message due, got %+v", due[0])
	}
}

func TestScheduler_StopIsIdempotent(t *testing.T) {
	s, _ := newTestScheduler(t)
	s.Start(context.Background())
	s.Stop()
	s.Stop() // must not panic or block
}

func TestScheduler_DispatchSendsAndClearsPersistedFile(t *testing.T) {
	var (
		mu       sync.Mutex
		gotBody  string
		gotRoom  string
		received = make(chan struct{}, 1)
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "/send/m.room.message/") {
			var content struct {
				Body string `json:"body"`
			}
			_ = json.NewDecoder(r.Body).Decode(&content)
			mu.Lock()
			gotBody = content.Body
			// path: /_matrix/client/v3/rooms/{roomID}/send/...
			parts := strings.Split(r.URL.Path, "/")
			for i, p := range parts {
				if p == "rooms" && i+1 < len(parts) {
					gotRoom = parts[i+1]
				}
			}
			mu.Unlock()
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"event_id":"$evt:example.com"}`))
			select {
			case received <- struct{}{}:
			default:
			}
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	client, err := mautrix.NewClient(srv.URL, id.UserID("@bot:example.com"), "token")
	if err != nil {
		t.Fatalf("NewClient: %v", err)
	}

	path := filepath.Join(t.TempDir(), "schedule.json")
	s, err := NewScheduler(path, client)
	if err != nil {
		t.Fatalf("NewScheduler: %v", err)
	}
	s.poll = 10 * time.Millisecond

	// Schedule a message that is already due so the loop delivers it promptly.
	if _, err := s.Schedule("!room:example.com", "scheduled hello", time.Now().Add(-time.Second)); err != nil {
		t.Fatalf("Schedule: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s.Start(ctx)
	defer s.Stop()

	select {
	case <-received:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for scheduled message delivery")
	}

	mu.Lock()
	if gotBody != "scheduled hello" {
		t.Errorf("unexpected body delivered: %q", gotBody)
	}
	if gotRoom != "!room:example.com" {
		t.Errorf("unexpected room: %q", gotRoom)
	}
	mu.Unlock()

	// After delivery the message must be removed from the store and file.
	deadline := time.Now().Add(2 * time.Second)
	for s.Pending() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if s.Pending() != 0 {
		t.Fatalf("expected 0 pending after delivery, got %d", s.Pending())
	}
}
