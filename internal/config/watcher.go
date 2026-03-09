package config

import (
	"context"
	"log"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors the config directory for changes and triggers reloads.
type Watcher struct {
	loader   *Loader
	onChange func(*Config)
	debounce time.Duration
}

// NewWatcher creates a Watcher that calls onChange after each successful reload.
func NewWatcher(loader *Loader, onChange func(*Config)) *Watcher {
	return &Watcher{
		loader:   loader,
		onChange: onChange,
		debounce: 500 * time.Millisecond,
	}
}

// Watch starts watching the config directory. It blocks until ctx is cancelled.
func (w *Watcher) Watch(ctx context.Context) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	if err := watcher.Add(w.loader.baseDir); err != nil {
		return err
	}

	var timer *time.Timer
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-watcher.Events:
			if !ok {
				return nil
			}
			if event.Has(fsnotify.Write) || event.Has(fsnotify.Create) {
				if timer != nil {
					timer.Stop()
				}
				timer = time.AfterFunc(w.debounce, func() {
					cfg, err := w.loader.Load()
					if err != nil {
						log.Printf("config: reload failed: %v", err)
						return
					}
					if w.onChange != nil {
						w.onChange(cfg)
					}
				})
			}
		case err, ok := <-watcher.Errors:
			if !ok {
				return nil
			}
			log.Printf("config: watcher error: %v", err)
		}
	}
}
