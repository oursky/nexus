package commands

// Mux is a handle to the RPC connection. It is defined here as a minimal
// interface to enable testing and loose coupling from the full rpc.MuxConn type.
type Mux interface {
	Call(method string, params, result any) error
}
