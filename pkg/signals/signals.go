package signals

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	log "github.com/Sirupsen/logrus"
)

// Context returns a context which will be canceled when either the SIGINT or
// SIGTERM signal is caught. It also returns a function that can be used to
// programmatically cancel the same context at any time. If either signal is
// caught a second time, the program is terminated immediately with exit code 1.
func Context() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	sigCh := make(chan os.Signal)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		log.Infof(`Received signal "%s"; beginning shutdown`, sig)
		cancel()
		sig = <-sigCh
		log.Fatalf(
			`Received signal "%s" during shutdown; exiting immediately`,
			sig,
		)
	}()
	return ctx
}
