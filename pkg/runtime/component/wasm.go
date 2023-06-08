// This needs to be moved to contrib repo if accepted.
// Put it here just for disscuss how to pass configuration to components.
package component

import "context"

type wasmStrictSandboxKey struct{}

func WithWasmStrictSandbox(ctx context.Context, enable bool) context.Context {
	return context.WithValue(ctx, wasmStrictSandboxKey{}, enable)
}

// GetWasmStrictSandbox returns true if wasm strict sandbox is set.
// This enables the wasm component get the configuration from context.
func GetWasmStrictSandbox(ctx context.Context) bool {
	v := ctx.Value(wasmStrictSandboxKey{})
	if v == nil {
		return false
	}
	enable := v.(bool)
	return enable
}
