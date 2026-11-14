package cmd

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubLinterVersionOutput replaces the golangci-lint command runner with a
// fixed result for the duration of a test, so version handling can be checked
// without depending on the binary installed on the host.
func stubLinterVersionOutput(t *testing.T, out []byte, err error) {
	t.Helper()
	original := linterVersionOutput
	linterVersionOutput = func() ([]byte, error) {
		return out, err
	}
	t.Cleanup(func() {
		linterVersionOutput = original
	})
}

func TestParseWorkflow(t *testing.T) {
	t.Run("parse invalid workflow file", func(t *testing.T) {
		parsedVersion, err := parseWorkflowVersionFromFile("../testdata/check-lint-version/invalid.yml")
		assert.Equal(t, "", parsedVersion)
		assert.Error(t, err)
	})

	t.Run("parse workflow file with a missing key", func(t *testing.T) {
		parsedVersion, err := parseWorkflowVersionFromFile("../testdata/check-lint-version/invalid-test.yml")
		assert.Equal(t, "", parsedVersion)
		assert.NoError(t, err)
	})

	t.Run("parse an invalid workflow file", func(t *testing.T) {
		parsedVersion, err := parseWorkflowVersionFromFile("../testdata/check-lint-version/invalid-yaml.yml")
		assert.Equal(t, "", parsedVersion)
		assert.Error(t, err)
	})

	t.Run("parse testing workflow file", func(t *testing.T) {
		parsedVersion, err := parseWorkflowVersionFromFile("../testdata/check-lint-version/valid-test.yml")
		assert.Equal(t, "123.123.123", parsedVersion)
		assert.NoError(t, err)
	})
}

func TestParseLinterVersion(t *testing.T) {
	t.Run("output with v prefix", func(t *testing.T) {
		version, err := parseLinterVersion([]byte("golangci-lint has version v2.10.1 built with go1.24.0"))
		assert.Equal(t, "v2.10.1", version)
		assert.NoError(t, err)
	})

	t.Run("output without v prefix", func(t *testing.T) {
		version, err := parseLinterVersion([]byte("golangci-lint has version 1.54.2 built from abcdef0"))
		assert.Equal(t, "v1.54.2", version)
		assert.NoError(t, err)
	})

	t.Run("output with no version match", func(t *testing.T) {
		version, err := parseLinterVersion([]byte("command not found"))
		assert.Equal(t, "", version)
		assert.Error(t, err)
	})
}

func TestGetCurrentVersion(t *testing.T) {
	t.Run("parses stubbed command output", func(t *testing.T) {
		stubLinterVersionOutput(t, []byte("golangci-lint has version v2.10.1 built with go1.24.0"), nil)
		currentVersion, err := getCurrentVersion()
		assert.Equal(t, "v2.10.1", currentVersion)
		assert.NoError(t, err)
	})

	t.Run("returns error when command fails", func(t *testing.T) {
		stubLinterVersionOutput(t, nil, errors.New("exec: \"golangci-lint\": executable file not found in $PATH"))
		currentVersion, err := getCurrentVersion()
		assert.Equal(t, "", currentVersion)
		assert.Error(t, err)
	})

	t.Run("returns error when output has no version", func(t *testing.T) {
		stubLinterVersionOutput(t, []byte("unexpected output"), nil)
		currentVersion, err := getCurrentVersion()
		assert.Equal(t, "", currentVersion)
		assert.Error(t, err)
	})
}

func TestIsVersionValid(t *testing.T) {
	t.Run("compare versions - exactly equal to", func(t *testing.T) {
		assert.Equal(t, true, isVersionValid("v1.54.2", "v1.54.2"))
	})

	t.Run("compare versions - patch version greater (workflow)", func(t *testing.T) {
		assert.Equal(t, true, isVersionValid("v1.54.3", "v1.54.2"))
	})

	t.Run("compare versions - patch version greater (installed)", func(t *testing.T) {
		assert.Equal(t, true, isVersionValid("v1.54.2", "v1.54.3"))
	})

	t.Run("compare versions - invalid (installed)", func(t *testing.T) {
		assert.Equal(t, false, isVersionValid("v1.54.2", "v1.52.2"))
	})

	t.Run("compare versions - invalid (workflow)", func(t *testing.T) {
		assert.Equal(t, false, isVersionValid("v1.52.2", "v1.54.2"))
	})
}

func TestCompareVersions(t *testing.T) {
	t.Run("Valid comparison", func(t *testing.T) {
		workflowVersion, err := parseWorkflowVersionFromFile("../../.github/workflows/dapr.yml")
		require.NoError(t, err)
		require.NotEmpty(t, workflowVersion)
		stubLinterVersionOutput(t, []byte("golangci-lint has version "+workflowVersion+" built with go1.24.0"), nil)

		res, err := compareVersions("../../.github/workflows/dapr.yml")
		assert.Contains(t, res, "Linter version is valid")
		assert.NoError(t, err)
	})

	t.Run("Invalid comparison", func(t *testing.T) {
		stubLinterVersionOutput(t, []byte("golangci-lint has version v0.0.1 built with go1.24.0"), nil)
		res, err := compareVersions("../../.github/workflows/dapr.yml")
		assert.Contains(t, res, "Invalid version")
		assert.ErrorIs(t, err, ErrVersionNotSupported)
	})

	t.Run("Failure getting current version", func(t *testing.T) {
		stubLinterVersionOutput(t, nil, errors.New("golangci-lint not installed"))
		res, err := compareVersions("../../.github/workflows/dapr.yml")
		assert.Contains(t, res, "Error getting current version")
		assert.ErrorIs(t, err, ErrVersionNotFound)
	})

	t.Run("Invalid path for comparison", func(t *testing.T) {
		res, err := compareVersions("../testdata/check-lint-version/invalid-test-incorrect-path.yml")
		assert.Contains(t, res, "Error parsing workflow")
		assert.Error(t, err)
	})
}
