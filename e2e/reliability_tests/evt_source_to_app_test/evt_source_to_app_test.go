package evt_source_to_app_test

import (
	"os"
	"path"
	"testing"
	"time"

	"github.com/actionscore/actions/e2e/mocks"
	"github.com/actionscore/actions/e2e/utils"
	"github.com/actionscore/actions/pkg/action"
)

var topology2 *utils.CrashingTopology

func TestEventSourceToApp(t *testing.T) {
	if os.Getenv("CRASHING_CASE") == "stable_sender" {
		assetPath, _ := os.Getwd()
		assetPath = path.Join(assetPath, "..", "..", "assets", "evt_source_to_app_test")
		time.Sleep(time.Duration(10) * time.Second)
		invoker := action.NewAction("evt_invoker", "8080",
			string(action.StandaloneMode), assetPath, "")
		invoker.Run("localhost:3502")
		var c chan int
		<-c
	} else if os.Getenv("CRASHING_CASE") == "stable_app" {
		app := mocks.NewMockApp(false)
		app.Run(8080)
	} else {
		topology2 = utils.NewCrashingTopology()

		senderProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "stable_sender",
		}
		topology2.AddTestHost(*utils.NewTestProfile(senderProfile))

		appProfile := utils.HostProfile{
			CrashProfile:    utils.NeverCrash,
			RecoveryProfile: utils.AlwaysRecover,
			Name:            "stable_app",
		}
		topology2.AddTestHost(*utils.NewTestProfile(appProfile))

		ret := topology2.Run("TestEventSourceToApp", 80)

		analyzer := new(utils.OutputAnalyzer)
		result, _ := analyzer.AnalyzeOutputs(ret, "stable_sender", "stable_app")
		analyzer.LogTestResult(t, result)
		if result.Lost != 0 {
			t.Fatalf("Messages lost: %d", result.Lost)
		}
	}
}
