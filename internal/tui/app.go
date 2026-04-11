// Package tui implements the bubbletea terminal interface with panels
// for chat, conversations, health, settings, and workspace browsing.
package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/health"
	"github.com/DonScott603/gogoclaw/internal/provider"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chatMessage is a single message displayed in the chat panel.
type chatMessage struct {
	role    string // "user", "assistant", "tool", "system"
	content string
}

// conversationEntry is a conversation in the sidebar list.
type conversationEntry struct {
	id    string
	title string
}

// streamChunkMsg wraps a provider.StreamChunk for the bubbletea update loop.
type streamChunkMsg struct {
	chunk provider.StreamChunk
	ch    <-chan provider.StreamChunk
}

// toolCallMsg notifies the TUI of a tool being invoked.
type toolCallMsg struct {
	name string
	args string
}

// toolResultMsg notifies the TUI of a tool result.
type toolResultMsg struct {
	name    string
	callID  string
	result  string
	isError bool
}

// confirmShellMsg asks the user to confirm a shell command.
type confirmShellMsg struct {
	command  string
	resultCh chan bool
}

// piiWarnMsg notifies the TUI of a PII warning in warn mode.
type piiWarnMsg struct {
	patterns []string
}

// errMsg wraps an error for the bubbletea update loop.
type errMsg struct{ err error }

// panel tracks which panel is focused.
type panel int

const (
	panelChat panel = iota
	panelConversations
	panelHealth
)

// model is the bubbletea model for the GoGoClaw TUI.
type model struct {
	engine         *engine.Engine
	sessionManager *engine.SessionManager
	currentSession *engine.Session
	viewport       viewport.Model
	textarea       textarea.Model
	messages       []chatMessage
	conversations  []conversationEntry
	activeConvoIdx int
	streaming      bool
	streamBuf      string
	width          int
	height         int
	err            error
	activePanel    panel
	showConfirm    bool
	confirmCmd     string
	confirmCh      chan bool
	toolActivity   string // current tool being executed
	healthMonitor  *health.Monitor
}

// New creates a new bubbletea program for the TUI.
// An optional health.Monitor can be passed to enable the health dashboard (F2).
func New(eng *engine.Engine, sm *engine.SessionManager, opts ...Option) *tea.Program {
	m := initialModel(eng, sm)
	for _, opt := range opts {
		opt(&m)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	return p
}

// Option configures the TUI model.
type Option func(*model)

// WithHealthMonitor attaches a health monitor to the TUI for the F2 dashboard.
func WithHealthMonitor(mon *health.Monitor) Option {
	return func(m *model) {
		m.healthMonitor = mon
	}
}

// ConfirmGate holds a settable reference to a tea.Program so the confirm
// function always sends to the currently-running program instance.
type ConfirmGate struct {
	program *tea.Program
}

// NewConfirmGate returns a ConfirmGate and a ConfirmFunc that can be passed
// to the tool dispatcher. Call SetProgram before the program starts running.
func NewConfirmGate() (*ConfirmGate, func(command string) bool) {
	gate := &ConfirmGate{}
	fn := func(command string) bool {
		if gate.program == nil {
			return false
		}
		ch := make(chan bool, 1)
		gate.program.Send(confirmShellMsg{command: command, resultCh: ch})
		return <-ch
	}
	return gate, fn
}

// SetProgram sets the tea.Program that will receive confirm messages.
func (g *ConfirmGate) SetProgram(p *tea.Program) {
	g.program = p
}

func initialModel(eng *engine.Engine, sm *engine.SessionManager) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Ctrl+S send, Ctrl+N new, Ctrl+L list, F2 health, Esc quit)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	vp := viewport.New(80, 20)
	vp.SetContent("Welcome to GoGoClaw. Type a message and press Ctrl+S to send.\n" +
		"Ctrl+N: new conversation | Ctrl+L: toggle conversation list | F2: health dashboard\n")

	defaultConvID := "default"
	session := sm.GetOrCreate("tui", defaultConvID)

	return model{
		engine:         eng,
		sessionManager: sm,
		currentSession: session,
		viewport:       vp,
		textarea:       ta,
		conversations: []conversationEntry{
			{id: defaultConvID, title: "New Conversation"},
		},
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

// appendAndScroll appends a chat message, re-renders the viewport, and scrolls
// to the bottom. This is the common pattern for adding visible messages.
func (m *model) appendAndScroll(msg chatMessage) {
	m.messages = append(m.messages, msg)
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
}

// Update delegates to per-message-type handlers.
func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		return m.handleKeyMsg(msg)
	case tea.WindowSizeMsg:
		return m.handleWindowResize(msg)
	case streamChunkMsg:
		return m.handleStreamChunk(msg)
	case toolCallMsg:
		return m.handleToolCall(msg)
	case toolResultMsg:
		return m.handleToolResult(msg)
	case piiWarnMsg:
		return m.handlePIIWarn(msg)
	case confirmShellMsg:
		return m.handleConfirmShell(msg)
	case errMsg:
		m.err = msg.err
		m.streaming = false
		return m, nil
	}

	// Default: forward to textarea and viewport.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	if !m.streaming && !m.showConfirm {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.showConfirm {
		return m.handleConfirmKey(msg)
	}

	switch msg.Type {
	case tea.KeyCtrlC, tea.KeyEsc:
		return m, tea.Quit

	case tea.KeyCtrlS:
		if m.streaming {
			return m, nil
		}
		text := strings.TrimSpace(m.textarea.Value())
		if text == "" {
			return m, nil
		}
		m.textarea.Reset()
		m.messages = append(m.messages, chatMessage{role: "user", content: text})
		m.streaming = true
		m.streamBuf = ""
		m.err = nil
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, m.startStream(text)

	case tea.KeyCtrlN:
		m.messages = nil
		m.streamBuf = ""
		m.streaming = false
		id := fmt.Sprintf("conv-%d", len(m.conversations))
		m.conversations = append(m.conversations, conversationEntry{id: id, title: "New Conversation"})
		m.activeConvoIdx = len(m.conversations) - 1
		m.currentSession = m.sessionManager.GetOrCreate("tui", id)
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case tea.KeyCtrlL:
		if m.activePanel == panelChat {
			m.activePanel = panelConversations
		} else {
			if m.activeConvoIdx < len(m.conversations) {
				selectedID := m.conversations[m.activeConvoIdx].id
				m.currentSession = m.sessionManager.GetOrCreate("tui", selectedID)
				// Rebuild display messages from session history.
				h := m.currentSession.GetHistory()
				m.messages = nil
				for _, msg := range h {
					if msg.Role == "system" {
						continue
					}
					m.messages = append(m.messages, chatMessage{role: msg.Role, content: msg.Content})
				}
			}
			m.activePanel = panelChat
		}
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case tea.KeyF2:
		if m.activePanel == panelHealth {
			m.activePanel = panelChat
		} else {
			m.activePanel = panelHealth
		}
		return m, nil
	}

	// Default: forward to textarea and viewport.
	var cmds []tea.Cmd
	var cmd tea.Cmd
	if !m.streaming {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)
	return m, tea.Batch(cmds...)
}

