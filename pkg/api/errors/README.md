### Error codes in Dapr

#### Introduction

This guide is intended for developers working on the Dapr code base, specifically for those updating the error handling to align with the gRPC richer error model. The goal is to standardize error responses across Dapr's services.

#### Prerequisites

- Familiarity with gRPC and its error model. Refer to the [gRPC Richer Error Model Guide](https://cloud.google.com/apis/design/errors).

#### Step 1: Understanding the Current Error Handling
Currently, error handling in Dapr is a mix of predefined errors and dynamically constructed errors within the code. 

- **Predefined Errors**: There are some legacy predefined errors located at [`/pkg/messages/predefined.go`](https://github.com/dapr/dapr/blob/master/pkg/messages/predefined.go), but the new errors are located in [`/pkg/api/errors`](https://github.com/dapr/dapr/blob/master/pkg/api/errors). These errors are standard and reused across various parts of the Dapr codebase. They provide a consistent error handling mechanism for common scenarios.
- **Dynamically Constructed Errors**: These errors are created on the fly within the code, typically to handle specific situations or errors that are not covered by the predefined set.
    
As we move predefined errors to the rich error model and a new location (`pkg/api/errors`), the first step in updating to the new error model is to familiarize yourself with the existing error handling patterns, both predefined and dynamically constructed. This understanding will be crucial when replacing them with the richer error model aligned with gRPC standards.

#### Step 2: Adding New Errors

1. **Check for existing errors**: Before creating a new error, check if a relevant error file exists in `/pkg/api/errors/`.
2. **Create a new error file**: If no relevant file exists, create a new file following the pattern `<building-block>.go`.

#### Step 3: Designing Rich Error Messages

1. **Understand Rich Error Construction**: Familiarize yourself with the construction of rich error messages. The definition can be found in [`github.com/dapr/kit/errors`](https://github.com/dapr/kit/blob/main/errors/errors.go).
2. **Helper Methods**: Utilize the error builder and the helper methods like `WithErrorInfo`, `WithResourceInfo` to enrich error information.
3. **Reference implementation**: You can check out `pkg/api/errors/state.go` for a reference implementation. The following code snippet shows how to create a new error using the helper methods:

```go
func StateStoreInvalidKeyName(storeName string, key string, msg string) error {
	return kiterrors.NewBuilder(
		codes.InvalidArgument,
		http.StatusBadRequest,
		msg,
		"ERR_MALFORMED_REQUEST",
	).
		WithErrorInfo(kiterrors.CodePrefixStateStore+kiterrors.CodeIllegalKey, nil).
		WithResourceInfo("state", storeName, "", "").
		WithFieldViolation(key, msg).
		Build()
}
```
4. **Mandatory and Optional Information**:
   - **ErrorInfo**: This is a required field.
   - **ResourceInfo and other Details fields**: These are optional but should be used following best practices as outlined in the [Google Cloud Error Model](https://cloud.google.com/apis/design/errors#error_model).

#### Step 4: Implementing the New Error Model

1. **Consistent Use**: Ensure that the new error model is used consistently throughout the codebase.
2. **Refactoring**: Replace existing errors in the code with the newly defined rich errors.

#### Step 5: Testing and Integration

1. **Integration Tests**: Add integration tests for the new error handling mechanism.
2. **Follow Existing Patterns**: Use the existing integration tests for the state API as a reference for structure and best practices. For example, these are the integration tests for the errors in state api: `/tests/integration/suite/daprd/state/grpc/errors.go` and `/tests/integration/suite/daprd/state/http/errors.go` 
