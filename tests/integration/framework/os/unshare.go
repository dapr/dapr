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

package os

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// InUnshareNamespace reports whether the current process is running inside an
// unshare user+mount namespace (e.g. invoked via `unshare -Umr`). Assumes the
// outer test runner is NOT root.
func InUnshareNamespace() bool {
	return os.Geteuid() == 0
}

// MountTmpfs mounts a size-capped tmpfs at a fresh t.TempDir() and registers
// umount via t.Cleanup. Requires InUnshareNamespace() to be true.
func MountTmpfs(t *testing.T, sizeMiB int) string {
	t.Helper()
	mount := t.TempDir()
	//nolint:gosec
	require.NoError(t, exec.Command("mount", "-t", "tmpfs",
		"-o", fmt.Sprintf("size=%dm", sizeMiB), "tmpfs", mount).Run())
	t.Cleanup(func() { _ = exec.Command("umount", mount).Run() })
	return mount
}

// SkipUnlessUnshareAvailable skips the test unless `unshare` is on PATH and
// unprivileged user namespaces are enabled by the kernel.
func SkipUnlessUnshareAvailable(t *testing.T) {
	t.Helper()
	MustLinux(t)
	if _, err := exec.LookPath("unshare"); err != nil {
		t.Skipf("unshare not available: %v", err)
	}
	if b, err := os.ReadFile("/proc/sys/kernel/unprivileged_userns_clone"); err == nil {
		if strings.TrimSpace(string(b)) != "1" {
			t.Skip("kernel.unprivileged_userns_clone is disabled")
		}
	}
}

// ReexecInUserNamespace returns false if already inside an unshare user+mount
// namespace (caller should run the rest of its test body). Otherwise re-runs
// this single integration test case inside one via `unshare -Umr`, then
// returns true on success so the caller can return without running the body
// twice, or calls t.Fatalf on subprocess failure. The subprocess's ports
// framework auto-detects it is inside a namespace (mapped euid 0) and probes
// ports from a high base to avoid collisions with the parent's reservations.
func ReexecInUserNamespace(t *testing.T, ctx context.Context) bool {
	t.Helper()
	if InUnshareNamespace() {
		return false
	}
	SkipUnlessUnshareAvailable(t)

	exe, err := os.Executable()
	require.NoError(t, err)

	focus := strings.TrimSuffix(strings.TrimPrefix(t.Name(), "Test_Integration/"), "/run")

	cmd := exec.CommandContext(ctx, "unshare",
		"--user", "--mount", "--map-root-user", "--propagation=private",
		exe,
		"-test.run", "^Test_Integration$",
		"-test.v",
		"-focus", "^"+regexp.QuoteMeta(focus)+"$",
	)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("re-exec under unshare failed: %v", err)
	}
	return true
}

// FillDisk writes 1MiB chunks to path until ENOSPC.
func FillDisk(t *testing.T, path string) {
	t.Helper()
	f, err := os.Create(path)
	require.NoError(t, err)
	chunk := make([]byte, 1024*1024)
	var writeErr error
	for range 1024 {
		if _, writeErr = f.Write(chunk); writeErr != nil {
			break
		}
	}
	require.NoError(t, f.Close())
	require.Error(t, writeErr)
	require.Contains(t, writeErr.Error(), "no space left on device")
}
