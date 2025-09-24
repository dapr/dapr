/*
Copyright 2025 The Dapr Authors
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

package security

import (
	"fmt"
	"os"
	"runtime"
	"syscall"

	"github.com/dapr/dapr/pkg/modes"
)

func checkUserIDGroupID(mode modes.DaprMode) error {
	if mode != modes.KubernetesMode || runtime.GOOS == "windows" {
		return nil
	}

	if os.Getenv("DAPR_UNSAFE_SKIP_CONTAINER_UID_GID_CHECK") == "true" {
		log.Warn("Skipping container UID/GID check due to env override.")
		return nil
	}

	uid := syscall.Geteuid()
	gid := syscall.Getegid()

	const expid = 65532
	if uid != expid || gid != expid {
		return fmt.Errorf("current user UID/GID (%[1]d:%[2]d) does not match the required UID/GID (%[3]d:%[3]d). "+
			"Dapr must be run as a non-root user %[3]d:%[3]d in Kubernetes environments. "+
			"To override this check, set the environment variable 'DAPR_UNSAFE_SKIP_CONTAINER_UID_GID_CHECK=true'",
			uid, gid, expid,
		)
	}

	log.Infof("Running in Kubernetes environment as user %d:%d", uid, gid)

	return nil
}
