package msg_to_app_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path"
	"testing"
	"time"

	"github.com/actionscore/actions/e2e/mocks"
	"github.com/actionscore/actions/e2e/utils"
	"github.com/actionscore/actions/pkg/action"
)

var topology *utils.CrashingTopology

func TestMessageToApp(t *testing.T) {
	messageCount := 10
	if os.Getenv("CRASHING_CASE") == "stable_sender" {
		time.Sleep(time.Duration(10) * time.Second)
		i := 0
		for i < messageCount {
			var jsonStr = []byte(fmt.Sprintf(`{"eventName": "httptest", "data": {"title":"this is a test message %d"}}`, i))
			var msg action.Event
			json.Unmarshal(jsonStr, &msg)
			str, _ := json.Marshal(msg.Data)
			fmt.Println(string(str) + "#" + time.Now().Format(time.RFC3339))
			sent := false
			for !sent {
				req, err := http.NewRequest("POST", "http://localhost:3500/publish", bytes.NewBuffer(jsonStr))
				if err == nil {
					req.Header.Set("Content-Type", "application/json")

					client := &http.Client{}
					res, err := client.Do(req)
					if err == nil && res.StatusCode == 200 {
						sent = true
						break
					}
				}
				time.Sleep(time.Duration(100) * time.Millisecond)
			}
			i++
		}
	} else if os.Getenv("CRASHING_CASE") == "stable_invoker" {
		assetPath, _ := os.Getwd()
		assetPath = path.Join(assetPath, "..", "assets", "msg_to_app_test")
		invoker := action.NewAction("msg_invoker", "8080",
			string(action.StandaloneMode), assetPath, "")
		invoker.Run("localhost:3500")
		var c chan int
		<-c
	} else if os.Getenv("CRASHING_CASE") == "stable_app" {
		app := mocks.NewMockApp(false)
		app.Run(8080)
	} else {
		topology = utils.NewCrashingTopology()

		senderProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "stable_sender",
		}
		topology.AddTestHost(*utils.NewTestProfile(senderProfile))

		invokerProfile := utils.HostProfile{
			CrashProfile:    utils.RareCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "stable_invoker",
		}
		topology.AddTestHost(*utils.NewTestProfile(invokerProfile))

		appProfile := utils.HostProfile{
			CrashProfile:    utils.RandomCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "stable_app",
		}
		topology.AddTestHost(*utils.NewTestProfile(appProfile))

		ret := topology.Run("TestMessageToApp", 180)

		analyzer := new(utils.OutputAnalyzer)
		result, _ := analyzer.AnalyzeOutputs(ret, "stable_sender", "stable_app")
		analyzer.LogTestResult(t, result)
		if result.Lost != 0 {
			t.Fatalf("Messages lost: %d", result.Lost)
		}
	}
}
