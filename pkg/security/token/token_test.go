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

package token

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	sentryv1pb "github.com/dapr/dapr/pkg/proto/sentry/v1"
	securityConsts "github.com/dapr/dapr/pkg/security/consts"
)

func TestGetSentryTokenFromFile(t *testing.T) {
	t.Run("valid token file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte("my-token"), 0o600))

		tkn, validator, err := GetSentryTokenFromFile(path)
		require.NoError(t, err)
		assert.Equal(t, "my-token", tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_JWKS, validator)
	})

	t.Run("missing file", func(t *testing.T) {
		tkn, validator, err := GetSentryTokenFromFile(filepath.Join(t.TempDir(), "missing"))
		require.Error(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})

	t.Run("empty file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte(""), 0o600))

		tkn, validator, err := GetSentryTokenFromFile(path)
		require.Error(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})
}

// unsetSentryTokenFileEnvVar ensures securityConsts.SentryTokenFileEnvVar is
// unset for the duration of the test, restoring whatever ambient value (if
// any) it had on cleanup, so kubernetes-path tests aren't accidentally
// short-circuited into the env-var branch by the outer environment.
func unsetSentryTokenFileEnvVar(t *testing.T) {
	t.Helper()
	if original, ok := os.LookupEnv(securityConsts.SentryTokenFileEnvVar); ok {
		require.NoError(t, os.Unsetenv(securityConsts.SentryTokenFileEnvVar))
		t.Cleanup(func() { t.Setenv(securityConsts.SentryTokenFileEnvVar, original) })
	}
}

func TestGetSentryToken(t *testing.T) {
	t.Run("token file env var set", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte("env-token"), 0o600))
		t.Setenv(securityConsts.SentryTokenFileEnvVar, path)

		tkn, validator, err := GetSentryToken(false)
		require.NoError(t, err)
		assert.Equal(t, "env-token", tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_JWKS, validator)
	})

	t.Run("token file env var set forces kubernetes validator when allowed", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "token")
		require.NoError(t, os.WriteFile(path, []byte("env-token"), 0o600))
		t.Setenv(securityConsts.SentryTokenFileEnvVar, path)

		tkn, validator, err := GetSentryToken(true)
		require.NoError(t, err)
		assert.Equal(t, "env-token", tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_KUBERNETES, validator)
	})

	t.Run("token file env var set but empty", func(t *testing.T) {
		t.Setenv(securityConsts.SentryTokenFileEnvVar, "")

		tkn, validator, err := GetSentryToken(false)
		require.Error(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})

	t.Run("no env var and kubernetes not allowed returns empty", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)

		tkn, validator, err := GetSentryToken(false)
		require.NoError(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})

	t.Run("kubernetes token present", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)
		dir := t.TempDir()
		path := filepath.Join(dir, "kube-token")
		require.NoError(t, os.WriteFile(path, []byte("kube-token"), 0o600))

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = path
		legacyKubeTknPath = filepath.Join(dir, "does-not-exist")
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		tkn, validator, err := GetSentryToken(true)
		require.NoError(t, err)
		assert.Equal(t, "kube-token", tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_KUBERNETES, validator)
	})

	t.Run("kubernetes token missing falls back to legacy token", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)
		dir := t.TempDir()
		legacyPath := filepath.Join(dir, "legacy-token")
		require.NoError(t, os.WriteFile(legacyPath, []byte("legacy-token"), 0o600))

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = filepath.Join(dir, "does-not-exist")
		legacyKubeTknPath = legacyPath
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		tkn, validator, err := GetSentryToken(true)
		require.NoError(t, err)
		assert.Equal(t, "legacy-token", tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_KUBERNETES, validator)
	})

	t.Run("both kubernetes token paths missing returns empty without error", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)
		dir := t.TempDir()

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = filepath.Join(dir, "does-not-exist")
		legacyKubeTknPath = filepath.Join(dir, "also-does-not-exist")
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		tkn, validator, err := GetSentryToken(true)
		require.NoError(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})

	t.Run("unreadable kubernetes token path surfaces the error instead of silently continuing", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)
		dir := t.TempDir()

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		// A directory can never be read as a token file. Unlike a missing
		// file, this must not be treated as "no token available": before
		// this fix the read error was only checked for os.IsNotExist and
		// silently discarded otherwise, masking a real misconfiguration
		// (e.g. a permission error) as "no kubernetes token configured".
		kubeTknPath = dir
		legacyKubeTknPath = filepath.Join(dir, "does-not-exist")
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		tkn, validator, err := GetSentryToken(true)
		require.Error(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})

	t.Run("unreadable legacy token path surfaces the error", func(t *testing.T) {
		unsetSentryTokenFileEnvVar(t)
		dir := t.TempDir()

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = filepath.Join(dir, "does-not-exist")
		legacyKubeTknPath = dir
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		tkn, validator, err := GetSentryToken(true)
		require.Error(t, err)
		assert.Empty(t, tkn)
		assert.Equal(t, sentryv1pb.SignCertificateRequest_UNKNOWN, validator)
	})
}

func TestHasKubernetesToken(t *testing.T) {
	t.Run("neither path exists", func(t *testing.T) {
		dir := t.TempDir()
		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = filepath.Join(dir, "does-not-exist")
		legacyKubeTknPath = filepath.Join(dir, "also-does-not-exist")
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		assert.False(t, HasKubernetesToken())
	})

	t.Run("primary path exists", func(t *testing.T) {
		dir := t.TempDir()
		path := filepath.Join(dir, "kube-token")
		require.NoError(t, os.WriteFile(path, []byte("kube-token"), 0o600))

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = path
		legacyKubeTknPath = filepath.Join(dir, "does-not-exist")
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		assert.True(t, HasKubernetesToken())
	})

	t.Run("only legacy path exists", func(t *testing.T) {
		dir := t.TempDir()
		legacyPath := filepath.Join(dir, "legacy-token")
		require.NoError(t, os.WriteFile(legacyPath, []byte("legacy-token"), 0o600))

		originalPath, originalLegacyPath := kubeTknPath, legacyKubeTknPath
		kubeTknPath = filepath.Join(dir, "does-not-exist")
		legacyKubeTknPath = legacyPath
		t.Cleanup(func() { kubeTknPath, legacyKubeTknPath = originalPath, originalLegacyPath })

		assert.True(t, HasKubernetesToken())
	})
}
