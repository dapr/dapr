package main

import (
	"os"
	"path"
	"time"
	"fmt"
	"strconv"
	"github.com/actionscore/actions/pkg/action"
	"github.com/actionscore/actions/e2e/mocks"
)

func main() {
	messageCount, _ := strconv.Atoi(os.Args[1])
	assetPath, _ := os.Getwd()
	assetPath = path.Join(assetPath, "eventsources")
	invoker := action.NewAction("benchmark_receiver", "8080",
		string(action.StandaloneMode), assetPath, "")
	invoker.Run("localhost:"+os.Args[4])
	fmt.Println("Launch mock app")
	app := mocks.NewMockApp(false, messageCount, true)
	start := time.Now()
	app.Run(8080)
	elapsed := int(time.Since(start)/time.Millisecond)
	os.Exit(elapsed)
}
