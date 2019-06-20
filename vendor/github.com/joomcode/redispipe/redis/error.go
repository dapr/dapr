package redis

import (
	"github.com/joomcode/errorx"
)

var (
	// Errors is a root namespaces of all redispipe errors.
	Errors = errorx.NewNamespace("redispipe").ApplyModifiers(errorx.TypeModifierOmitStackTrace)

	// ErrOpts - options are wrong
	ErrOpts = Errors.NewSubNamespace("opts")
	// ErrContextIsNil - context is not passed to constructor
	ErrContextIsNil = ErrOpts.NewType("context_is_nil")
	// ErrNoAddressProvided - no address is given to constructor
	ErrNoAddressProvided = ErrOpts.NewType("no_address")

	// ErrTraitNotSent signals request were not written to wire
	ErrTraitNotSent = errorx.RegisterTrait("request_not_sent")

	// ErrContextClosed - context were explicitly closed (or connection / cluster were shut down)
	ErrContextClosed = Errors.NewType("connection_context_closed", ErrTraitNotSent)

	// ErrTraitConnectivity marks all networking and io errors
	ErrTraitConnectivity = errorx.RegisterTrait("network")

	// ErrIO - io error: read/write error, or timeout, or connection closed while reading/writting
	// It is not known if request were processed or not
	ErrIO = Errors.NewType("io error", ErrTraitConnectivity)

	// ErrRequest - request malformed. Can not serialize request, no reason to retry.
	ErrRequest = Errors.NewSubNamespace("request")
	// ErrArgumentType - argument is not serializable
	ErrArgumentType = ErrRequest.NewType("argument_type")
	// ErrBatchFormat - some other command in batch is malformed
	ErrBatchFormat = ErrRequest.NewType("batch_format")
	// ErrNoSlotKey - no key to determine cluster slot
	ErrNoSlotKey = ErrRequest.NewType("no_slot_key")
	// ErrRequestCancelled - request already cancelled
	ErrRequestCancelled = ErrRequest.NewType("request_cancelled")
	// ErrCommandForbidden - command is blocking or dangerous
	ErrCommandForbidden = ErrRequest.NewType("command_forbidden")

	// ErrResponse - response malformed. Redis returns unexpected response.
	ErrResponse = Errors.NewSubNamespace("response")
	// ErrResponseFormat - response is not valid Redis response
	ErrResponseFormat = ErrResponse.NewType("format")
	// ErrResponseUnexpected - response is valid redis response, but its structure/type unexpected
	ErrResponseUnexpected = ErrResponse.NewType("unexpected")
	// ErrHeaderlineTooLarge - header line too large
	ErrHeaderlineTooLarge = ErrResponse.NewType("headerline_too_large")
	// ErrHeaderlineEmpty - header line is empty
	ErrHeaderlineEmpty = ErrResponse.NewType("headerline_empty")
	// ErrIntegerParsing - integer malformed
	ErrIntegerParsing = ErrResponse.NewType("integer_parsiing")
	// ErrNoFinalRN - no final "\r\n"
	ErrNoFinalRN = ErrResponse.NewType("no_final_rn")
	// ErrUnknownHeaderType - unknown header type
	ErrUnknownHeaderType = ErrResponse.NewType("unknown_headerline_type")
	// ErrPing - ping receives wrong response
	ErrPing = ErrResponse.NewType("ping")

	// ErrTraitClusterMove signals that error happens due to cluster rebalancing.
	ErrTraitClusterMove = errorx.RegisterTrait("cluster_move")

	// ErrResult - just regular redis response.
	ErrResult = Errors.NewType("result")
	// ErrMoved - MOVED response
	ErrMoved = ErrResult.NewSubtype("moved", ErrTraitClusterMove)
	// ErrAsk - ASK response
	ErrAsk = ErrResult.NewSubtype("ask", ErrTraitClusterMove)
	// ErrLoading - redis didn't finish start
	ErrLoading = ErrResult.NewSubtype("loading", ErrTraitNotSent)
	// ErrExecEmpty - EXEC returns nil (WATCH failed) (it is strange, cause we don't support WATCH)
	ErrExecEmpty = ErrResult.NewSubtype("exec_empty")
	// ErrExecAbort - EXEC returns EXECABORT
	ErrExecAbort = ErrResult.NewSubtype("exec_abort")
	// ErrTryAgain - EXEC returns TryAgain
	ErrTryAgain = ErrResult.NewSubtype("exec_try_again")
)

var (
	// EKLine - set by response parser for unrecognized header lines.
	EKLine = errorx.RegisterProperty("line")
	// EKMovedTo - set by response parser for MOVED and ASK responses.
	EKMovedTo = errorx.RegisterProperty("movedto")
	// EKSlot - set by response parser for MOVED and ASK responses.
	EKSlot = errorx.RegisterPrintableProperty("slot")
	// EKVal - set by request writer and checker to argument value which could not be serialized.
	EKVal = errorx.RegisterPrintableProperty("val")
	// EKArgPos - set by request writer and checker to argument position which could not be serialized.
	EKArgPos = errorx.RegisterPrintableProperty("argpos")
	// EKRequest - request that triggered error.
	EKRequest = errorx.RegisterPrintableProperty("request")
	// EKRequests - batch requests that triggered error.
	EKRequests = errorx.RegisterPrintableProperty("requests")
	// EKResponse - unexpected response
	EKResponse = errorx.RegisterProperty("response")
	// EKAddress - address of redis that has a problems
	EKAddress = errorx.RegisterPrintableProperty("address")
)

var (
	// CollectTrace - should Sync and SyncCtx wrappers collect stack traces on a call side.
	CollectTrace = false
)
