package main

import (
	"flag"
	"time"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/injector"
	"github.com/actionscore/actions/pkg/signals"
	"github.com/actionscore/actions/pkg/version"
)

var (
	logLevel = flag.String("log-level", "info", "Options are debug, info, warning, error, fatal, or panic. (default info)")
)

func main() {
	log.Infof("starting Actions Sidecar Injector -- version %s -- commit %s", version.Version(), version.Commit())

	ctx := signals.Context()
	cfg, err := injector.GetConfigFromEnvironment()
	if err != nil {
		log.Fatalf("error getting config: %s", err)
	}
	injector.NewInjector(cfg).Run(ctx)

	shutdownDuration := 5 * time.Second
	log.Infof("allowing %s for graceful shutdown to complete", shutdownDuration)
	<-time.After(shutdownDuration)
}

func init() {
	flag.Parse()

	parsedLogLevel, err := log.ParseLevel(*logLevel)
	if err == nil {
		log.SetLevel(parsedLogLevel)
		log.Infof("log level set to: %s", parsedLogLevel)
	} else {
		log.Fatalf("invalid value for --log-level: %s", *logLevel)
	}
}
