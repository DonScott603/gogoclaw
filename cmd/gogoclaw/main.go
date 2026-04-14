package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/DonScott603/gogoclaw/internal/agent"
	"github.com/DonScott603/gogoclaw/internal/app"
	"github.com/DonScott603/gogoclaw/internal/channel"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/engine"
	"github.com/DonScott603/gogoclaw/internal/pii"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/internal/tui"
	"github.com/DonScott603/gogoclaw/internal/util"
)

var version = "dev"

func main() {
	if len(os.Args) > 1 && os.Args[1] == "version" {
		fmt.Printf("gogoclaw %s\n", version)
		return
	}

	if len(os.Args) > 1 && os.Args[1] == "rotate-key" {
		home, err := os.UserHomeDir()
		if err != nil {
			log.Fatalf("cannot determine home directory: %v", err)
		}
		runRotateKey(filepath.Join(home, ".gogoclaw"))
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

	// Resolve encryption key early so audit logger is encrypted from first event.
	enc, err := app.ResolveEncryptor(cfg, configDir)
	if err != nil {
		log.Fatalf("%v", err)
	}

	// Initialize subsystems.
	auditDeps := app.InitAudit(cfg, configDir, enc)
	defer auditDeps.Logger.Close()

	secDeps, err := app.InitSecurity(cfg, auditDeps, configDir)
	if err != nil {
		log.Fatalf("%v", err)
	}

	storeDeps, err := app.InitStorage(ctx, cfg, configDir, secDeps, auditDeps, enc)
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

func runRotateKey(configDir string) {
	fs := flag.NewFlagSet("rotate-key", flag.ExitOnError)
	newPassphrase := fs.String("new-passphrase", "", "New passphrase (derives key via Argon2id with new salt)")
	generateNewKey := fs.Bool("generate-new-key", false, "Generate a new random auto-key (default if no --new-passphrase)")
	dryRun := fs.Bool("dry-run", false, "Show what would be rotated without making changes")
	fs.Parse(os.Args[2:])

	if *newPassphrase != "" && *generateNewKey {
		fmt.Fprintln(os.Stderr, "Error: --new-passphrase and --generate-new-key are mutually exclusive.")
		os.Exit(1)
	}

	// Load env file and config.
	agent.LoadEnvFile(configDir)
	cfg, err := config.NewLoader(configDir).Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error loading config: %v\n", err)
		os.Exit(1)
	}

	if !cfg.Storage.Conversations.Encrypt {
		fmt.Fprintln(os.Stderr, "Encryption is not enabled in config. Enable storage.conversations.encrypt first.")
		os.Exit(1)
	}

	// Resolve OLD key.
	oldEnc, err := app.ResolveEncryptor(cfg, configDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving current encryption key: %v\n", err)
		os.Exit(1)
	}
	if oldEnc == nil {
		fmt.Fprintln(os.Stderr, "Error: encryption is enabled but no key could be resolved.")
		os.Exit(1)
	}

	// Determine old key source.
	passphraseEnv := cfg.Storage.Conversations.PassphraseEnv
	if passphraseEnv == "" {
		passphraseEnv = "GOGOCLAW_DB_PASSPHRASE"
	}
	oldSource := "auto-key"
	if os.Getenv(passphraseEnv) != "" {
		oldSource = "passphrase"
	}

	// Resolve paths.
	dbPath := util.ExpandHome(cfg.Storage.Conversations.Path)
	if dbPath == "" {
		dbPath = filepath.Join(configDir, "data", "conversations.db")
	}
	auditPath := util.ExpandHome(cfg.Logging.Audit.Path)
	if auditPath == "" {
		auditPath = filepath.Join(configDir, "audit", "gogoclaw.jsonl")
	}

	// Resolve NEW key.
	newEnc, newSource, err := app.ResolveNewEncryptor(configDir, *newPassphrase, *dryRun)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error resolving new key: %v\n", err)
		os.Exit(1)
	}

	// Strip "(staged)" and "(dry-run)" suffixes for promotion logic.
	newSourceBase := newSource
	for _, suffix := range []string{" (staged)", " (dry-run)"} {
		newSourceBase = strings.TrimSuffix(newSourceBase, suffix)
	}

	// Get pre-rotation stats.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	stats, err := storage.GetRotationStats(ctx, dbPath, auditPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error getting rotation stats: %v\n", err)
		os.Exit(1)
	}

	// Print summary.
	fmt.Println("Encryption key rotation")
	fmt.Printf("  Old key source: %s\n", oldSource)
	fmt.Printf("  New key source: %s\n", newSource)
	fmt.Printf("  Database: %s\n", dbPath)
	fmt.Printf("  Audit log: %s\n", auditPath)
	fmt.Printf("  Encrypted messages: %d\n", stats.EncryptedMessages)
	fmt.Printf("  Plaintext messages: %d\n", stats.PlaintextMessages)
	fmt.Printf("  Encrypted audit lines: %d\n", stats.EncryptedAuditLines)
	fmt.Printf("  Plaintext audit lines: %d\n", stats.AuditLines-stats.EncryptedAuditLines)

	if *dryRun {
		fmt.Println("\nDry run — no changes made.")
		return
	}

	// Confirmation.
	fmt.Println("\nWARNING: GoGoClaw must not be running during key rotation.")
	fmt.Print("\nProceed? [y/N] ")
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	answer = strings.TrimSpace(answer)
	if answer != "y" && answer != "Y" {
		fmt.Println("Aborted.")
		return
	}

	// Run rotation.
	result, err := storage.RotateKeys(ctx, storage.RotateConfig{
		OldEncryptor: oldEnc,
		NewEncryptor: newEnc,
		DBPath:       dbPath,
		AuditPath:    auditPath,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nError: %v\n", err)
		fmt.Fprintln(os.Stderr, "\nRotation failed. Some batches may have already been committed under the new key.")
		fmt.Fprintln(os.Stderr, "The new key is saved in a staging file and will be reused automatically.")
		fmt.Fprintln(os.Stderr, "To complete: re-run 'gogoclaw rotate-key' with the same flags.")
		if newSourceBase == "passphrase" {
			fmt.Fprintln(os.Stderr, "You must provide the same --new-passphrase value on re-run.")
		}
		os.Exit(1)
	}

	// Success — promote and clean up.
	if err := app.PromoteKeyFiles(configDir, oldSource, newSourceBase); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: key promotion failed: %v\n", err)
		fmt.Fprintln(os.Stderr, "Rotation succeeded but staging files were not promoted. Promote manually.")
		os.Exit(1)
	}
	app.CleanupStagingFiles(configDir)

	fmt.Println("\nKey rotation complete.")
	fmt.Printf("  Messages re-encrypted: %d\n", result.MessagesRotated)
	fmt.Printf("  Messages skipped (already rotated): %d\n", result.MessagesSkipped)
	fmt.Printf("  Plaintext messages encrypted: %d\n", result.PlaintextEncrypted)
	fmt.Printf("  Audit lines re-encrypted: %d\n", result.AuditLinesRotated)
	fmt.Printf("  Audit lines unchanged: %d\n", result.AuditLinesPassedThru)

	bakFile := filepath.Join(configDir, "data", ".encryption_key.bak")
	if oldSource == "passphrase" {
		bakFile = filepath.Join(configDir, "data", ".encryption_salt.bak")
	}
	fmt.Printf("\nOld key backed up to %s.\n", bakFile)
	fmt.Println("Delete this backup once you've verified everything works.")

	if newSourceBase == "passphrase" {
		fmt.Printf("\nIMPORTANT: Ensure your passphrase env var (%s) is set to the new\npassphrase before starting GoGoClaw.\n", passphraseEnv)
	}
}
