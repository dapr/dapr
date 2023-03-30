package operator

import (
	"testing"

	"sigs.k8s.io/controller-runtime/pkg/manager"
)

func Test_nonLeaderRunnable(t *testing.T) {
	var _ manager.LeaderElectionRunnable = nonLeaderRunnable{}
	var _ manager.Runnable = nonLeaderRunnable{}
}
