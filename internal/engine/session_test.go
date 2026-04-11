package engine

import (
	"sync"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/provider"
)

func TestSessionAppendMessage(t *testing.T) {
	s := &Session{ID: "tui:test", ConversationID: "test", Channel: "tui"}

	s.AppendMessage(provider.Message{Role: "user", Content: "hello"})
	s.AppendMessage(provider.Message{Role: "assistant", Content: "hi"})

	h := s.GetHistory()
	if len(h) != 2 {
		t.Fatalf("history length = %d, want 2", len(h))
	}
	if h[0].Role != "user" || h[0].Content != "hello" {
		t.Errorf("h[0] = %+v, want user/hello", h[0])
	}
	if h[1].Role != "assistant" || h[1].Content != "hi" {
		t.Errorf("h[1] = %+v, want assistant/hi", h[1])
	}
}

func TestSessionConcurrentAccess(t *testing.T) {
	s := &Session{ID: "tui:concurrent", ConversationID: "concurrent", Channel: "tui"}

	const goroutines = 100
	var wg sync.WaitGroup
	wg.Add(goroutines)

	for i := 0; i < goroutines; i++ {
		go func(n int) {
			defer wg.Done()
			s.AppendMessage(provider.Message{Role: "user", Content: "msg"})
		}(i)
	}

	wg.Wait()

	h := s.GetHistory()
	if len(h) != goroutines {
		t.Errorf("history length = %d, want %d", len(h), goroutines)
	}
}

func TestSessionTouchActivity(t *testing.T) {
	s := &Session{ID: "tui:activity", ConversationID: "activity", Channel: "tui"}

	before := time.Now().Add(-time.Millisecond)
	s.TouchActivity()
	after := time.Now().Add(time.Millisecond)

	if s.LastActivityAt.Before(before) || s.LastActivityAt.After(after) {
		t.Errorf("LastActivityAt = %v, want between %v and %v", s.LastActivityAt, before, after)
	}
}

func TestSessionSetHistory(t *testing.T) {
	s := &Session{ID: "tui:set", ConversationID: "set", Channel: "tui"}
	s.AppendMessage(provider.Message{Role: "user", Content: "old"})

	newHistory := []provider.Message{
		{Role: "user", Content: "new1"},
		{Role: "assistant", Content: "new2"},
	}
	s.SetHistory(newHistory)

	h := s.GetHistory()
	if len(h) != 2 {
		t.Fatalf("history length = %d, want 2", len(h))
	}
	if h[0].Content != "new1" {
		t.Errorf("h[0].Content = %q, want %q", h[0].Content, "new1")
	}
}

func TestSessionClearHistory(t *testing.T) {
	s := &Session{ID: "tui:clear", ConversationID: "clear", Channel: "tui"}
	s.AppendMessage(provider.Message{Role: "user", Content: "msg"})
	s.ClearHistory()

	h := s.GetHistory()
	if len(h) != 0 {
		t.Errorf("history length = %d, want 0", len(h))
	}
}

func TestSessionGetHistoryReturnsCopy(t *testing.T) {
	s := &Session{ID: "tui:copy", ConversationID: "copy", Channel: "tui"}
	s.AppendMessage(provider.Message{Role: "user", Content: "original"})

	h := s.GetHistory()
	h[0].Content = "mutated"

	h2 := s.GetHistory()
	if h2[0].Content != "original" {
		t.Errorf("GetHistory did not return a copy; content = %q, want %q", h2[0].Content, "original")
	}
}
