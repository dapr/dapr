package helm

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func AssertArgsValue(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				assert.Equal(t, value, args[i+1])
				return
			}
		}
	}
	assert.Failf(t, "arg %s not found", arg)
}

func AssertArgNotPresent(t *testing.T, args []string, arg string) {
	t.Helper()
	for _, a := range args {
		if a == arg {
			assert.Failf(t, "arg %s found", arg)
			return
		}
	}
}

func AssertArgPresent(t *testing.T, args []string, arg string) {
	t.Helper()
	for _, a := range args {
		if a == arg {
			return
		}
	}
	assert.Failf(t, "arg %s not found", arg)
}

func AssertArgsValueContains(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				assert.Contains(t, args[i+1], value)
				return
			}
		}
	}
	assert.Failf(t, "arg %s not found", arg)
}

func AssertArgsValueNotContains(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				assert.NotContains(t, args[i+1], value)
				return
			}
		}
	}
	assert.Failf(t, "arg or value %s not found", arg)
}

func RequireArgsValue(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				require.Equal(t, value, args[i+1])
				return
			}
		}
	}
	require.Failf(t, "arg %s not found", arg)
}

func RequireArgNotPresent(t *testing.T, args []string, arg string) {
	t.Helper()
	for _, a := range args {
		if a == arg {
			require.Failf(t, "arg %s found", arg)
			return
		}
	}
}

func RequireArgPresent(t *testing.T, args []string, arg string) {
	t.Helper()
	for _, a := range args {
		if a == arg {
			return
		}
	}
	require.Failf(t, "arg %s not found", arg)
}

func RequireArgsValueContains(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				require.Contains(t, args[i+1], value)
				return
			}
		}
	}
	require.Failf(t, "arg %s not found", arg)
}

func RequireArgsValueNotContains(t *testing.T, args []string, arg string, value string) {
	t.Helper()
	for i, a := range args {
		if a == arg {
			if len(args) > i+1 {
				require.NotContains(t, args[i+1], value)
				return
			}
		}
	}
	require.Failf(t, "arg %s not found", arg)
}
