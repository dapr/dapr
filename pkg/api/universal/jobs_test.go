/*
Copyright 2026 The Dapr Authors
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
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	runtimev1pb "github.com/dapr/dapr/pkg/proto/runtime/v1"
	schedulerv1pb "github.com/dapr/dapr/pkg/proto/scheduler/v1"
)

func TestValidateOverrideRoutePath(t *testing.T) {
	t.Run("valid paths", func(t *testing.T) {
		for _, path := range []string{
			"a",
			"my-job",
			"sync-video-state/video-id",
			"a/b/c/d",
			"v1.0/job_name~1",
			"...",
			".a/b./..c",
			"user@example.com/a:b",
			"!$&'()*+,;=",
			"UPPER/lower/0123456789",
		} {
			t.Run(path, func(t *testing.T) {
				require.NoError(t, validateOverrideRoutePath(path))
			})
		}
	})

	t.Run("invalid paths", func(t *testing.T) {
		for name, path := range map[string]string{
			"empty":                  "",
			"leading slash":          "/a",
			"trailing slash":         "a/",
			"only slash":             "/",
			"double slash":           "a//b",
			"dot segment":            "./a",
			"parent segment":         "a/../b",
			"only parent segment":    "..",
			"trailing dot segment":   "a/.",
			"query":                  "a?b=c",
			"fragment":               "a#b",
			"percent encoding":       "a%2Fb",
			"encoded parent segment": "%2e%2e/a",
			"space":                  "a b",
			"backslash":              "a\\b",
			"newline":                "a\nb",
			"nul":                    "a\x00b",
			"non-ascii":              "vidéo",
			"absolute url":           "http://example.com/a",
			"angle brackets":         "<a>",
			"curly braces":           "{a}",
			"pipe":                   "a|b",
			"caret":                  "a^b",
			"backtick":               "a`b",
			"double quote":           `a"b`,
			"tab":                    "a\tb",
			"double slash in middle": "a/b//c",
		} {
			t.Run(name, func(t *testing.T) {
				require.Error(t, validateOverrideRoutePath(path))
			})
		}
	})
}

func TestScheduleJobOverrideRoutePath(t *testing.T) {
	// No scheduler client is configured, so the request is guaranteed to be
	// rejected before it would be sent to the Scheduler.
	fakeAPI := &Universal{
		logger:    testLogger,
		appID:     "fakeAPI",
		namespace: "default",
	}

	_, err := fakeAPI.ScheduleJob(t.Context(), &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:              "test",
			Schedule:          new("@daily"),
			OverrideRoutePath: new("../test"),
		},
	})
	require.Error(t, err)

	s, ok := status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, s.Code())
	assert.Equal(t, `invalid job override route path: "../test" must not contain '.' or '..' path segments`, s.Message())

	_, err = fakeAPI.ScheduleJob(t.Context(), &runtimev1pb.ScheduleJobRequest{
		Job: &runtimev1pb.Job{
			Name:              "test",
			Schedule:          new("@daily"),
			OverrideRoutePath: new(""),
		},
	})
	require.Error(t, err)

	s, ok = status.FromError(err)
	require.True(t, ok)
	assert.Equal(t, codes.InvalidArgument, s.Code())
	assert.Equal(t, "invalid job override route path: must not be empty", s.Message())
}

func TestOverrideRoutePath(t *testing.T) {
	t.Run("not set", func(t *testing.T) {
		for name, meta := range map[string]*schedulerv1pb.JobMetadata{
			"nil metadata": nil,
			"nil target":   {AppId: "app", Namespace: "default"},
			"actor target": {
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Actor{
						Actor: &schedulerv1pb.TargetActorReminder{Type: "type", Id: "id"},
					},
				},
			},
			"job target without override route path": {
				Target: &schedulerv1pb.JobTargetMetadata{
					Type: &schedulerv1pb.JobTargetMetadata_Job{Job: new(schedulerv1pb.TargetJob)},
				},
			},
		} {
			t.Run(name, func(t *testing.T) {
				assert.Nil(t, overrideRoutePath(meta))
			})
		}
	})

	t.Run("set", func(t *testing.T) {
		got := overrideRoutePath(&schedulerv1pb.JobMetadata{
			Target: &schedulerv1pb.JobTargetMetadata{
				Type: &schedulerv1pb.JobTargetMetadata_Job{Job: &schedulerv1pb.TargetJob{
					OverrideRoutePath: new("my/route"),
				}},
			},
		})
		require.NotNil(t, got)
		assert.Equal(t, "my/route", *got)
	})
}
