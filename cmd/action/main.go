package main

import (
	"flag"
	"os"
	"os/signal"
	"strconv"

	log "github.com/Sirupsen/logrus"
	"github.com/actionscore/actions/pkg/action"
	"github.com/actionscore/actions/pkg/version"
)

func main() {
	log.Infof("Starting Actions Runtime -- version %s -- commit %s", version.Version(), version.Commit())

	mode := flag.String("mode", "standalone", "")
	actionHTTPPort := flag.String("action-http-port", "3500", "")
	actionGRPCPort := flag.String("action-grpc-port", "50001", "")
	appPort := flag.String("app-port", "", "")
	appProtocol := flag.String("protocol", "http", "")
	eventSourcesPath := flag.String("event-sources-path", "./eventsources", "")
	configurationName := flag.String("configuration-name", "", "")
	actionID := flag.String("action-id", "", "")
	apiAddress := flag.String("api-address", "", "")
	placementServiceAddresss := flag.String("placement-address", "", "")
	allowedOrigins := flag.String("allowed-origins", "*", "")

	flag.Parse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	actionHTTP, _ := strconv.Atoi(*actionHTTPPort)
	actionGRPC, _ := strconv.Atoi(*actionGRPCPort)

	i := action.NewAction(*actionID, *appPort, *mode, *appProtocol, *eventSourcesPath, *configurationName, *apiAddress, *placementServiceAddresss, *allowedOrigins)
	i.Run(actionHTTP, actionGRPC)

	<-stop
}
