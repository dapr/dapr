package memguard

import (
	"os"
	"os/signal"
	"sync"

	"github.com/awnumar/memguard/core"
)

var (
	// Ensure we only start a single signal handling instance
	create sync.Once

	// Channel for updating the signal handler
	sigfunc = make(chan func(os.Signal), 1)

	// Channel that caught signals are sent to by the runtime
	listener = make(chan os.Signal, 4)
)

/*
CatchSignal assigns a given function to be run in the event of a signal being received by the process. If no signals are provided all signals will be caught.

  1. Signal is caught by the process
  2. Interrupt handler is executed
  3. Secure session state is wiped
  4. Process terminates with exit code 1

This function can be called multiple times with the effect that only the last call will have any effect.
*/
func CatchSignal(f func(os.Signal), signals ...os.Signal) {
	create.Do(func() {
		// Start a goroutine to listen on the channels.
		go func() {
			var handler func(os.Signal)
			for {
				select {
				case signal := <-listener:
					handler(signal)
					core.Exit(1)
				case handler = <-sigfunc:
				}
			}
		}()
	})

	// Update the handler function.
	sigfunc <- f

	// Notify the channel if we receive a signal.
	signal.Reset()
	signal.Notify(listener, signals...)
}

/*
CatchInterrupt is a wrapper around CatchSignal that makes it easy to safely handle receiving interrupt signals. If an interrupt is received, the process will wipe sensitive data in memory before terminating.

A subsequent call to CatchSignal will override this call.
*/
func CatchInterrupt() {
	CatchSignal(func(_ os.Signal) {}, os.Interrupt)
}