func (m model) handleWindowResize(msg tea.WindowSizeMsg) (tea.Model, tea.Cmd) {
	m.width = msg.Width
	m.height = msg.Height
	headerHeight := 1
	inputHeight := 5
	m.viewport.Width = msg.Width
	m.viewport.Height = msg.Height - headerHeight - inputHeight
	m.textarea.SetWidth(msg.Width)
	m.viewport.SetContent(m.renderMessages())
	return m, nil
}

func (m model) handleStreamChunk(msg streamChunkMsg) (tea.Model, tea.Cmd) {
	chunk := msg.chunk
	if chunk.Error != nil {
		m.err = chunk.Error
		m.streaming = false
		return m, nil
	}
	// Process content before checking Done so that chunks with both
	// Content and Done=true (e.g. PII gate block messages) are captured.
	m.streamBuf += chunk.Content
	if chunk.Done {
		if m.streamBuf != "" {
			m.messages = append(m.messages, chatMessage{role: "assistant", content: m.streamBuf})
		}
		m.streamBuf = ""
		m.streaming = false
		m.toolActivity = ""
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil
	}
	m.viewport.SetContent(m.renderMessages())
	m.viewport.GotoBottom()
	return m, waitForChunk(msg.ch)
}

func (m model) handleToolCall(msg toolCallMsg) (tea.Model, tea.Cmd) {
	m.toolActivity = fmt.Sprintf("Calling %s...", msg.name)
	m.appendAndScroll(chatMessage{
		role:    "tool",
		content: fmt.Sprintf("[tool: %s] %s", msg.name, truncate(msg.args, 200)),
	})
	return m, nil
}

func (m model) handleToolResult(msg toolResultMsg) (tea.Model, tea.Cmd) {
	m.toolActivity = ""
	display := truncate(msg.result, 500)
	if msg.isError {
		display = "ERROR: " + display
	}
	m.appendAndScroll(chatMessage{
		role:    "tool",
		content: fmt.Sprintf("[result: %s] %s", msg.name, display),
	})
	return m, nil
}

func (m model) handlePIIWarn(msg piiWarnMsg) (tea.Model, tea.Cmd) {
	m.appendAndScroll(chatMessage{
		role:    "system",
		content: fmt.Sprintf("[PII WARNING] Detected sensitive data (%s) — proceeding in warn mode.", strings.Join(msg.patterns, ", ")),
	})
	return m, nil
}

func (m model) handleConfirmShell(msg confirmShellMsg) (tea.Model, tea.Cmd) {
	m.showConfirm = true
	m.confirmCmd = msg.command
	m.confirmCh = msg.resultCh
	m.viewport.SetContent(m.renderMessages())
	return m, nil
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.showConfirm = false
		if m.confirmCh != nil {
			m.confirmCh <- true
		}
		m.appendAndScroll(chatMessage{role: "system", content: fmt.Sprintf("[shell approved] %s", m.confirmCmd)})
		return m, nil
	case "n", "N", "escape":
		m.showConfirm = false
		if m.confirmCh != nil {
			m.confirmCh <- false
		}
		m.appendAndScroll(chatMessage{role: "system", content: "[shell denied]"})
		return m, nil
	}
	return m, nil
}

