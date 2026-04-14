package engine

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DonScott603/gogoclaw/internal/memory"
	"github.com/DonScott603/gogoclaw/internal/provider"
)

// recordingSummarizer records calls and optionally returns a result.
type recordingSummarizer struct {
	mu        sync.Mutex
	calls     [][]provider.Message
	result    *memory.SummarizeResult
	delay     time.Duration
	callCount atomic.Int32
}

func (r *recordingSummarizer) MaybeSummarize(_ context.Context, history []provider.Message, _ string) (*memory.SummarizeResult, error) {
	r.callCount.Add(1)
	if r.delay > 0 {
		time.Sleep(r.delay)
	}
	r.mu.Lock()
	r.calls = append(r.calls, history)
	result := r.result
	r.mu.Unlock()
	return result, nil
}

func (r *recordingSummarizer) getCalls() [][]provider.Message {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.calls
}

func msg(role, content string) provider.Message {
	return provider.Message{Role: role, Content: content}
}

// --- Test 1 ---

func TestApplyPendingSummaryNoOp(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "hello"))
	session.AppendMessage(msg("assistant", "hi"))

	eng := newTestEngine(&mockProvider{name: "mock", response: "ok"}, "")
	eng.applyPendingSummary(session)

	h := session.GetHistory()
	if len(h) != 2 {
		t.Fatalf("history length = %d, want 2", len(h))
	}
}

// --- Test 2 ---

func TestApplyPendingSummaryReconciles(t *testing.T) {
	session := newTestSession("tui", "test")

	// Populate history with 6 messages (msg1-msg6).
	session.AppendMessage(msg("user", "msg1"))
	session.AppendMessage(msg("assistant", "msg2"))
	session.AppendMessage(msg("user", "msg3"))
	session.AppendMessage(msg("assistant", "msg4"))
	session.AppendMessage(msg("user", "msg5"))
	session.AppendMessage(msg("assistant", "msg6"))

	// Summarizer saw messages 1-4 (SnapshotLen=4).
	// RemainingHistory = [summary, msg3, msg4] (summary replaces msg1-msg2).
	pending := &PendingSummaryResult{
		SnapshotLen: 4,
		Result: &memory.SummarizeResult{
			RemainingHistory: []provider.Message{
				msg("system", "summary of msg1-msg2"),
				msg("user", "msg3"),
				msg("assistant", "msg4"),
			},
		},
	}

	// Append 2 more messages after the snapshot (msg7, msg8).
	session.AppendMessage(msg("user", "msg7"))
	session.AppendMessage(msg("assistant", "msg8"))

	session.PendingSummary <- pending

	eng := newTestEngine(&mockProvider{name: "mock", response: "ok"}, "")
	eng.applyPendingSummary(session)

	h := session.GetHistory()
	// Expected: [summary, msg3, msg4, msg5, msg6, msg7, msg8] = 7 messages
	if len(h) != 7 {
		t.Fatalf("history length = %d, want 7", len(h))
		return
	}

	expected := []string{"summary of msg1-msg2", "msg3", "msg4", "msg5", "msg6", "msg7", "msg8"}
	for i, want := range expected {
		if h[i].Content != want {
			t.Errorf("h[%d].Content = %q, want %q", i, h[i].Content, want)
		}
	}
}

// --- Test 3 ---

func TestApplyPendingSummaryNilChannel(t *testing.T) {
	session := &Session{
		ID:             "tui:test",
		ConversationID: "test",
		Channel:        "tui",
		// PendingSummary is nil — InitAsync NOT called.
	}
	session.AppendMessage(msg("user", "hello"))

	eng := newTestEngine(&mockProvider{name: "mock", response: "ok"}, "")
	// Should not panic.
	eng.applyPendingSummary(session)

	h := session.GetHistory()
	if len(h) != 1 {
		t.Fatalf("history length = %d, want 1", len(h))
	}
}

// --- Test 4 ---

func TestApplyPendingSummaryInvalidSnapshotLen(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "msg1"))
	session.AppendMessage(msg("assistant", "msg2"))
	session.AppendMessage(msg("user", "msg3"))
	session.AppendMessage(msg("assistant", "msg4"))

	pending := &PendingSummaryResult{
		SnapshotLen: 10, // greater than current history length
		Result: &memory.SummarizeResult{
			RemainingHistory: []provider.Message{msg("system", "summary")},
		},
	}
	session.PendingSummary <- pending

	eng := newTestEngine(&mockProvider{name: "mock", response: "ok"}, "")
	eng.applyPendingSummary(session)

	h := session.GetHistory()
	if len(h) != 4 {
		t.Fatalf("history length = %d, want 4 (invalid result should be discarded)", len(h))
	}
}

