// This needs to be moved to contrib repo if accepted.
// Put it here just for disscuss how to pass configuration to components.
// For reviewers, here are two potiential ways:
package component

import (
	"strconv"

	"github.com/dapr/components-contrib/metadata"
)

// ------ add wasm strict sandbox configuration via context, just leave here to show how to pass via context, I'll clean the code once we accept ------

//type wasmStrictSandboxKey struct{}
//
//func WithWasmStrictSandbox(ctx context.Context, enable bool) context.Context {
//	return context.WithValue(ctx, wasmStrictSandboxKey{}, enable)
//}
//
//// GetWasmStrictSandbox returns true if wasm strict sandbox is set.
//// This enables the wasm component get the configuration from context.
//func GetWasmStrictSandbox(ctx context.Context) bool {
//	v := ctx.Value(wasmStrictSandboxKey{})
//	if v == nil {
//		return false
//	}
//	enable := v.(bool)
//	return enable
//}

// ------ add wasm strict sandbox configuration via metadata ------

// AddWasmStrictSandbox adds wasm strict sandbox configuration to metadata.
func AddWasmStrictSandbox(meta *metadata.Base, enable bool) {
	meta.Properties["strictSandbox"] = strconv.FormatBool(enable)
}
