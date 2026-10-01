// Package privacy implements Telegram-style Privacy settings: the eleven
// (twelve) parameters of Telegram's "Privacy and Security -> Privacy" screen,
// the three rules (Everybody / My contacts / Nobody) plus the two exception
// lists ("Always share with" / "Never share with"), and the single
// server-side enforcement entry point (Service.Evaluate).
package privacy

import (
	"errors"
	"fmt"
)

// Supported parameters. The list mirrors the Telegram privacy screen.
const (
	ParamPhoneNumber       = "phone_number"
	ParamLastSeen          = "last_seen"
	ParamProfilePhotos     = "profile_photos"
	ParamForwardedMessages = "forwarded_messages"
	ParamCalls             = "calls"
	ParamVoiceMessages     = "voice_messages"
	ParamMessages          = "messages"
	ParamBirthday          = "birthday"
	ParamGifts             = "gifts"
	ParamBio               = "bio"
	ParamSavedMusic        = "saved_music"
	ParamInvites           = "invites"
)

// The three Telegram rules.
const (
	RuleEverybody = "everybody"
	RuleContacts  = "contacts"
	RuleNobody    = "nobody"
)

// Params lists every supported parameter in the order Telegram shows them.
// A missing privacy_settings row for a parameter means DefaultRule(param).
var Params = []string{
	ParamPhoneNumber,
	ParamLastSeen,
	ParamProfilePhotos,
	ParamForwardedMessages,
	ParamCalls,
	ParamVoiceMessages,
	ParamMessages,
	ParamBirthday,
	ParamGifts,
	ParamBio,
	ParamSavedMusic,
	ParamInvites,
}

// Defaults documents the rule of an untouched parameter. Every parameter
// defaults to everybody EXCEPT phone_number and forwarded_messages, which
// default to contacts. The map is exposed through the API so the UI can
// render Telegram-style "reset to default" affordances.
var Defaults = map[string]string{
	ParamPhoneNumber:       RuleContacts,
	ParamLastSeen:          RuleEverybody,
	ParamProfilePhotos:     RuleEverybody,
	ParamForwardedMessages: RuleContacts,
	ParamCalls:             RuleEverybody,
	ParamVoiceMessages:     RuleEverybody,
	ParamMessages:          RuleEverybody,
	ParamBirthday:          RuleEverybody,
	ParamGifts:             RuleEverybody,
	ParamBio:               RuleEverybody,
	ParamSavedMusic:        RuleEverybody,
	ParamInvites:           RuleEverybody,
}

// IsParam reports whether param is one of the supported parameters.
func IsParam(param string) bool {
	_, ok := Defaults[param]
	return ok
}

// DefaultRule returns the documented default rule of a parameter; unknown
// parameters fall back to everybody.
func DefaultRule(param string) string {
	if rule, ok := Defaults[param]; ok {
		return rule
	}
	return RuleEverybody
}

// IsRule reports whether rule is one of the three supported rules.
func IsRule(rule string) bool {
	switch rule {
	case RuleEverybody, RuleContacts, RuleNobody:
		return true
	default:
		return false
	}
}

// Row is one stored privacy_settings row. Only parameters the user actually
// touched have a row; everything else is a DefaultRule(param).
type Row struct {
	Param    string
	Rule     string
	AllowIDs []string
	DenyIDs  []string
}

// Setting is the API shape of a single parameter: the rule plus both
// exception lists and their counts, so the UI can render "Nobody (+3)".
type Setting struct {
	Param      string   `json:"param"`
	Rule       string   `json:"rule"`
	AllowIDs   []string `json:"allow_ids"`
	DenyIDs    []string `json:"deny_ids"`
	AllowCount int      `json:"allow_count"`
	DenyCount  int      `json:"deny_count"`
}

// Result is the API shape of GET /api/private/v1/profile/privacy.
type Result struct {
	Settings []Setting         `json:"settings"`
	Defaults map[string]string `json:"defaults"`
}

// Error codes used by the service.
const (
	CodeInvalidArgument = "invalid_argument"
	CodeNotFound        = "not_found"
	CodeInternal        = "internal"
)

// Sentinel validation errors, unwrappable through Error.Cause.
var (
	// ErrUnknownParam is returned for a parameter outside Params.
	ErrUnknownParam = errors.New("unknown privacy param")
	// ErrInvalidRule is returned for a rule outside the three Telegram rules.
	ErrInvalidRule = errors.New("invalid privacy rule")
	// ErrInvalidID is returned for a value that is not a valid UUID.
	ErrInvalidID = errors.New("invalid privacy id")
)

// Error is the typed error of the privacy service. Handlers map
// CodeInvalidArgument onto HTTP 400 and everything else onto HTTP 500.
type Error struct {
	Code       string
	MessageKey string
	Details    map[string]string
	Cause      error
}

func (e *Error) Error() string {
	if e.Cause == nil {
		return e.Code
	}
	return fmt.Sprintf("%s: %v", e.Code, e.Cause)
}

func (e *Error) Unwrap() error {
	return e.Cause
}

func invalidArg(key string, cause error, details map[string]string) *Error {
	return &Error{Code: CodeInvalidArgument, MessageKey: key, Details: details, Cause: cause}
}

func internalErr(cause error) *Error {
	return &Error{Code: CodeInternal, MessageKey: "error.internal", Cause: cause}
}