// --- Test 5 ---

func TestApplyPendingSummaryNilRemainingHistory(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "msg1"))
	session.AppendMessage(msg("assistant", "msg2"))
	session.AppendMessage(msg("user", "msg3"))
	session.AppendMessage(msg("assistant", "msg4"))

	pending := &PendingSummaryResult{
		SnapshotLen: 4,
		Result: &memory.SummarizeResult{
			RemainingHistory: nil,
		},
	}
	session.PendingSummary <- pending

	eng := newTestEngine(&mockProvider{name: "mock", response: "ok"}, "")
	eng.applyPendingSummary(session)

	h := session.GetHistory()
	if len(h) != 4 {
		t.Fatalf("history length = %d, want 4 (nil RemainingHistory should be discarded)", len(h))
	}
}

// --- Test 6 ---

func TestApplyPendingSummaryBeforeNewUserTurn(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "msg1"))
	session.AppendMessage(msg("assistant", "msg2"))
	session.AppendMessage(msg("user", "msg3"))
	session.AppendMessage(msg("assistant", "msg4"))

	// Pending summary compacts first pair, keeps second pair.
	pending := &PendingSummaryResult{
		SnapshotLen: 4,
		Result: &memory.SummarizeResult{
			RemainingHistory: []provider.Message{
				msg("system", "summary_of_msg1_msg2"),
				msg("user", "msg3"),
				msg("assistant", "msg4"),
			},
		},
	}
	session.PendingSummary <- pending

	mock := &mockProvider{name: "mock", response: "reply5"}
	eng := newTestEngine(mock, "")

	resp, err := eng.Send(context.Background(), session, "msg5")
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if resp != "reply5" {
		t.Fatalf("Send() = %q, want %q", resp, "reply5")
	}

	h := session.GetHistory()
	// Expected: [summary, msg3, msg4, msg5(user), reply5(assistant)]
	if len(h) != 5 {
		t.Fatalf("history length = %d, want 5", len(h))
	}
	if h[0].Content != "summary_of_msg1_msg2" {
		t.Errorf("h[0] = %q, want summary", h[0].Content)
	}
	if h[3].Content != "msg5" {
		t.Errorf("h[3] = %q, want msg5", h[3].Content)
	}
	if h[4].Content != "reply5" {
		t.Errorf("h[4] = %q, want reply5", h[4].Content)
	}
}

// --- Test 7 ---

func TestMaybeStartSummarizationSkipsWhenAlreadyRunning(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "hello"))
	session.Summarizing.Store(true)

	summarizer := &recordingSummarizer{}
	eng := New(Config{
		Provider:     &mockProvider{name: "mock", response: "ok"},
		MaxContext:   8192,
		Summarizer:   summarizer,
	})

	eng.maybeStartSummarization(context.Background(), session)

	// Brief wait to confirm no goroutine launched.
	time.Sleep(50 * time.Millisecond)

	if !session.Summarizing.Load() {
		t.Fatal("Summarizing should still be true")
	}
	if len(summarizer.getCalls()) != 0 {
		t.Fatal("summarizer should not have been called")
	}
}

// --- Test 8 ---

func TestMaybeStartSummarizationNoOpSummarizer(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "hello"))

	eng := New(Config{
		Provider:   &mockProvider{name: "mock", response: "ok"},
		MaxContext: 8192,
		// Summarizer defaults to NoOpSummarizer
	})

	eng.maybeStartSummarization(context.Background(), session)

	// Wait for goroutine to complete.
	time.Sleep(200 * time.Millisecond)

	if session.Summarizing.Load() {
		t.Fatal("Summarizing should be false after goroutine completes")
	}

	// Channel should be empty (MaybeSummarize returned nil).
	select {
	case <-session.PendingSummary:
		t.Fatal("channel should be empty — NoOpSummarizer returns nil")
	default:
	}
}

// --- Test 9 ---

func TestMaybeStartSummarizationNilSummarizer(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "hello"))

	eng := &Engine{
		provider:   &mockProvider{name: "mock", response: "ok"},
		summarizer: nil,
	}

	// Should not panic.
	eng.maybeStartSummarization(context.Background(), session)

	if session.Summarizing.Load() {
		t.Fatal("Summarizing should remain false")
	}
}

