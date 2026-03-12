package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/agent"
	"github.com/DonScott603/gogoclaw/internal/app"
	"github.com/DonScott603/gogoclaw/internal/channel"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/pii"
	"github.com/DonScott603/gogoclaw/internal/tui"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("gogoclaw %s\n", version)
		return
	}

	// Determine config directory.
	home, err := os.UserHomeDir()
	if err != nil {
		log.Fatalf("cannot determine home directory: %v", err)
	}
	configDir := filepath.Join(home, ".gogoclaw")

	// Load config.
	cfg, err := config.NewLoader(configDir).Load()
	if err != nil {
		log.Fatalf("config: %v", err)
	}

	// Initialize subsystems.
	auditDeps := app.InitAudit(cfg, configDir)
	defer auditDeps.Logger.Close()

	secDeps, err := app.InitSecurity(cfg, auditDeps)
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
		if err := agent.RunBootstrap(
			context.Background(), engDeps.Engine, configDir,
			templatesDir, os.Stdin, os.Stdout,
		); err != nil {
			log.Printf("bootstrap: %v (continuing with defaults)", err)
		} else {
			// Reload config to pick up any changes bootstrap wrote.
			cfg, err = config.NewLoader(configDir).Load()
			if err != nil {
				log.Printf("config reload after bootstrap: %v", err)
			}
			// Reinitialize system prompt with post-bootstrap config.
			newPrompt := app.LoadSystemPrompt(configDir, cfg)
			newPrompt = app.ResolvePromptVars(configDir, cfg, newPrompt)
			engDeps.Engine.SetSystemPrompt(newPrompt)
		}
	}

	// Create TUI and wire observers.
	program := tui.New(engDeps.Engine, tui.WithHealthMonitor(engDeps.Monitor))
	gate.SetProgram(program)

	onCall, onResult := tui.ToolCallObserver(program)
	engDeps.Dispatcher.SetCallbacks(onCall, onResult)

	piiWarnSend := tui.PIIWarnFunc(program)
	secDeps.PIIGate.SetWarnFn(func(patterns []string, mode pii.Mode) {
		piiWarnSend(patterns)
	})

	// Start REST channel if enabled.
	if restCfg, ok := cfg.Channels["rest"]; ok && restCfg.Enabled {
		restDeps := app.InitREST(engDeps, storeDeps, auditDeps, channel.RESTConfig{
			Channel:     restCfg,
			Engine:      engDeps.Engine,
			Store:       storeDeps.Store,
			Monitor:     engDeps.Monitor,
			AuditLogger: auditDeps.Logger,
			InboxDir:    storeDeps.Workspace.Inbox,
		})
		defer restDeps.Close()
	}

	// Start Telegram channel if enabled.
	if tgCfg, ok := cfg.Channels["telegram"]; ok && tgCfg.Enabled {
		tgDeps, err := app.InitTelegram(channel.TelegramConfig{
			Channel:     tgCfg,
			Engine:      engDeps.Engine,
			AuditLogger: auditDeps.Logger,
			InboxDir:    storeDeps.Workspace.Inbox,
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
