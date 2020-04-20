package fswatcher

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
)

func Watch(ctx context.Context, dir string, eventCh chan<- struct{}) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return fmt.Errorf("failed to create watcher: %s", err)
	}
	defer watcher.Close()

	if err := watcher.Add(dir); err != nil {
		return fmt.Errorf("watcher error: %s", err)
	}

LOOP:
	for {
		select {
		// watch for events
		case event := <-watcher.Events:
			if event.Op == fsnotify.Create || event.Op == fsnotify.Write {
				if strings.Contains(event.Name, dir) {
					// give time for other updates to occur
					time.Sleep(time.Second * 1)
					eventCh <- struct{}{}
				}
			}
		case <-watcher.Errors:
			break LOOP
		case <-ctx.Done():
			break LOOP
		}
	}
	return nil
}
