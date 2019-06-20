package performance_tests

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

func TestInvokerThroughput(t *testing.T) {
	messageCount := 10000
	if os.Getenv("CRASHING_CASE") == "benchmark_sender" {
		time.Sleep(time.Duration(10) * time.Second)
		i := 0
		for i < messageCount {
			var jsonStr = []byte(fmt.Sprintf(`{"eventName": "httptest", "data": {"title":"this is a test message %d"}}`, i))
			var msg action.Event
			json.Unmarshal(jsonStr, &msg)
			str, _ := json.Marshal(msg.Data)
			fmt.Println(string(str) + "#" + time.Now().Format(time.RFC3339))
			t.Log(string(str))
			req, _ := http.NewRequest("POST", "http://localhost:3502/publish", bytes.NewBuffer(jsonStr))
			req.Header.Set("Content-Type", "application/json")

			client := &http.Client{}
			_, err := client.Do(req)
			if err != nil {
				t.Fatalf("failed to send request message: %s", err.Error())
			}
			i++
		}
	} else if os.Getenv("CRASHING_CASE") == "benchmark_invoker" {
		assetPath, _ := os.Getwd()
		assetPath = path.Join(assetPath, "..", "assets", "single_invoker_throughput_test")
		invoker := action.NewAction("benchmark_invoker", "8082",
			string(action.StandaloneMode), assetPath, "")
		invoker.Run("localhost:3502")
		var c chan int
		<-c
	} else if os.Getenv("CRASHING_CASE") == "benchmark_receiver" {
		app := mocks.NewMockApp(false)
		app.Run(8082)
	} else {
		topology = utils.NewCrashingTopology()

		senderProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "benchmark_sender",
		}
		topology.AddTestHost(*utils.NewTestProfile(senderProfile))

		invokerProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "benchmark_invoker",
		}
		topology.AddTestHost(*utils.NewTestProfile(invokerProfile))

		appProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "benchmark_receiver",
		}
		topology.AddTestHost(*utils.NewTestProfile(appProfile))

		ret := topology.Run("TestInvokerThroughput", 30)

		analyzer := new(utils.OutputAnalyzer)
		result, _ := analyzer.AnalyzeOutputs(ret, "benchmark_sender", "benchmark_receiver")
		analyzer.LogTestResult(t, result)
		if result.Lost != 0 {
			t.Fatalf("Messages lost: %d", result.Lost)
		}
	}
}