func (m model) View() string {
	statusBar := m.renderStatusBar()

	var mainView string
	switch m.activePanel {
	case panelConversations:
		mainView = m.renderConversationList()
	case panelHealth:
		mainView = m.renderHealthPanel()
	default:
		mainView = m.viewport.View()
	}

	inputView := m.textarea.View()

	if m.showConfirm {
		return fmt.Sprintf("%s\n%s\n\n%s\n%s",
			statusBar,
			mainView,
			m.renderConfirmDialog(),
			inputView,
		)
	}

	return fmt.Sprintf("%s\n%s\n%s", statusBar, mainView, inputView)
}

func (m model) renderStatusBar() string {
	providerName := m.engine.ProviderName()
	status := "ready"
	if m.streaming {
		status = "streaming..."
	}
	if m.toolActivity != "" {
		status = m.toolActivity
	}
	if m.err != nil {
		status = fmt.Sprintf("error: %v", m.err)
	}

	convoInfo := ""
	if m.activeConvoIdx < len(m.conversations) {
		convoInfo = fmt.Sprintf(" | Conv: %s", m.conversations[m.activeConvoIdx].title)
	}

	style := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Foreground(lipgloss.Color("230")).
		Padding(0, 1).
		Width(m.width)

	return style.Render(fmt.Sprintf("GoGoClaw | Provider: %s%s | %s", providerName, convoInfo, status))
}

func (m model) renderMessages() string {
	var b strings.Builder
	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)
	toolStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")).Italic(true)
	systemStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("3")).Italic(true)
	wrapStyle := lipgloss.NewStyle().Width(m.width)

	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userStyle.Render("You") + "\n")
			b.WriteString(wrapStyle.Render(msg.content))
		case "assistant":
			b.WriteString(assistantStyle.Render("Assistant") + "\n")
			b.WriteString(wrapStyle.Render(msg.content))
		case "tool":
			b.WriteString(toolStyle.Render(wrapStyle.Render(msg.content)))
		case "system":
			b.WriteString(systemStyle.Render(wrapStyle.Render(msg.content)))
		}
		b.WriteString("\n\n")
	}

	if m.streaming && m.streamBuf != "" {
		b.WriteString(assistantStyle.Render("Assistant") + "\n")
		b.WriteString(wrapStyle.Render(m.streamBuf))
		b.WriteString("▌\n")
	}

	return b.String()
}

func (m model) renderConversationList() string {
	var b strings.Builder
	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("12"))
	activeStyle := lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("10"))

	b.WriteString(titleStyle.Render("Conversations") + "\n")
	b.WriteString(strings.Repeat("-", 40) + "\n")

	for i, c := range m.conversations {
		prefix := "  "
		style := lipgloss.NewStyle()
		if i == m.activeConvoIdx {
			prefix = "> "
			style = activeStyle
		}
		b.WriteString(style.Render(fmt.Sprintf("%s%s", prefix, c.title)) + "\n")
	}
	b.WriteString("\nPress Ctrl+L to return to chat.\n")

	return b.String()
}

func (m model) renderConfirmDialog() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("11")).
		Padding(1, 2).
		Width(m.width - 4)

	return style.Render(fmt.Sprintf(
		"Shell command requires confirmation:\n\n  %s\n\nAllow execution? (y/n)",
		m.confirmCmd,
	))
}

// ToolCallObserver returns callback functions for the tool dispatcher
// that send messages to the TUI program.
func ToolCallObserver(p *tea.Program) (
	onCall func(name string, args json.RawMessage),
	onResult func(name string, callID string, result string, isErr bool),
) {
	onCall = func(name string, args json.RawMessage) {
		p.Send(toolCallMsg{name: name, args: string(args)})
	}
	onResult = func(name string, callID string, result string, isErr bool) {
		p.Send(toolResultMsg{name: name, callID: callID, result: result, isError: isErr})
	}
	return
}

// PIIWarnFunc returns a callback that sends a visible PII warning to the TUI.
// The returned function matches the signature expected by the PII gate's WarnFn
// when wrapped with a type conversion in main.go.
func PIIWarnFunc(p *tea.Program) func(patterns []string) {
	return func(patterns []string) {
		p.Send(piiWarnMsg{patterns: patterns})
	}
}

func (m model) startStream(text string) tea.Cmd {
	return func() tea.Msg {
		ch, err := m.engine.SendStream(context.Background(), m.currentSession, text)
		if err != nil {
			return errMsg{err: err}
		}
		return waitForChunkSync(ch)
	}
}

func waitForChunk(ch <-chan provider.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		return waitForChunkSync(ch)
	}
}

func waitForChunkSync(ch <-chan provider.StreamChunk) tea.Msg {
	chunk, ok := <-ch
	if !ok {
		return streamChunkMsg{
			chunk: provider.StreamChunk{Done: true},
			ch:    ch,
		}
	}
	return streamChunkMsg{chunk: chunk, ch: ch}
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}
