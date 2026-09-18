package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"maunium.net/go/mautrix"
	"maunium.net/go/mautrix/event"
	"maunium.net/go/mautrix/id"
)

// ScheduledMessage represents a message queued to be sent at a future time.
type ScheduledMessage struct {
	ID     string    `json:"id"`
	RoomID string    `json:"room_id"`
	Body   string    `json:"message"`
	SendAt time.Time `json:"send_at"`
}

// Scheduler stores scheduled messages durably in a JSON file and dispatches
// them at their configured time. Pending messages are reloaded on startup so
// they are not lost if the process crashes.
type Scheduler struct {
	path   string
	client *mautrix.Client

	mu       sync.Mutex
	messages map[string]ScheduledMessage

	poll   time.Duration
	stopCh chan struct{}
	doneCh chan struct{}
}

// NewScheduler creates a scheduler backed by the JSON file at path. Any
// messages already present in the file are loaded so scheduled sends survive a
// process restart or crash.
func NewScheduler(path string, client *mautrix.Client) (*Scheduler, error) {
	s := &Scheduler{
		path:     path,
		client:   client,
		messages: make(map[string]ScheduledMessage),
		poll:     time.Second,
		stopCh:   make(chan struct{}),
		doneCh:   make(chan struct{}),
	}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load reads persisted scheduled messages from disk. A missing file is not an
// error (there is simply nothing scheduled yet).
func (s *Scheduler) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("reading schedule file: %w", err)
	}
	if len(data) == 0 {
		return nil
	}

	var list []ScheduledMessage
	if err := json.Unmarshal(data, &list); err != nil {
		return fmt.Errorf("parsing schedule file: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	for _, m := range list {
		if m.ID == "" {
			m.ID = uuid.NewString()
		}
		s.messages[m.ID] = m
	}
	return nil
}

// persist writes the current set of scheduled messages to disk atomically
// (write to a temp file, then rename) so a crash mid-write cannot corrupt the
// schedule file. Callers must hold s.mu.
func (s *Scheduler) persist() error {
	list := make([]ScheduledMessage, 0, len(s.messages))
	for _, m := range s.messages {
		list = append(list, m)
	}
	sort.Slice(list, func(i, j int) bool {
		return list[i].SendAt.Before(list[j].SendAt)
	})

	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding schedule: %w", err)
	}

	dir := filepath.Dir(s.path)
	tmp, err := os.CreateTemp(dir, ".schedule-*.tmp")
	if err != nil {
		return fmt.Errorf("creating temp schedule file: %w", err)
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op if rename succeeded

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("writing temp schedule file: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return fmt.Errorf("syncing temp schedule file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("closing temp schedule file: %w", err)
	}
	//nolint:gosec // s.path is operator-supplied configuration, not user input
	if err := os.Rename(tmpName, s.path); err != nil {
		return fmt.Errorf("replacing schedule file: %w", err)
	}
	return nil
}

// Schedule stores a message to be sent at sendAt and persists it immediately.
func (s *Scheduler) Schedule(roomID, body string, sendAt time.Time) (ScheduledMessage, error) {
	msg := ScheduledMessage{
		ID:     uuid.NewString(),
		RoomID: roomID,
		Body:   body,
		SendAt: sendAt.UTC(),
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages[msg.ID] = msg
	if err := s.persist(); err != nil {
		delete(s.messages, msg.ID)
		return ScheduledMessage{}, err
	}
	return msg, nil
}

// Pending returns the number of messages currently scheduled.
func (s *Scheduler) Pending() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.messages)
}

// dueMessages returns all messages whose send time is at or before now.
func (s *Scheduler) dueMessages(now time.Time) []ScheduledMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	var due []ScheduledMessage
	for _, m := range s.messages {
		if !m.SendAt.After(now) {
			due = append(due, m)
		}
	}
	return due
}

// remove deletes a delivered message from the store and persists the change.
func (s *Scheduler) remove(msgID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.messages[msgID]; !ok {
		return
	}
	delete(s.messages, msgID)
	if err := s.persist(); err != nil {
		log.Printf("failed to persist schedule after delivering %s: %v", msgID, err)
	}
}

// send delivers a single scheduled message via the Matrix client.
func (s *Scheduler) send(ctx context.Context, m ScheduledMessage) error {
	_, err := s.client.SendMessageEvent(ctx, id.RoomID(m.RoomID), event.EventMessage, &event.MessageEventContent{
		MsgType: event.MsgText,
		Body:    m.Body,
	})
	return err
}

// Start launches the background dispatch loop. Call Stop to shut it down.
func (s *Scheduler) Start(ctx context.Context) {
	go s.loop(ctx)
}

func (s *Scheduler) loop(ctx context.Context) {
	defer close(s.doneCh)
	ticker := time.NewTicker(s.poll)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopCh:
			return
		case <-ticker.C:
			s.dispatch(ctx)
		}
	}
}

// dispatch sends every message that is currently due. A message that fails to
// send is kept in the store so it can be retried on the next tick (and is not
// lost across restarts).
func (s *Scheduler) dispatch(ctx context.Context) {
	for _, m := range s.dueMessages(time.Now()) {
		if err := s.send(ctx, m); err != nil {
			log.Printf("failed to send scheduled message %s to %s: %v", m.ID, m.RoomID, err)
			continue
		}
		s.remove(m.ID)
	}
}

// Stop signals the dispatch loop to exit and waits for it to finish.
func (s *Scheduler) Stop() {
	select {
	case <-s.stopCh:
		// already stopped
	default:
		close(s.stopCh)
	}
	<-s.doneCh
}
