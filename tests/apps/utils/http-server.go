package utils

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// StartServer starts a HTTP or HTTP2 server
func StartServer(port int, appRouter func() *mux.Router, allowHTTP2 bool) {
	// HTTP/2 is allowed only if the DAPR_TESTS_HTTP2 env var is set
	if allowHTTP2 {
		allowHTTP2, _ = strconv.ParseBool(os.Getenv("DAPR_TESTS_HTTP2"))
	}

	// logConnState := IsTruthy(os.Getenv("DAPR_TESTS_LOG_CONNSTATE"))
	logConnState := false

	// Create a listener
	addr := fmt.Sprintf(":%d", port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		log.Fatalf("Failed to create listener: %v", err)
	}

	var server *http.Server
	if allowHTTP2 {
		// Create a server capable of supporting HTTP2 Cleartext connections
		// Also supports HTTP1.1 and upgrades from HTTP1.1 to HTTP2
		h2s := &http2.Server{}
		server = &http.Server{
			Addr:    addr,
			Handler: h2c.NewHandler(appRouter(), h2s),
			ConnState: func(c net.Conn, cs http.ConnState) {
				if logConnState {
					log.Printf("ConnState changed: %s -> %s state: %s (HTTP2)", c.RemoteAddr(), c.LocalAddr(), cs)
				}
			},
		}
	} else {
		server = &http.Server{
			Addr:    addr,
			Handler: appRouter(),
			ConnState: func(c net.Conn, cs http.ConnState) {
				if logConnState {
					log.Printf("ConnState changed: %s -> %s state: %s", c.RemoteAddr(), c.LocalAddr(), cs)
				}
			},
		}
	}

	// Stop the server when we get a termination signal
	stopCh := make(chan os.Signal, 1)
	signal.Notify(stopCh, syscall.SIGKILL, syscall.SIGTERM, syscall.SIGINT)
	go func() {
		// Wait for cancelation signal
		<-stopCh
		log.Println("Shutdown signal received")
		ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
		defer cancel()
		server.Shutdown(ctx)
	}()

	// Blocking call
	err = server.Serve(ln)
	if err != http.ErrServerClosed {
		log.Fatalf("Failed to run server: %v", err)
	}

	log.Println("Server shut down")
}
