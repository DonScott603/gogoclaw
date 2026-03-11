package agent

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/DonScott603/gogoclaw/internal/config"
)

// BootstrapSummary is the JSON output parsed from the LLM's bootstrap response.
type BootstrapSummary struct {
	UserName    string `json:"user_name"`
	Personality string `json:"personality"`
	WorkDomain  string `json:"work_domain"`
	PIIMode     string `json:"pii_mode"`
}

// Sender abstracts the engine.Send method for testing.
type Sender interface {
	Send(ctx context.Context, text string) (string, error)
}

// bootstrapDirs are the directories created during Phase 1.
var bootstrapDirs = []string{
	"workspace/inbox",
	"workspace/outbox",
	"workspace/scratch",
	"workspace/documents",
	"memory/daily",
	"audit",
	"skills.d",
	"agents",
	"providers",
	"channels",
}

// templateFiles maps source paths (relative to the config templates dir) to
// destination paths (relative to configDir). Only copied if dest doesn't exist.
var templateFiles = []struct {
	Src string // relative to config templates dir (templates/config/)
	Dst string // relative to configDir
}{
	{"config.yaml", "config.yaml"},
	{"network.yaml", "network.yaml"},
	{"agents/base.yaml", "agents/base.yaml"},
	{"agents/base.md", "agents/base.md"},
	{"providers/example.yaml", "providers/example.yaml"},
	{"channels/rest.yaml", "channels/rest.yaml"},
	{"channels/telegram.yaml", "channels/telegram.yaml"},
}

// IsBootstrapped returns true if the initialized marker file exists.
func IsBootstrapped(configDir string) bool {
	_, err := os.Stat(filepath.Join(configDir, "initialized"))
	return err == nil
}

// RunBootstrap performs the two-phase bootstrap ritual.
// Phase 1: create directory structure and copy default templates.
// Phase 2: interactive Q&A through the engine to configure identity.
// templatesDir is the root templates/ directory (contains bootstrap.md and config/ subdir).
// stdin/stdout are used for the interactive conversation.
func RunBootstrap(ctx context.Context, sender Sender, configDir string, cfg *config.Config, templatesDir string, stdin io.Reader, stdout io.Writer) error {
	// Phase 1: infrastructure — template files live under templates/config/.
	configTemplatesDir := filepath.Join(templatesDir, "config")
	if err := bootstrapInfrastructure(configDir, configTemplatesDir); err != nil {
		return fmt.Errorf("agent: bootstrap phase 1: %w", err)
	}

	// Validate at least one provider is configured and reachable.
	if err := validateProviders(ctx, cfg); err != nil {
		return err
	}

	// Phase 2: identity — bootstrap.md lives at templates/bootstrap.md.
	summary, err := bootstrapIdentity(ctx, sender, templatesDir, stdin, stdout)
	if err != nil {
		return fmt.Errorf("agent: bootstrap phase 2: %w", err)
	}

	// Write results.
	if err := writeBootstrapResults(configDir, summary); err != nil {
		return fmt.Errorf("agent: bootstrap write results: %w", err)
	}

	// Create initialized marker.
	marker := filepath.Join(configDir, "initialized")
	if err := os.WriteFile(marker, []byte("ok\n"), 0644); err != nil {
		return fmt.Errorf("agent: create marker: %w", err)
	}

	return nil
}

// bootstrapInfrastructure creates directories and copies default templates.
func bootstrapInfrastructure(configDir, templatesDir string) error {
	for _, dir := range bootstrapDirs {
		path := filepath.Join(configDir, dir)
		if err := os.MkdirAll(path, 0755); err != nil {
			return fmt.Errorf("create dir %s: %w", dir, err)
		}
	}

	for _, tf := range templateFiles {
		dst := filepath.Join(configDir, tf.Dst)
		if _, err := os.Stat(dst); err == nil {
			continue // already exists, don't overwrite
		}
		src := filepath.Join(templatesDir, tf.Src)
		data, err := os.ReadFile(src)
		if err != nil {
			// Template missing is not fatal — user may have custom install.
			continue
		}
		dstDir := filepath.Dir(dst)
		if err := os.MkdirAll(dstDir, 0755); err != nil {
			return fmt.Errorf("create dir for %s: %w", tf.Dst, err)
		}
		if err := os.WriteFile(dst, data, 0644); err != nil {
			return fmt.Errorf("write %s: %w", tf.Dst, err)
		}
	}

	return nil
}

