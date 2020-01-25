package watcher

import (
	"context"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	log "github.com/sirupsen/logrus"
)

func StartIssuerWatcher(ctx context.Context, dir string, eventCh chan<- struct{}) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		log.Errorf("failed to create watcher: %s", err)
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		log.Errorf("watcher error: %s", err)
	}

LOOP:
	for {
		select {
		// watch for events
		case event := <-watcher.Events:
			if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
				// check if issuer cert or key changes
				if strings.Contains(event.Name, dir) {
					// give time for either the key or cert to update
					time.Sleep(time.Second * 1)
					eventCh <- struct{}{}
				}
			}
		case err := <-watcher.Errors:
			log.Errorf("watcher error event: %s", err)
			break LOOP
		case <-ctx.Done():
			log.Info("stopping watcher")
			break LOOP
		}
	}
}
