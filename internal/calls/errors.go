package calls

import "errors"

// Error is a domain error carrying a machine readable code that is mirrored
// back to the client in `call.error` frames and HTTP error envelopes.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return e.Code
}

// Is makes sentinel comparisons work on the machine readable code, since the
// concrete error instances are often created at the failure site.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code
}

func newError(code, message string) *Error {
	return &Error{Code: code, Message: message}
}

// Sentinel errors shared across the package.
var (
	ErrCallNotFound   = newError("call_not_found", "call not found")
	ErrCallFull       = newError("call_full", "call is full")
	ErrCallEnded      = newError("call_ended", "call already ended")
	ErrNotMember      = newError("not_member", "user is not a chat member")
	ErrNotParticipant = newError("not_participant", "user is not a call participant")
	ErrForbidden      = newError("forbidden", "action is not allowed for this role")
	ErrInvalidMessage = newError("invalid_message", "malformed signaling message")
	ErrInvalidState   = newError("invalid_state", "signaling state does not allow this operation")
	ErrTopology       = newError("invalid_topology", "operation is not available in the current topology")
	ErrDuplicateJoin  = newError("duplicate_join", "user already joined this call")
	ErrServiceOff     = newError("service_disabled", "calls are disabled")
)

// Code returns the machine readable code of an error.
func Code(err error) string {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return "internal"
}
