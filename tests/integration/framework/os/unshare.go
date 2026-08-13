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
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/dapr/dapr/tests/integration/framework/iowriter"
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
	MustLinux(t)
	mount := t.TempDir()
	//nolint:gosec
	require.NoError(t, exec.Command("mount", "-t", "tmpfs",
		"-o", fmt.Sprintf("size=%dm", sizeMiB), "tmpfs", mount).Run())
	t.Cleanup(func() { _ = exec.Command("umount", mount).Run() })
	return mount
}

// SkipUnlessUnshareAvailable skips the test unless `unshare -Umr` is actually
// usable: `unshare` must be on PATH, unprivileged user namespaces must be
// enabled by the kernel, and the current process must be able to set up the
// uid_map mapping.
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
	if out, err := exec.Command("unshare",
		"--user", "--mount", "--map-root-user", "--propagation=private",
		"true").CombinedOutput(); err != nil {
		t.Skipf("unshare -Umr is not usable on this host: %v: %s", err, out)
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

	// The child is a whole second test binary. Captured like any other process,
	// its output is reported with this test's rather than printed straight to
	// the terminal, where it collides with the parent run's own output.
	iow := iowriter.New(t, "unshare")
	cmd.Stdout = iow
	cmd.Stderr = iow

	cmd.Env = append(os.Environ(),
		// The parent owns the terminal; a second progress line would fight it.
		"DAPR_INTEGRATION_PROGRESS=false",
		// Fold the child's own report into the output captured above, so there
		// is one place to read rather than a file the parent never mentions.
		"DAPR_INTEGRATION_LOGS_INLINE=true",
		// Without its own directory the child's start of run reset would delete
		// the reports of every other test in the parent run.
		"DAPR_INTEGRATION_LOGS_DIR="+t.TempDir(),
	)

	err = cmd.Run()
	iow.Close()
	if err != nil {
		t.Fatalf("re-exec under unshare failed: %v", err)
	}

	return true
}

// FillDisk writes 1MiB chunks to path until ENOSPC. Linux can defer ENOSPC
// reporting until Close (or fsync), so failure from either Write or Close
// satisfies the helper.
func FillDisk(t *testing.T, path string) {
	t.Helper()
	MustLinux(t)
	f, err := os.Create(path)
	require.NoError(t, err)
	chunk := make([]byte, 1024*1024)
	var writeErr error
	for range 1024 {
		if _, writeErr = f.Write(chunk); writeErr != nil {
			break
		}
	}
	closeErr := f.Close()
	require.True(t,
		errors.Is(writeErr, syscall.ENOSPC) || errors.Is(closeErr, syscall.ENOSPC),
		"expected ENOSPC from Write or Close; write=%v close=%v", writeErr, closeErr)
}
