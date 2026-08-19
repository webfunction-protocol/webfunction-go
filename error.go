package webfunction

import "fmt"

// WFNError is the shared payload carried by every error this package
// returns. It is embedded in each of the four concrete error types below
// rather than used directly, so callers can distinguish error kinds with
// errors.As(&BadRequestError{}) etc. while still getting Code/Message/
// Details from one place.
//
// Mirrors the base WebFunction::Error class in the Ruby reference client
// (lib/web_function.rb), which every raised error inherits from.
type WFNError struct {
	// Code is a machine-readable error code. For BadRequestError this
	// comes from the server's error triple (or "WFN_BAD_REQUEST_ERROR" if
	// the body wasn't a triple); for the other three it's a fixed code
	// naming the error kind.
	Code string

	// Message is a human-readable description.
	Message string

	// Details carries additional structured context. For BadRequestError
	// this is the triple's third element (or the raw body if the
	// response wasn't a triple); for the others it's kind-specific (see
	// each type below).
	Details any
}

func (e WFNError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("[%s] %s", e.Code, e.Message)
	}
	return e.Message
}

// BadRequestError is returned when the server responds with status 400.
// See https://webfunction.org's error-handling section: a 400 body is
// either a JSON "error triple" ([code, message, details]) or, failing
// that, treated as an opaque body under the code "WFN_BAD_REQUEST_ERROR".
type BadRequestError struct{ WFNError }

// UnexpectedStatusCodeError is returned when the server responds with any
// status other than 200 or 400. Details holds the raw status code and
// response body under the keys "status_code" and "raw_body".
type UnexpectedStatusCodeError struct{ WFNError }

// JsonParseError is returned when a response body (200 or 400) is not
// valid JSON. Details holds "status_code" and "raw_body".
type JsonParseError struct{ WFNError }

// UnresolvedPromiseError is returned by Promise.Value when a pipelined
// call's result is read before the pipeline has been executed.
type UnresolvedPromiseError struct{ WFNError }

func newBadRequestError(code, message string, details any) *BadRequestError {
	return &BadRequestError{WFNError{Code: code, Message: message, Details: details}}
}

func newUnexpectedStatusCodeError(statusCode int, rawBody string) *UnexpectedStatusCodeError {
	return &UnexpectedStatusCodeError{WFNError{
		Code:    "WFN_UNEXPECTED_STATUS_CODE_ERROR",
		Message: fmt.Sprintf("Unexpected status code (%d)", statusCode),
		Details: map[string]any{"status_code": statusCode, "raw_body": rawBody},
	}}
}

func newJsonParseError(err error, statusCode int, rawBody string) *JsonParseError {
	return &JsonParseError{WFNError{
		Code:    "WFN_JSON_PARSE_ERROR",
		Message: err.Error(),
		Details: map[string]any{"status_code": statusCode, "raw_body": rawBody},
	}}
}

func newUnresolvedPromiseError(path string) *UnresolvedPromiseError {
	return &UnresolvedPromiseError{WFNError{
		Code:    "WFN_UNRESOLVED_PROMISE_ERROR",
		Message: "promise value read before the pipeline was executed",
		Details: map[string]any{"path": path},
	}}
}