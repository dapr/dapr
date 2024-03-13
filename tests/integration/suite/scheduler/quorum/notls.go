/*
Copyright 2024 The Dapr Authors
Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at
    http://www.apache.org/licenses/LICENSE-2.0
Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package quorum

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/tests/integration/framework"
	"github.com/dapr/dapr/tests/integration/framework/process/scheduler"
	"github.com/dapr/dapr/tests/integration/framework/util"
	"github.com/dapr/dapr/tests/integration/suite"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func init() {
	suite.Register(new(notls))
}

// notls tests scheduler can find quorum with tls disabled.
type notls struct {
	schedulers []*scheduler.Scheduler
}

func (n *notls) Setup(t *testing.T) []framework.Option {
	fp := util.ReservePorts(t, 6)

	opts := []scheduler.Option{ //switch to 0
		//scheduler.WithInitialCluster(fmt.Sprintf("scheduler0=http://localhost:%d,scheduler1=http://localhost:%d,scheduler2=http://localhost:%d", fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2))),
		scheduler.WithInitialCluster(fmt.Sprintf("scheduler0=http://localhost:%d,scheduler1=http://localhost:%d", fp.Port(t, 0), fp.Port(t, 1))),
		scheduler.WithInitialClusterPorts(fp.Port(t, 0), fp.Port(t, 1)),
		//scheduler.WithInitialClusterPorts(fp.Port(t, 0), fp.Port(t, 1), fp.Port(t, 2)),
	}

	//switch to zero
	clientPorts := []string{
		"scheduler0=" + strconv.Itoa(fp.Port(t, 3)),
		"scheduler1=" + strconv.Itoa(fp.Port(t, 4)),
		//"scheduler2=" + strconv.Itoa(fp.Port(t, 5)),
	}
	n.schedulers = []*scheduler.Scheduler{
		scheduler.New(t, append(opts, scheduler.WithID("scheduler0"), scheduler.WithEtcdClientPorts(clientPorts))...),
		scheduler.New(t, append(opts, scheduler.WithID("scheduler1"), scheduler.WithEtcdClientPorts(clientPorts))...),
		//scheduler.New(t, append(opts, scheduler.WithID("scheduler2"), scheduler.WithEtcdClientPorts(clientPorts))...),
	}

	fp.Free(t)
	return []framework.Option{
		framework.WithProcesses(n.schedulers[0], n.schedulers[1]),
		//framework.WithProcesses(n.schedulers[0], n.schedulers[1], n.schedulers[2]),
	}
}

func (n *notls) Run(t *testing.T, ctx context.Context) {
	n.schedulers[0].WaitUntilRunning(t, ctx)
	n.schedulers[1].WaitUntilRunning(t, ctx)
	//n.schedulers[2].WaitUntilRunning(t, ctx)

	// Randomly choose one scheduler
	//chosenScheduler := n.schedulers[rand.Intn(3)]
	chosenScheduler := n.schedulers[0] //hardcoded for now
	log.Printf("CASSIE: choose scheduler: %s", chosenScheduler.ID())

	host := chosenScheduler.Address()
	conn, err := grpc.DialContext(ctx, host, grpc.WithBlock(), grpc.WithReturnConnectionError(),
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	)
	require.NoError(t, err)

	fmt.Println("CASSIE!!! no issue w/ first conn")

	t.Cleanup(func() { require.NoError(t, conn.Close()) })

	client := schedulerv1pb.NewSchedulerClient(conn)

	jobName := "appID||testJob"
	req := &schedulerv1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:     jobName,
			Schedule: "@every 1s",
		},
		Namespace: "default",
		Metadata:  map[string]string{"app_id": "test"},
	}

	log.Println("CASSIE: before scheduling job")

	_, err = client.ScheduleJob(ctx, req)

	// Keep this, I believe the record is added to the db at trigger time so I need the sleep for the db check to pass
	time.Sleep(2 * time.Second) // allow time for the record to be added to the db

	log.Printf("CASSIE: after scheduling job: %s", err)
	require.NoError(t, err)
	//fmt.Println("CASSIE!!!filepath.Walk")

	fmt.Println("HITHER url: %s:%d", chosenScheduler.Address(), n.schedulers[0].EtcdClientPort())
	fmt.Println("&&&&&&&&&&Cassie: scheduled job - check if in etcd", n.schedulers[0].EtcdClientPort())

	//time.Sleep(15 * time.Second)
	log.Printf("CASSIE: scheduler: %+v", chosenScheduler)

	// Assuming n.schedulers[0].ID holds the scheduler ID you want to match
	targetID := n.schedulers[0].ID()
	targetID2 := n.schedulers[1].ID()

	// Iterate through the EtcdClientPort slice to find the corresponding port
	var targetPort string
	for _, entry := range n.schedulers[0].EtcdClientPort() {
		parts := strings.Split(entry, "=")
		if len(parts) == 2 && parts[0] == targetID {
			targetPort = parts[1]
			break
		}
	}

	// Check if the target port is found
	if targetPort == "" {
		fmt.Printf("Port for scheduler %s not found\n", targetID)
		return

		// Handle the case where the port is not found
	}

	cmd := exec.Command("etcdctl", "get", "", "--prefix", fmt.Sprintf("--endpoints=localhost:%s", targetPort))
	log.Printf("CASSIE: running cmd: %+v", cmd)

	// Run the command and capture output
	output, err := cmd.CombinedOutput()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	log.Printf("\nCASSIE: cmd output: %+v\n", string(output[:]))

	// Convert output bytes to string and split by newline
	lines := strings.Split(string(output[:]), "\n")

	// Check if any line contains "appID"
	found := false
	for _, line := range lines {
		if strings.Contains(line, "etcd_cron/appid__testjob") {
			found = true
			break
		}
	}

	require.True(t, found)
	// Print result
	if found {
		fmt.Println("Output contains 'appID'")

	} else {
		fmt.Println("Output does not contain 'appID'")
	}

	// Iterate through the EtcdClientPort slice to find the corresponding port
	var targetPort2 string
	for _, entry := range n.schedulers[1].EtcdClientPort() {
		parts := strings.Split(entry, "=")
		if len(parts) == 2 && parts[0] == targetID2 {
			targetPort2 = parts[1]
			break
		}
	}

	// Check if the target port is found
	if targetPort2 == "" {
		fmt.Printf("Port for scheduler %s not found\n", targetID2)
		return

		// Handle the case where the port is not found
	}
	log.Printf("CASSIE: targetport2: %+v", targetPort2)

	cmd = exec.Command("etcdctl", "get", "", "--prefix", fmt.Sprintf("--endpoints=localhost:%s", targetPort2))
	log.Printf("CASSIE: running cmd: %+v", cmd)

	// Run the command and capture output
	output, err = cmd.CombinedOutput()
	if err != nil {
		fmt.Println("Error:", err)
		return
	}
	log.Printf("\nCASSIE: cmd output: %+v\n", string(output[:]))

	// Convert output bytes to string and split by newline
	lines = strings.Split(string(output[:]), "\n")

	// Check if any line contains "appID"
	found = false
	for _, line := range lines {
		if strings.Contains(line, "etcd_cron/appid__testjob") {
			found = true
			break
		}
	}

	require.True(t, found)
	// Print result
	if found {
		fmt.Println("Output contains 'appID'")

	} else {
		fmt.Println("Output does not contain 'appID'")
	}

	/*
		// Choose a different scheduler for GetJob
		//diffScheduler := n.schedulers[rand.Intn(3)]

		diffScheduler := n.schedulers[1] //hard coded for now
		log.Printf("CASSIE: diff scheduler: %s", diffScheduler.ID())

		diffHost := diffScheduler.Address()
		diffConn, diffErr := grpc.DialContext(ctx, diffHost, grpc.WithBlock(), grpc.WithReturnConnectionError(),
			grpc.WithTransportCredentials(insecure.NewCredentials()),
		)
		require.NoError(t, diffErr)
		t.Cleanup(func() { require.NoError(t, diffConn.Close()) })

		diffClient := schedulerv1pb.NewSchedulerClient(diffConn)

		log.Println("CASSIE: in if err == nil")

		// verify job was scheduled
		getJobReq := &schedulerv1pb.JobRequest{
			JobName: "testJob",
		}

		log.Println("CASSIE: before get job")

		job, getJobErr := diffClient.GetJob(ctx, getJobReq)
		log.Printf("CASSIE: after get job: %s", getJobErr)
		log.Printf("CASSIE: job: %s", job)
		log.Printf("CASSIE: return val: %v", assert.Equal(t, jobName, job.GetJob().GetName()))

		//resp, err := diffClient.ListJobs(ctx, &schedulerv1pb.ListJobsRequest{AppId: "dapr-scheduler"})
		//log.Printf("CASSIE: list job returns: %+v", resp)
		//if err != nil {
		//	log.Println(err)
		//}
		assert.Equal(t, jobName, job.GetJob().GetName())

		log.Println("CASSIE: after the eventually")
	*/
}
