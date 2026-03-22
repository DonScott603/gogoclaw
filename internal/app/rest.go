package app

import (
	"context"
	"log"

	"github.com/DonScott603/gogoclaw/internal/channel"
)

// RESTDeps holds the REST channel and its lifecycle.
type RESTDeps struct {
	Channel *channel.RESTChannel
}

// InitREST creates and starts the REST channel if enabled in config.
// Returns nil RESTDeps if the channel is not configured or not enabled.
func InitREST(engDeps EngineDeps, storeDeps StorageDeps, auditDeps AuditDeps, restCfg channel.RESTConfig) (*RESTDeps, error) {
	rc, err := channel.NewREST(restCfg)
	if err != nil {
		return nil, err
	}

	go func() {
		listen := restCfg.Channel.Listen
		if listen == "" {
			listen = "127.0.0.1:8080"
		}
		log.Printf("rest: listening on %s", listen)
		log.Printf("rest: API key configured (length %d)", len(rc.APIKey()))
		if err := rc.Start(context.Background()); err != nil {
			log.Printf("rest: %v", err)
		}
	}()

	return &RESTDeps{Channel: rc}, nil
}

// Close shuts down the REST channel.
func (d *RESTDeps) Close() {
	if d != nil && d.Channel != nil {
		d.Channel.Stop(context.Background())
	}
}
