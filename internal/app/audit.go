package app

import (
	"log"
	"path/filepath"

	"github.com/DonScott603/gogoclaw/internal/audit"
	"github.com/DonScott603/gogoclaw/internal/config"
	"github.com/DonScott603/gogoclaw/internal/storage"
	"github.com/DonScott603/gogoclaw/internal/util"
)

// AuditDeps holds the initialized audit logger.
type AuditDeps struct {
	Logger *audit.Logger
}

// InitAudit creates and configures the audit logger.
func InitAudit(cfg *config.Config, configDir string) AuditDeps {
	auditPath := util.ExpandHome(cfg.Logging.Audit.Path)
	if auditPath == "" {
		auditPath = filepath.Join(configDir, "audit", "gogoclaw.jsonl")
	}
	logger, err := audit.NewLogger(audit.LoggerConfig{
		Enabled: cfg.Logging.Audit.Enabled,
		Path:    auditPath,
	})
	if err != nil {
		log.Printf("audit: failed to initialize: %v (continuing without audit)", err)
		logger, _ = audit.NewLogger(audit.LoggerConfig{Enabled: false})
	}
	return AuditDeps{Logger: logger}
}

// EnableAuditEncryption sets the encryptor on the audit logger if audit
// encryption is enabled in config.
func EnableAuditEncryption(cfg *config.Config, auditDeps AuditDeps, enc *storage.Encryptor) {
	if cfg.Logging.Audit.Encrypt && enc != nil {
		auditDeps.Logger.SetEncryptor(enc)
		log.Printf("audit: log encryption enabled")
	}
}
