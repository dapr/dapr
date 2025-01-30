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

package raft

import (
	"testing"

	fuzz "github.com/AdaLogics/go-fuzz-headers"
	"github.com/hashicorp/raft"

	"github.com/dapr/kit/logger"
)

func init() {
	logging.SetOutputLevel(logger.FatalLevel)
}

// Tests PlacementState with a randomized Log entry
func FuzzFSMPlacementState(f *testing.F) {
	f.Fuzz(func(t *testing.T, data []byte, str1 string, b1 bool) {
		ff := fuzz.NewConsumer(data)
		rl := &raft.Log{}
		err := ff.GenerateStruct(rl)
		if err != nil {
			return
		}
		fsm := newFSM(DaprHostMemberStateConfig{
			replicationFactor: 5,
		})
		fsm.Apply(rl)

		_ = fsm.PlacementState(b1, str1)
	})
}
