// Package tui implements the bubbletea terminal interface with panels
// for chat, conversations, health, settings, and workspace browsing.
package tui

import (
	"context"
	"fmt"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/provider"

	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// chatMessage is a single message displayed in the chat panel.
type chatMessage struct {
	role    string
	content string
}

// streamChunkMsg wraps a provider.StreamChunk for the bubbletea update loop.
type streamChunkMsg struct {
	chunk provider.StreamChunk
	ch    <-chan provider.StreamChunk
}

// errMsg wraps an error for the bubbletea update loop.
type errMsg struct{ err error }

// model is the bubbletea model for the GoGoClaw TUI.
type model struct {
	engine    *engine.Engine
	viewport  viewport.Model
	textarea  textarea.Model
	messages  []chatMessage
	streaming bool
	streamBuf string
	width     int
	height    int
	err       error
}

// New creates a new bubbletea program for the TUI.
func New(eng *engine.Engine) *tea.Program {
	return tea.NewProgram(initialModel(eng), tea.WithAltScreen())
}

func initialModel(eng *engine.Engine) model {
	ta := textarea.New()
	ta.Placeholder = "Type a message..."
	ta.Focus()
	ta.CharLimit = 4096
	ta.SetWidth(80)
	ta.SetHeight(3)
	ta.ShowLineNumbers = false

	vp := viewport.New(80, 20)
	vp.SetContent("Welcome to GoGoClaw. Type a message and press Ctrl+S to send.\n")

	return model{
		engine:   eng,
		viewport: vp,
		textarea: ta,
	}
}

func (m model) Init() tea.Cmd {
	return textarea.Blink
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
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
			m.viewport.SetContent(m.renderMessages())
			m.viewport.GotoBottom()
			return m, nil
		}
		m.streamBuf += chunk.Content
		m.viewport.SetContent(m.renderMessages())
		m.viewport.GotoBottom()
		return m, waitForChunk(msg.ch)

	case errMsg:
		m.err = msg.err
		m.streaming = false
		return m, nil
	}

	var cmd tea.Cmd
	if !m.streaming {
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

func (m model) View() string {
	statusBar := m.renderStatusBar()
	chatView := m.viewport.View()
	inputView := m.textarea.View()

	return fmt.Sprintf("%s\n%s\n%s", statusBar, chatView, inputView)
}

func (m model) renderStatusBar() string {
	providerName := m.engine.ProviderName()
	status := "ready"
	if m.streaming {
		status = "streaming..."
	}
	if m.err != nil {
		status = fmt.Sprintf("error: %v", m.err)
	}

	style := lipgloss.NewStyle().
		Background(lipgloss.Color("62")).
		Foreground(lipgloss.Color("230")).
		Padding(0, 1).
		Width(m.width)

	return style.Render(fmt.Sprintf("GoGoClaw | Provider: %s | %s", providerName, status))
}

func (m model) renderMessages() string {
	var b strings.Builder
	userStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	assistantStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true)

	for _, msg := range m.messages {
		switch msg.role {
		case "user":
			b.WriteString(userStyle.Render("You") + "\n")
		case "assistant":
			b.WriteString(assistantStyle.Render("Assistant") + "\n")
		}
		b.WriteString(msg.content)
		b.WriteString("\n\n")
	}

	if m.streaming && m.streamBuf != "" {
		b.WriteString(assistantStyle.Render("Assistant") + "\n")
		b.WriteString(m.streamBuf)
		b.WriteString("▌\n")
	}

	return b.String()
}

// startStream initiates streaming from the engine and returns the first chunk.
func (m model) startStream(text string) tea.Cmd {
	return func() tea.Msg {
		ch, err := m.engine.SendStream(context.Background(), text)
		if err != nil {
			return errMsg{err: err}
		}
		return waitForChunkSync(ch)
	}
}

// waitForChunk returns a command that reads the next chunk from the channel.
func waitForChunk(ch <-chan provider.StreamChunk) tea.Cmd {
	return func() tea.Msg {
		return waitForChunkSync(ch)
	}
}

// waitForChunkSync reads a single chunk from the channel.
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
