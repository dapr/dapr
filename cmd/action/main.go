package main

import (
	"flag"
	"os"
	"os/signal"
	"strconv"

	"github.com/actionscore/actions/pkg/action"
)

func main() {
	mode := flag.String("mode", "standalone", "")
	actionHTTPPort := flag.String("action-http-port", "3500", "")
	actionGRPCPort := flag.String("action-grpc-port", "50001", "")
	appPort := flag.String("app-port", "", "")
	appProtocol := flag.String("protocol", "http", "")
	eventSourcesPath := flag.String("event-sources-path", "./eventsources", "")
	configurationName := flag.String("configuration-name", "", "")
	actionID := flag.String("action-id", "", "")
	apiAddress := flag.String("api-address", "", "")
	assignerAddress := flag.String("assigner-address", "", "")
	allowedOrigins := flag.String("allowed-origins", "*", "")

	flag.Parse()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt)

	actionHTTP, _ := strconv.Atoi(*actionHTTPPort)
	actionGRPC, _ := strconv.Atoi(*actionGRPCPort)

	i := action.NewAction(*actionID, *appPort, *mode, *appProtocol, *eventSourcesPath, *configurationName, *apiAddress, *assignerAddress, *allowedOrigins)
	i.Run(actionHTTP, actionGRPC)

	<-stop
}
