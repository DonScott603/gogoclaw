package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/DonScott603/gogoclaw/internal/agent"
	"github.com/DonScott603/gogoclaw/internal/app"
	"github.com/DonScott603/gogoclaw/internal/channel"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/pii"
	"github.com/DonScott603/gogoclaw/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("gogoclaw %s\n", version)
		return
	}

	// Set up graceful shutdown via signal.NotifyContext.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// Determine config directory.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home directory: %v", err)
	}
	configDir := filepath.Join(home, ".gogoclaw")

	// Load env file before config so ${ENV_VAR} references resolve.
	agent.LoadEnvFile(configDir)

	// Load config.
	cfg, err := config.NewLoader(configDir).Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Initialize subsystems.
	auditDeps := app.InitAudit(cfg, configDir)
	defer auditDeps.Logger.Close()

	secDeps, err := app.InitSecurity(cfg, auditDeps, configDir)
	if err != nil {
		log.Fatalf("%v", err)
	}

	storeDeps, err := app.InitStorage(cfg, configDir, secDeps, auditDeps)
	if err != nil {
		log.Fatalf("%v", err)
	}
	defer storeDeps.Store.Close()

	memDeps := app.InitMemory(cfg, configDir, secDeps.ActiveProvider)
	defer memDeps.Close()

	skillDeps := app.InitSkills(configDir)
	defer skillDeps.Close()

	gate, confirmFn := tui.NewConfirmGate()

	engDeps := app.InitEngine(cfg, configDir, secDeps, storeDeps, memDeps, skillDeps, auditDeps, confirmFn)
	defer engDeps.Monitor.Stop()

	// Run bootstrap if first launch.
	if !agent.IsBootstrapped(configDir) {
		templatesDir := filepath.Join(filepath.Dir(os.Args[0]), "templates")
		// Fall back to templates/ relative to working directory if not found.
		if _, err := os.Stat(templatesDir); os.IsNotExist(err) {
			templatesDir = "templates"
		}
		fmt.Println("Welcome to GoGoClaw! Running first-time setup...")
		bootstrapSession, err := engDeps.SessionManager.GetOrCreate(ctx, "tui", "bootstrap")
		if err != nil {
			log.Fatalf("bootstrap: create session: %v", err)
		}
		bootstrapSender := &bootstrapAdapter{engine: engDeps.Engine, session: bootstrapSession}
		if err := agent.RunBootstrap(
			ctx, bootstrapSender, configDir,
			templatesDir, os.Stdin, os.Stdout,
		); err != nil {
			if errors.Is(err, agent.ErrSetupCancelled) {
				fmt.Println("Setup cancelled. Run GoGoClaw again when ready.")
				os.Exit(0)
			}
			log.Printf("bootstrap: %v (continuing with defaults)", err)
		} else {
			// Reload config to pick up any changes bootstrap wrote.
			cfg, err = config.NewLoader(configDir).Load()
			if err != nil {
				log.Printf("config reload after bootstrap: %v", err)
			} else {
				// Full runtime re-init with the new config.
				memDeps.Close()
				engDeps.Monitor.Stop()

				newSecDeps, err := app.InitSecurity(cfg, auditDeps, configDir)
				if err != nil {
					log.Fatalf("security re-init after bootstrap: %v", err)
				}
				secDeps = newSecDeps

				memDeps = app.InitMemory(cfg, configDir, secDeps.ActiveProvider)
				engDeps = app.InitEngine(cfg, configDir, secDeps, storeDeps, memDeps, skillDeps, auditDeps, confirmFn)
			}
		}
	}

	// Connect MCP servers and register their tools with the dispatcher.
	mcpDeps := app.InitMCP(cfg, engDeps.Dispatcher, engDeps.Monitor, secDeps.MCPTransport)
	defer mcpDeps.Close()

	// Create TUI and wire observers.
	program := tui.New(ctx, engDeps.Engine, engDeps.SessionManager, storeDeps.Store, tui.WithHealthMonitor(engDeps.Monitor))
	gate.SetProgram(program)

	onCall, onResult := tui.ToolCallObserver(program)
	engDeps.Dispatcher.SetCallbacks(onCall, onResult)

	piiWarnSend := tui.PIIWarnFunc(program)
	secDeps.PIIGate.SetWarnFn(func(patterns []string, mode pii.Mode) {
		piiWarnSend(patterns)
	})

	// Start REST channel if enabled.
	if restCfg, ok := cfg.Channels["rest"]; ok && restCfg.Enabled {
		rateLimit := restCfg.RateLimit
		if rateLimit <= 0 {
			rateLimit = 60
		}
		restDeps, err := app.InitREST(engDeps, storeDeps, auditDeps, channel.RESTConfig{
			Channel:        restCfg,
			Engine:         engDeps.Engine,
			SessionManager: engDeps.SessionManager,
			Store:          storeDeps.Store,
			Monitor:        engDeps.Monitor,
			AuditLogger:    auditDeps.Logger,
			InboxDir:       storeDeps.Workspace.Inbox,
			RateLimit:      rateLimit,
		})
		if err != nil {
			log.Fatalf("rest: %v", err)
		}
		defer restDeps.Close()
	}

	// Start Telegram channel if enabled.
	if tgCfg, ok := cfg.Channels["telegram"]; ok && tgCfg.Enabled {
		tgDeps, err := app.InitTelegram(channel.TelegramConfig{
			Channel:        tgCfg,
			Engine:         engDeps.Engine,
			SessionManager: engDeps.SessionManager,
			AuditLogger:    auditDeps.Logger,
			InboxDir:       storeDeps.Workspace.Inbox,
			Ctx:            ctx,
		})
		if err != nil {
			log.Printf("telegram: %v", err)
		} else {
			defer tgDeps.Close()
		}
	}

	if _, err := program.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}

// bootstrapAdapter wraps Engine+Session to satisfy the agent.Sender interface
// used during bootstrap, where the caller doesn't manage sessions.
type bootstrapAdapter struct {
	engine  *engine.Engine
	session *engine.Session
}

func (b *bootstrapAdapter) Send(ctx context.Context, text string) (string, error) {
	return b.engine.Send(ctx, b.session, text)
}
