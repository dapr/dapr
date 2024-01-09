/*
Copyright 2023 The Dapr Authors
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

package server

import (
	"context"
	"fmt"
	"net"
	"time"

	"go.etcd.io/etcd/server/v3/embed"
	"google.golang.org/grpc"

	etcdcron "github.com/Scalingo/go-etcd-cron"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
	"github.com/dapr/dapr/pkg/security"
	"github.com/dapr/kit/logger"
)

var schedulerServerLogger = logger.NewLogger("dapr.scheduler.server")

type SchedulerServiceOpts struct {
	// Port is the port that the server will listen on.
	Port int

	Security security.Handler
}

// server is the gRPC server for the Scheduler service.
type server struct {
	opts       SchedulerServiceOpts
	srv        *grpc.Server
	shutdownCh chan struct{}
	cron       *etcdcron.Cron

	connectedHosts map[string][]string
}

// Start starts the server. Blocks until the context is cancelled.
func Start(ctx context.Context, opts SchedulerServiceOpts, secHandler security.Handler) error {
	// Init the server
	s := &server{}
	err := s.Init(ctx, opts, secHandler)
	if err != nil {
		return fmt.Errorf("failed to init server: %w", err)
	}

	return s.Run(ctx)
}

func (s *server) Init(ctx context.Context, opts SchedulerServiceOpts, secHandler security.Handler) (err error) {
	s.opts = opts
	s.shutdownCh = make(chan struct{})
	s.connectedHosts = make(map[string][]string)

	// initialize etcd server at localhost:2379

	etcd, err := embed.StartEtcd(conf())
	if err != nil {
		schedulerServerLogger.Fatal(err)
	}
	defer etcd.Close()

	select {
	case <-etcd.Server.ReadyNotify():
		schedulerServerLogger.Info("Etcd server is ready!")
	case <-time.After(1000 * time.Second):
		etcd.Server.Stop()
		schedulerServerLogger.Info("Etcd server timed out and stopped!")
	}
	// err = <-etcd.Err()
	// schedulerServerLogger.Fatal(err)

	// initialize etcd client via go-etcd-cron

	schedulerServerLogger.Info("Starting etcdcron")
	cron, err := etcdcron.New()

	if err != nil {
		schedulerServerLogger.Fatalf("fail to create etcd-cron", err)
	}
	s.cron = cron
	cron.Start(context.Background())

	// // Dummy method
	//s.ScheduleJob(context.Background(), &schedulerv1pb.ScheduleJobRequest{
	//	Job: &runtimev1pb.Job{
	//		Name:     "testDummy2",
	//		Schedule: "*/4 * * * * *",
	//	},
	//	Namespace: "default",
	//})

	err = <-etcd.Err()
	schedulerServerLogger.Fatal(err)

	// Create the gRPC server
	s.srv = grpc.NewServer(secHandler.GRPCServerOptionMTLS())
	// schedulerv1pb.RegisterSchedulerServer(s.srv, s)

	return nil
}

func (s *server) Run(ctx context.Context) error {
	lis, err := net.Listen("tcp", fmt.Sprintf(":%d", s.opts.Port))
	if err != nil {
		return fmt.Errorf("could not listen on port %d: %w", s.opts.Port, err)
	}

	errCh := make(chan error, 1)
	go func() {
		schedulerServerLogger.Infof("Running gRPC server on port %d", s.opts.Port)
		if err := s.srv.Serve(lis); err != nil {
			errCh <- fmt.Errorf("failed to serve: %w", err)
			return
		}
		errCh <- nil
	}()

	<-ctx.Done()
	schedulerServerLogger.Info("Shutting down gRPC server")

	gracefulShutdownCh := make(chan struct{})
	go func() {
		s.srv.GracefulStop()
		close(gracefulShutdownCh)
	}()
	close(s.shutdownCh)
	<-gracefulShutdownCh

	return <-errCh
}

func (s *server) ConnectHost(context.Context, *schedulerv1pb.ConnectHostRequest) (*schedulerv1pb.ConnectHostResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// ScheduleJob is a placeholder method that needs to be implemented
func (s *server) ScheduleJob(ctx context.Context, req *schedulerv1pb.ScheduleJobRequest) (*schedulerv1pb.ScheduleJobResponse, error) {
	err := s.cron.AddJob(etcdcron.Job{
		Name:   req.Job.Name,
		Rhythm: req.Job.Schedule,
		Func: func(context.Context) error {
			innerErr := s.triggerJob(req.Job, req.Namespace, req.Metadata)
			if innerErr != nil {
				return innerErr
			}
			return nil
		},
	})
	if err != nil {
		schedulerServerLogger.Errorf("error scheduling job %s: %s", req.Job.Name, err)
		return nil, err
	}
	return &schedulerv1pb.ScheduleJobResponse{}, nil
}

func (s *server) triggerJob(job *runtimev1pb.Job, namespace string, metadata map[string]string) error {
	_, err := s.TriggerJob(context.Background(), &schedulerv1pb.TriggerJobRequest{
		JobName:   job.Name,
		Namespace: namespace,
		Metadata:  metadata,
	})
	if err != nil {
		schedulerServerLogger.Errorf("error triggering job %s: %s", job.Name, err)
		return err
	}
	return nil
}

// ListJobs is a placeholder method that needs to be implemented
func (s *server) ListJobs(context.Context, *schedulerv1pb.ListJobsRequest) (*schedulerv1pb.ListJobsResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// GetJob is a placeholder method that needs to be implemented
func (s *server) GetJob(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.GetJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

// DeleteJob is a placeholder method that needs to be implemented
func (s *server) DeleteJob(context.Context, *schedulerv1pb.JobRequest) (*schedulerv1pb.DeleteJobResponse, error) {
	return nil, fmt.Errorf("not implemented")
}

func (s *server) TriggerJob(context.Context, *schedulerv1pb.TriggerJobRequest) (*schedulerv1pb.TriggerJobResponse, error) {
	schedulerServerLogger.Info("Triggering job")
	return nil, fmt.Errorf("not implemented")
}
