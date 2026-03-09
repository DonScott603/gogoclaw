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
// An optional health.Monitor can be passed to enable the health dashboard (Ctrl+H).
func New(eng *engine.Engine, opts ...Option) *tea.Program {
	m := initialModel(eng)
	for _, opt := range opts {
		opt(&m)
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	return p
}

// Option configures the TUI model.
type Option func(*model)

// WithHealthMonitor attaches a health monitor to the TUI for the Ctrl+H dashboard.
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

// NewWithConfirmGate creates a TUI and returns a shell confirmation function
// that can be passed to the tool dispatcher.
func NewWithConfirmGate(eng *engine.Engine) (*tea.Program, func(command string) bool) {
	m := initialModel(eng)
	p := tea.NewProgram(m, tea.WithAltScreen())

	confirmFn := func(command string) bool {
		ch := make(chan bool, 1)
		p.Send(confirmShellMsg{command: command, resultCh: ch})
		return <-ch
	}

	return p, confirmFn
}

func initialModel(eng *engine.Engine) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (Ctrl+S send, Ctrl+N new, Ctrl+L list, Ctrl+H health, Esc quit)"
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	vp := viewport.New(80, 20)
	vp.SetContent("Welcome to GoGoClaw. Type a message and press Ctrl+S to send.\n" +
		"Ctrl+N: new conversation | Ctrl+L: toggle conversation list | Ctrl+H: health dashboard\n")

	return model{
		engine:   eng,
		viewport: vp,
		textarea: ta,
		conversations: []conversationEntry{
			{id: "default", title: "New Conversation"},
		},
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Handle confirmation dialog first.
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
			// New conversation.
			m.messages = nil
			m.engine.ClearHistory()
			m.streamBuf = ""
			m.streaming = false
			id := fmt.Sprintf("conv-%d", len(m.conversations))
			m.conversations = append(m.conversations, conversationEntry{id: id, title: "New Conversation"})
			m.activeConvoIdx = len(m.conversations) - 1
			m.viewport.SetContent(m.renderMessages())
			return m, nil

		case tea.KeyCtrlL:
			// Toggle conversation list panel.
			if m.activePanel == panelChat {
				m.activePanel = panelConversations
			} else {
				m.activePanel = panelChat
			}
			m.viewport.SetContent(m.renderMessages())
			return m, nil

		case tea.KeyCtrlH:
			// Toggle health dashboard panel.
			if m.activePanel == panelHealth {
				m.activePanel = panelChat
			} else {
				m.activePanel = panelHealth
			}
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		headerHeight := 1
		inputHeight := 5
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - headerHeight - inputHeight
		m.textarea.SetWidth(msg.Width)
		m.viewport.SetContent(m.renderMessages())

	case streamChunkMsg:
		chunk := msg.chunk
		if chunk.Error != nil {
			m.err = chunk.Error
			m.streaming = false
			return m, nil
		}
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
		m.streamBuf += chunk.Content
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, waitForChunk(msg.ch)

	case toolCallMsg:
		m.toolActivity = fmt.Sprintf("Calling %s...", msg.name)
		m.messages = append(m.messages, chatMessage{
			role:    "tool",
			content: fmt.Sprintf("[tool: %s] %s", msg.name, truncate(msg.args, 200)),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case toolResultMsg:
		m.toolActivity = ""
		display := truncate(msg.result, 500)
		if msg.isError {
			display = "ERROR: " + display
		}
		m.messages = append(m.messages, chatMessage{
			role:    "tool",
			content: fmt.Sprintf("[result: %s] %s", msg.name, display),
		})
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, nil

	case confirmShellMsg:
		m.showConfirm = true
		m.confirmCmd = msg.command
		m.confirmCh = msg.resultCh
		m.viewport.SetContent(m.renderMessages())
		return m, nil

	case errMsg:
		m.err = msg.err
		m.streaming = false
		return m, nil
	}

	var cmd tea.Cmd
	if !m.streaming && !m.showConfirm {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) handleConfirmKey(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {
	case "y", "Y":
		m.showConfirm = false
		if m.confirmCh != nil {
			m.confirmCh <- true
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: fmt.Sprintf("[shell approved] %s", m.confirmCmd)})
		m.viewport.SetContent(m.renderMessages())
		return m, nil
	case "n", "N", "escape":
		m.showConfirm = false
		if m.confirmCh != nil {
			m.confirmCh <- false
		}
		m.messages = append(m.messages, chatMessage{role: "system", content: "[shell denied]"})
		m.viewport.SetContent(m.renderMessages())
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

	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userStyle.Render("You") + "\n")
			b.WriteString(msg.content)
		case "assistant":
			b.WriteString(assistantStyle.Render("Assistant") + "\n")
			b.WriteString(msg.content)
		case "tool":
			b.WriteString(toolStyle.Render(msg.content))
		case "system":
			b.WriteString(systemStyle.Render(msg.content))
		}
		b.WriteString("\n\n")
	}

	if m.streaming && m.streamBuf != "" {
		b.WriteString(assistantStyle.Render("Assistant") + "\n")
		b.WriteString(m.streamBuf)
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

func (m model) startStream(text string) tea.Cmd {
	return func() tea.Msg {
		ch, err := m.engine.SendStream(context.Background(), text)
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
