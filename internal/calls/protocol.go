package calls

import (
	"encoding/json"
	"strings"
)

// Signaling message types (client -> server and server -> client share the
// namespace; the direction is implied by the endpoint).
const (
	MsgJoin      = "call.join"
	MsgLeave     = "call.leave"
	MsgDecline   = "call.decline"
	MsgOffer     = "call.offer"
	MsgAnswer    = "call.answer"
	MsgCandidate = "call.candidate"
	MsgRelay     = "call.relay"
	MsgMedia     = "call.media"
	MsgTracks    = "call.tracks"
	MsgPing      = "call.ping"
	MsgPong      = "call.pong"

	MsgJoined     = "call.joined"
	MsgPeerJoined = "call.peer_joined"
	MsgPeerLeft   = "call.peer_left"
	MsgEscalate   = "call.escalate"
	MsgCallEnded  = "call.ended"
	MsgError      = "call.error"
)

// RelayKind enumerates the relayed p2p payloads.
const (
	RelayOffer     = "offer"
	RelayAnswer    = "answer"
	RelayCandidate = "candidate"
)

// Envelope is the single flat JSON frame used by the calls signaling channel.
type Envelope struct {
	Type string `json:"type"`
	ID   string `json:"id,omitempty"`

	// join / room state
	CallID    string   `json:"call_id,omitempty"`
	ChatID    string   `json:"chat_id,omitempty"`
	Kind      CallKind `json:"kind,omitempty"`
	Role      Role     `json:"role,omitempty"`
	Topology  Topology `json:"topology,omitempty"`
	DeviceID  string   `json:"device_id,omitempty"`
	E2EE      bool     `json:"e2ee,omitempty"`
	Reason    string   `json:"reason,omitempty"`
	MeshLimit int      `json:"mesh_limit,omitempty"`

	// sdp / ice
	SDP           string `json:"sdp,omitempty"`
	Candidate     string `json:"candidate,omitempty"`
	SDPMid        string `json:"sdp_mid,omitempty"`
	SDPMLineIndex *int   `json:"sdp_mline_index,omitempty"`

	// relay (mesh)
	FromUserID   string `json:"from_user_id,omitempty"`
	TargetUserID string `json:"target_user_id,omitempty"`
	RelayKind    string `json:"relay_kind,omitempty"`

	// media controls
	Media *MediaState `json:"media,omitempty"`

	// publisher track sources: track id -> audio|camera|screen
	Tracks map[string]string `json:"tracks,omitempty"`

	// bulk payloads
	Participants []*Participant `json:"participants,omitempty"`
	ICEServers   []ICEServer    `json:"ice_servers,omitempty"`

	// errors
	Code    string `json:"code,omitempty"`
	Message string `json:"message,omitempty"`
}

// ParseEnvelope decodes and validates a signaling frame.
func ParseEnvelope(data []byte) (Envelope, error) {
	var env Envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return Envelope{}, newError(ErrInvalidMessage.Code, "invalid json")
	}
	env.Type = strings.TrimSpace(env.Type)
	if env.Type == "" {
		return Envelope{}, ErrInvalidMessage
	}
	if err := env.validate(); err != nil {
		return Envelope{}, err
	}
	return env, nil
}

func (e Envelope) validate() error {
	switch e.Type {
	case MsgJoin:
		if strings.TrimSpace(e.ChatID) == "" {
			return newError(ErrInvalidMessage.Code, "chat_id is required")
		}
		if e.Kind != "" && !e.Kind.Valid() {
			return newError(ErrInvalidMessage.Code, "kind is invalid")
		}
		if e.Role != "" && !e.Role.Valid() {
			return newError(ErrInvalidMessage.Code, "role is invalid")
		}
	case MsgOffer, MsgAnswer:
		if strings.TrimSpace(e.CallID) == "" {
			return newError(ErrInvalidMessage.Code, "call_id is required")
		}
		if strings.TrimSpace(e.SDP) == "" {
			return newError(ErrInvalidMessage.Code, "sdp is required")
		}
	case MsgCandidate:
		if strings.TrimSpace(e.CallID) == "" {
			return newError(ErrInvalidMessage.Code, "call_id is required")
		}
		if strings.TrimSpace(e.Candidate) == "" {
			return newError(ErrInvalidMessage.Code, "candidate is required")
		}
		if strings.TrimSpace(e.SDPMid) == "" && e.SDPMLineIndex == nil {
			return newError(ErrInvalidMessage.Code, "sdp_mid or sdp_mline_index is required")
		}
	case MsgLeave, MsgDecline:
		if strings.TrimSpace(e.CallID) == "" {
			return newError(ErrInvalidMessage.Code, "call_id is required")
		}
	case MsgPing:
		// The call id is optional: the client may ping before it joined.
	case MsgRelay:
		if strings.TrimSpace(e.CallID) == "" {
			return newError(ErrInvalidMessage.Code, "call_id is required")
		}
		if strings.TrimSpace(e.TargetUserID) == "" {
			return newError(ErrInvalidMessage.Code, "target_user_id is required")
		}
		switch e.RelayKind {
		case RelayOffer, RelayAnswer:
			if strings.TrimSpace(e.SDP) == "" {
				return newError(ErrInvalidMessage.Code, "sdp is required")
			}
		case RelayCandidate:
			if strings.TrimSpace(e.Candidate) == "" {
				return newError(ErrInvalidMessage.Code, "candidate is required")
			}
		default:
			return newError(ErrInvalidMessage.Code, "relay_kind is invalid")
		}
	case MsgMedia:
		if strings.TrimSpace(e.CallID) == "" || e.Media == nil {
			return newError(ErrInvalidMessage.Code, "call_id and media are required")
		}
	case MsgTracks:
		if strings.TrimSpace(e.CallID) == "" || len(e.Tracks) == 0 {
			return newError(ErrInvalidMessage.Code, "call_id and tracks are required")
		}
	default:
		return newError(ErrInvalidMessage.Code, "unknown message type")
	}
	return nil
}

// newFrame builds an outgoing frame of the given type.
func newFrame(msgType string) Envelope {
	return Envelope{Type: msgType}
}

// errorFrame builds a `call.error` frame, optionally correlated to a request.
func errorFrame(reqID string, err error) Envelope {
	env := newFrame(MsgError)
	env.ID = reqID
	env.Code = Code(err)
	env.Message = err.Error()
	return env
}