// --- Test 10 ---

func TestPendingSummaryNewestWins(t *testing.T) {
	session := newTestSession("tui", "test")
	session.AppendMessage(msg("user", "msg1"))
	session.AppendMessage(msg("assistant", "msg2"))

	// Seed a stale result.
	stale := &PendingSummaryResult{
		SnapshotLen: 2,
		Result: &memory.SummarizeResult{
			Summary:          "old summary",
			RemainingHistory: []provider.Message{msg("system", "old summary")},
		},
	}
	session.PendingSummary <- stale

	// Mock summarizer returns a newer result immediately.
	summarizer := &recordingSummarizer{
		result: &memory.SummarizeResult{
			Summary: "new summary",
			RemainingHistory: []provider.Message{
				msg("system", "new summary"),
				msg("user", "msg1"),
				msg("assistant", "msg2"),
			},
			FactsExtracted: []string{"fact1"},
		},
	}
	eng := New(Config{
		Provider:   &mockProvider{name: "mock", response: "ok"},
		MaxContext: 8192,
		Summarizer: summarizer,
	})

	eng.maybeStartSummarization(context.Background(), session)

	// Wait for goroutine to complete.
	time.Sleep(200 * time.Millisecond)

	pending := <-session.PendingSummary
	if pending.Result.Summary != "new summary" {
		t.Fatalf("expected newest result, got %q", pending.Result.Summary)
	}
}

// --- Test 11 ---

func TestSendUsesAsyncFlow(t *testing.T) {
	summarizer := &recordingSummarizer{
		result: &memory.SummarizeResult{
			Summary: "turn1 summary",
			RemainingHistory: []provider.Message{
				msg("system", "turn1 summary"),
			},
			FactsExtracted: []string{"fact1"},
		},
	}
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:   mock,
		MaxContext: 8192,
		Summarizer: summarizer,
	})
	session := newTestSession("tui", "test")

	// First Send — summarizer should be called AFTER the response (background).
	resp, err := eng.Send(context.Background(), session, "hello")
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}
	if resp != "reply" {
		t.Fatalf("Send() = %q, want %q", resp, "reply")
	}

	// Wait for background goroutine.
	time.Sleep(200 * time.Millisecond)

	calls := summarizer.getCalls()
	if len(calls) != 1 {
		t.Fatalf("summarizer called %d times, want 1", len(calls))
	}
	// The summarizer should have received the full history (user + assistant).
	if len(calls[0]) != 2 {
		t.Fatalf("summarizer received %d messages, want 2", len(calls[0]))
	}

	// Second Send — pending summary should be applied before the new user message.
	resp, err = eng.Send(context.Background(), session, "followup")
	if err != nil {
		t.Fatalf("Send() error: %v", err)
	}

	h := session.GetHistory()
	// After applying summary: [turn1 summary] + new turn: [followup, reply]
	if len(h) < 3 {
		t.Fatalf("history length = %d, want at least 3", len(h))
	}
	if h[0].Content != "turn1 summary" {
		t.Errorf("h[0] = %q, want 'turn1 summary'", h[0].Content)
	}
}

// --- Test 12 ---

func TestRapidFireMessagesPreserved(t *testing.T) {
	summarizer := &recordingSummarizer{
		delay: 100 * time.Millisecond,
		result: &memory.SummarizeResult{
			Summary: "compact",
			RemainingHistory: []provider.Message{
				msg("system", "compact"),
			},
			FactsExtracted: []string{},
		},
	}
	mock := &mockProvider{name: "mock", response: "reply"}
	eng := New(Config{
		Provider:   mock,
		MaxContext: 8192,
		Summarizer: summarizer,
	})
	session := newTestSession("tui", "test")

	// Send 3 messages rapidly.
	for _, m := range []string{"msg1", "msg2", "msg3"} {
		resp, err := eng.Send(context.Background(), session, m)
		if err != nil {
			t.Fatalf("Send(%q) error: %v", m, err)
		}
		if resp != "reply" {
			t.Fatalf("Send(%q) = %q, want 'reply'", m, resp)
		}
	}

	// After all 3 complete, verify all user and assistant messages are present.
	h := session.GetHistory()
	userCount := 0
	assistantCount := 0
	for _, m := range h {
		switch m.Role {
		case "user":
			userCount++
		case "assistant":
			assistantCount++
		}
	}

	// We may have a summary applied, but all 3 user+assistant pairs should
	// still be present (some may be compacted by earlier summaries, but the
	// rapid-fire messages added after the snapshot must be preserved).
	// At minimum, we should have the 3 user messages and 3 assistant replies
	// minus whatever was summarized. But message 2 and 3 should always survive
	// because they were added after any snapshot.
	if userCount < 2 {
		t.Errorf("expected at least 2 user messages, got %d", userCount)
	}
	if assistantCount < 2 {
		t.Errorf("expected at least 2 assistant messages, got %d", assistantCount)
	}
}

