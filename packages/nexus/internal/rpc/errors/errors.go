package errors

import "fmt"

const (
	CodeNotFound      = 404
	CodeInvalidParams = 400
	CodeInternal      = 500
)

// RPCError is the JSON-RPC error envelope.
type RPCError struct {
	Code    int        `json:"code"`
	Message string     `json:"message"`
	Data    *ErrorData `json:"data,omitempty"`
}

// ErrorData carries machine-readable error metadata.
type ErrorData struct {
	Kind string `json:"kind"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("rpc error %d: %s", e.Code, e.Message)
}

// NotFound returns an RPCError for missing resources.
func NotFound(kind string, msg string) *RPCError {
	return &RPCError{Code: CodeNotFound, Message: msg, Data: &ErrorData{Kind: kind}}
}

// InvalidParams returns an RPCError for bad request parameters.
func InvalidParams(kind string, msg string) *RPCError {
	return &RPCError{Code: CodeInvalidParams, Message: msg, Data: &ErrorData{Kind: kind}}
}

// Internal returns an RPCError for unexpected server errors.
func Internal(kind string, msg string) *RPCError {
	return &RPCError{Code: CodeInternal, Message: msg, Data: &ErrorData{Kind: kind}}
}
