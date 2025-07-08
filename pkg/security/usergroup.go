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
	"bufio"
	"fmt"
	"os"
	"os/user"
	"strings"
)

func init() {
	if !isContainerized() {
		return
	}

	user, err := user.Current()
	if err != nil {
		panic(err)
	}

	const uid = "65532"
	if user.Uid != uid || user.Gid != uid {
		panic(fmt.Sprintf("Current user UID/GID (%[1]s:%[2]s) does not match the required UID/GID (%[3]s:%[3]s)."+
			"Dapr must be run as a non-root user %[3]s:%[3]s in containerized environments. "+
			user.Uid, user.Gid, uid,
		))
	}
}

func isContainerized() bool {
	if _, err := os.Stat("/.dockerenv"); err == nil {
		return true
	}

	file, err := os.Open("/proc/1/cgroup")
	if err != nil {
		panic(err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "docker") ||
			strings.Contains(line, "kubepods") ||
			strings.Contains(line, "containerd") {
			return true
		}
	}

	return false
}