// --- Test 13 ---

func TestSendStreamAsyncFlow(t *testing.T) {
	summarizer := &recordingSummarizer{
		result: &memory.SummarizeResult{
			Summary: "stream summary",
			RemainingHistory: []provider.Message{
				msg("system", "stream summary"),
			},
			FactsExtracted: []string{"fact1"},
		},
	}
	mock := &mockProvider{name: "mock", response: "streamed"}
	eng := New(Config{
		Provider:   mock,
		MaxContext: 8192,
		Summarizer: summarizer,
	})
	session := newTestSession("tui", "test")

	// First SendStream.
	ch, err := eng.SendStream(context.Background(), session, "hello")
	if err != nil {
		t.Fatalf("SendStream() error: %v", err)
	}
	for range ch {
		// drain
	}

	// Wait for background goroutine.
	time.Sleep(200 * time.Millisecond)

	if summarizer.callCount.Load() < 1 {
		t.Fatal("summarizer should have been called after SendStream completes")
	}

	// Seed a pending result for the second call.
	// The goroutine should have already put one there; verify.
	select {
	case p := <-session.PendingSummary:
		// Re-send it so the next SendStream can apply it.
		session.PendingSummary <- p
	default:
		t.Fatal("expected pending summary from first SendStream")
	}

	// Second SendStream — should apply the pending summary.
	ch2, err := eng.SendStream(context.Background(), session, "followup")
	if err != nil {
		t.Fatalf("second SendStream() error: %v", err)
	}
	for range ch2 {
		// drain
	}

	h := session.GetHistory()
	if len(h) < 3 {
		t.Fatalf("history length = %d, want at least 3", len(h))
	}
	if h[0].Content != "stream summary" {
		t.Errorf("h[0] = %q, want 'stream summary'", h[0].Content)
	}
}

// --- WaitForSummarization tests ---

func TestWaitForSummarizationNotRunning(t *testing.T) {
	session := newTestSession("tui", "test")
	// Summarizing is false by default.
	result := session.WaitForSummarization(100 * time.Millisecond)
	if !result {
		t.Fatal("expected true when not summarizing")
	}
}

func TestWaitForSummarizationCompletesInTime(t *testing.T) {
	session := newTestSession("tui", "test")
	session.Summarizing.Store(true)

	go func() {
		time.Sleep(50 * time.Millisecond)
		session.Summarizing.Store(false)
	}()

	result := session.WaitForSummarization(500 * time.Millisecond)
	if !result {
		t.Fatal("expected true when summarization completes in time")
	}
}

func TestWaitForSummarizationTimesOut(t *testing.T) {
	session := newTestSession("tui", "test")
	session.Summarizing.Store(true)
	// Don't clear the flag.

	result := session.WaitForSummarization(50 * time.Millisecond)
	if result {
		t.Fatal("expected false on timeout")
	}
}

func TestWaitForSummarizationDoesNotConsumePending(t *testing.T) {
	session := newTestSession("tui", "test")
	session.Summarizing.Store(true)

	// Put a pending result on the channel.
	pending := &PendingSummaryResult{
		SnapshotLen: 2,
		Result: &memory.SummarizeResult{
			Summary:          "test summary",
			RemainingHistory: []provider.Message{msg("system", "test summary")},
		},
	}
	session.PendingSummary <- pending

	go func() {
		time.Sleep(30 * time.Millisecond)
		session.Summarizing.Store(false)
	}()

	result := session.WaitForSummarization(500 * time.Millisecond)
	if !result {
		t.Fatal("expected true")
	}

	// Verify the pending result is still on the channel.
	select {
	case got := <-session.PendingSummary:
		if got.Result.Summary != "test summary" {
			t.Errorf("expected same pending result, got summary=%q", got.Result.Summary)
		}
	default:
		t.Fatal("pending result was consumed — WaitForSummarization should not drain channel")
	}
}
