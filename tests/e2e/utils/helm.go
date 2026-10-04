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

package utils

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// HelmUpgradeDapr upgrades the deployed Dapr release in place, reusing every
// deployed value and applying the given overrides. Tests use this to change
// one control plane setting on a live cluster, ex:
// HelmUpgradeDapr(ns, "global.scheduler.placement.enabled=true").
func HelmUpgradeDapr(namespace string, setValues ...string) error {
	_, thisFile, _, ok := runtime.Caller(0)
	if !ok {
		return errors.New("failed to resolve the repository root")
	}
	chartDir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "charts", "dapr")

	release := os.Getenv("HELM_RELEASE_NAME")
	if release == "" {
		release = "dapr"
	}

	args := []string{
		"upgrade", release, chartDir,
		"--namespace", namespace,
		"--reuse-values",
		"--wait", "--timeout", "10m0s",
	}
	for _, v := range setValues {
		args = append(args, "--set="+v)
	}

	// The helm binary and arguments are test infrastructure constants, not
	// external input.
	//nolint:gosec
	out, err := exec.Command("helm", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("helm upgrade failed: %w: %s", err, string(out))
	}
	return nil
}