// validateProviders checks that at least one provider is configured.
// Full health checks require the provider router which is initialized later,
// so we just verify configuration presence here.
func validateProviders(ctx context.Context, cfg *config.Config) error {
	if len(cfg.Providers) == 0 {
		return fmt.Errorf("agent: bootstrap: no providers configured. Add a provider config to ~/.gogoclaw/providers/ (see providers/example.yaml)")
	}
	return nil
}

// bootstrapIdentity runs the interactive Q&A through the engine.
func bootstrapIdentity(ctx context.Context, sender Sender, templatesDir string, stdin io.Reader, stdout io.Writer) (*BootstrapSummary, error) {
	tmpl, err := os.ReadFile(filepath.Join(templatesDir, "bootstrap.md"))
	if err != nil {
		return nil, fmt.Errorf("read bootstrap template: %w", err)
	}

	// Send the bootstrap template as the first message.
	resp, err := sender.Send(ctx, string(tmpl))
	if err != nil {
		return nil, fmt.Errorf("engine send: %w", err)
	}

	scanner := bufio.NewScanner(stdin)

	// Interactive loop: show LLM response, get user input, repeat.
	for {
		fmt.Fprintln(stdout, resp)

		// Check if the response contains a JSON summary block.
		if summary := parseJSONSummary(resp); summary != nil {
			return summary, nil
		}

		fmt.Fprint(stdout, "\n> ")
		if !scanner.Scan() {
			return nil, fmt.Errorf("unexpected end of input")
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}

		resp, err = sender.Send(ctx, input)
		if err != nil {
			return nil, fmt.Errorf("engine send: %w", err)
		}
	}
}

var jsonBlockRe = regexp.MustCompile("(?s)```json\\s*\n(.*?)\n\\s*```")

// parseJSONSummary extracts a BootstrapSummary from ```json fences in text.
func parseJSONSummary(text string) *BootstrapSummary {
	matches := jsonBlockRe.FindStringSubmatch(text)
	if len(matches) < 2 {
		return nil
	}

	var summary BootstrapSummary
	if err := json.Unmarshal([]byte(matches[1]), &summary); err != nil {
		return nil
	}

	// Require at least user_name to consider it valid.
	if summary.UserName == "" {
		return nil
	}

	return &summary
}

// writeBootstrapResults creates user.md and updates base.yaml PII mode.
func writeBootstrapResults(configDir string, summary *BootstrapSummary) error {
	// Write user.md with preferences.
	userMD := fmt.Sprintf("# User Profile\n\nName: %s\nPersonality: %s\nWork Domain: %s\nPII Mode: %s\n",
		summary.UserName, summary.Personality, summary.WorkDomain, summary.PIIMode)
	userPath := filepath.Join(configDir, "agents", "user.md")
	if err := os.WriteFile(userPath, []byte(userMD), 0644); err != nil {
		return fmt.Errorf("write user.md: %w", err)
	}

	// Update base.yaml PII mode if not disabled.
	if summary.PIIMode != "" && summary.PIIMode != "disabled" {
		basePath := filepath.Join(configDir, "agents", "base.yaml")
		if err := updatePIIMode(basePath, summary.PIIMode); err != nil {
			return fmt.Errorf("update pii mode: %w", err)
		}
	}

	return nil
}

// updatePIIMode reads base.yaml, replaces the PII mode, and writes it back.
func updatePIIMode(path, mode string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	content := string(data)
	// Replace pii mode line.
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "mode:") {
			// Check if we're in the pii section by looking at previous lines.
			for j := i - 1; j >= 0; j-- {
				prev := strings.TrimSpace(lines[j])
				if prev == "pii:" {
					lines[i] = strings.Replace(line, trimmed, fmt.Sprintf("mode: %q", mode), 1)
					break
				}
				if prev != "" && !strings.HasPrefix(prev, "#") {
					break
				}
			}
		}
	}

	return os.WriteFile(path, []byte(strings.Join(lines, "\n")), fs.FileMode(0644))
}
