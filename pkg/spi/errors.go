// Package spi defines the structured error envelope that Podium's built-in
// pluggable interfaces (SPIs, §9.1) return.
//
// The §9.3 forward-compatibility contract for out-of-process plugins requires
// every SPI failure to use a structured envelope ({code, message, retryable,
// details}) with a §6.10-namespaced code, rather than an opaque Go error
// chain. An out-of-process boundary can then serialize the failure from the
// returned value alone, without an in-process sentinel-to-code lookup table.
// The built-in SPIs (RegistryStore, HarnessAdapter, LayerSourceProvider,
// SignatureProvider, MaterializationHook) return *Error to satisfy this
// constraint while they still run in-process.
//
// spec: §9.3 "Structured errors"; codes follow the §6.10 namespacing.
package spi

import "errors"

// Error is the structured SPI error envelope. It satisfies the error
// interface. Code is the §6.10-namespaced code; Retryable reports whether the
// condition clears on its own so a caller may retry without operator action;
// Details carries optional structured context that an out-of-process boundary
// serializes alongside the code.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Retryable bool           `json:"retryable"`
	Details   map[string]any `json:"details,omitempty"`
}

// Error returns Message verbatim. It deliberately does not prepend Code so a
// structured sentinel produces the same string its errors.New predecessor did,
// which keeps fmt.Errorf wrapping and string-matching consumers unchanged.
func (e *Error) Error() string { return e.Message }

// New constructs a structured SPI error with the given §6.10 code, message,
// and retryable flag.
func New(code, message string, retryable bool) *Error {
	return &Error{Code: code, Message: message, Retryable: retryable}
}

// AsError reports whether err is, or wraps, a structured *Error and returns
// it. An out-of-process boundary (and the §6.10 HTTP edge) calls this to
// recover the {code, message, retryable, details} envelope from any error a
// built-in SPI returns.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}
