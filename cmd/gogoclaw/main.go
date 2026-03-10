package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"

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
		rc := channel.NewREST(channel.RESTConfig{
			Channel:  restCfg,
			Engine:   engDeps.Engine,
			Store:    storeDeps.Store,
			Monitor:  engDeps.Monitor,
			InboxDir: storeDeps.Workspace.Inbox,
		})
		go func() {
			log.Printf("rest: listening on %s", restCfg.Listen)
			if err := rc.Start(context.Background()); err != nil {
				log.Printf("rest: %v", err)
			}
		}()
		defer rc.Stop(context.Background())
	}

	if _, err := program.Run(); err != nil {
		log.Fatalf("tui: %v", err)
	}
}
