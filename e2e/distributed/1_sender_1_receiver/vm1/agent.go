package main

import (
	"os"
	"path"
	"net/http"
	"time"
	"strconv"
	"strings"
	"fmt"
	"bytes"
	"github.com/actionscore/actions/pkg/action"
)

func main() {
	messageCount, _ := strconv.Atoi(os.Args[1])
	messageSize, _ := strconv.Atoi(os.Args[2])
	target := os.Args[3]
	assetPath, _ := os.Getwd()
	assetPath = path.Join(assetPath, "eventsources")
	invoker := action.NewAction("benchmark_invoker", "",
		string(action.StandaloneMode), assetPath, "")
	invoker.Run("localhost:"+os.Args[4])
	fmt.Print("Sending messages...")
	i := 0
	client := &http.Client{}
	start := time.Now()
	for i < messageCount {
		var jsonStr = fmt.Sprintf(`{"eventName": "httptest", "data": {"title":"message %d", "payload":"LOAD"}, "to" : ["%s"]}`, i, target)
		payload := strings.Repeat("*", messageSize-len(jsonStr))
		jsonStr = strings.Replace(jsonStr, "LOAD", payload, -1)
		data := []byte(jsonStr)
		req, _ := http.NewRequest("POST", "http://localhost:" + os.Args[4] + "/publish", bytes.NewBuffer(data))
		req.Header.Set("Content-Type", "application/json")
		client.Do(req)
		i++
	}
	elapsed := int(time.Since(start)/time.Millisecond)
	os.Exit(elapsed)
}
