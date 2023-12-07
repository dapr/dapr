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

package universal

import (
	"context"
	"errors"
	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	"google.golang.org/protobuf/types/known/emptypb"
)

// Service layer funcs

func (a *Universal) ScheduleJob(ctx context.Context, inReq *runtimev1pb.ScheduleJobRequest) (*emptypb.Empty, error) {
	//validate job details, date, schedule, etc??
	var err error
	//req := scheduler.ScheduleJobRequest{
	//	Job: scheduler.Job{
	//		Name:     inReq.GetJob().GetName(),
	//		Schedule: inReq.GetJob().GetSchedule(),
	//		Data:     inReq.GetJob().GetData(),
	//		Repeats:  inReq.GetJob().GetRepeats(),
	//		DueTime:  inReq.GetJob().GetDueTime(),
	//		TTL:      inReq.GetJob().GetTtl(),
	//	},
	//}

	return &emptypb.Empty{}, err
}

func (a *Universal) DeleteJob(ctx context.Context, inReq *runtimev1pb.DeleteJobRequest) (*emptypb.Empty, error) {

	return &emptypb.Empty{}, errors.New("DeleteJob is unimplemented in universal api")
}

func (a *Universal) GetJob(ctx context.Context, inReq *runtimev1pb.GetJobRequest) (*runtimev1pb.GetJobResponse, error) {
	var response *runtimev1pb.GetJobResponse

	return response, errors.New("GetJob is unimplemented in universal api")
}

func (a *Universal) ListJobs(ctx context.Context, inReq *runtimev1pb.ListJobsRequest) (*runtimev1pb.ListJobsResponse, error) {
	var response *runtimev1pb.ListJobsResponse

	return response, errors.New("ListJobs is unimplemented in universal api")
}
