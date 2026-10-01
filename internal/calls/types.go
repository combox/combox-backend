package calls

import (
	"sync"
	"time"
)

// CallKind enumerates the supported call flavours.
type CallKind string

const (
	// KindP2P is a 1:1 call (mesh until a third participant arrives).
	KindP2P CallKind = "p2p"
	// KindGroup is a multiparty call (mesh until MeshLimit, then SFU).
	KindGroup CallKind = "group"
	// KindBroadcast is a live stream: one publisher, n subscribers.
	KindBroadcast CallKind = "broadcast"
)

// Valid reports whether the kind is supported.
func (k CallKind) Valid() bool {
	switch k {
	case KindP2P, KindGroup, KindBroadcast:
		return true
	}
	return false
}

// Topology describes how media flows inside a room.
type Topology string

const (
	// TopologyMesh forwards SDP/ICE between peers; the server never touches media.
	TopologyMesh Topology = "mesh"
	// TopologySFU switches every participant onto a server side pion peer.
	TopologySFU Topology = "sfu"
	// TopologyBroadcast is SFU with a single allowed publisher.
	TopologyBroadcast Topology = "broadcast"
)

// Role limits what a participant may send.
type Role string

const (
	RolePublisher  Role = "publisher"
	RoleSubscriber Role = "subscriber"
)

// Reasons carried by `call.ended` frames.
const (
	// ReasonDeclined means the callee rejected the call before joining.
	ReasonDeclined = "declined"
)

// Valid reports whether the role is supported.
func (r Role) Valid() bool {
	return r == RolePublisher || r == RoleSubscriber
}

// MediaState is the organoleptic control state relayed to the other peers.
type MediaState struct {
	Mic      bool `json:"mic"`
	Camera   bool `json:"camera"`
	Screen   bool `json:"screen"`
	Speaking bool `json:"speaking"`
}

// Participant is a live member of a call room.
type Participant struct {
	UserID   string     `json:"user_id"`
	DeviceID string     `json:"device_id,omitempty"`
	Role     Role       `json:"role"`
	JoinedAt time.Time  `json:"joined_at"`
	Media    MediaState `json:"media"`

	mu     sync.RWMutex
	sender Sender
}

// Send delivers a frame to the bound signaling connection.
func (p *Participant) Send(env Envelope) error {
	p.mu.RLock()
	sender := p.sender
	p.mu.RUnlock()
	if sender == nil {
		return nil
	}
	return sender.Send(env)
}

// rebind points the participant at a fresh signaling connection (reconnect).
func (p *Participant) rebind(sender Sender, deviceID string) {
	p.mu.Lock()
	p.sender = sender
	if deviceID != "" {
		p.DeviceID = deviceID
	}
	p.mu.Unlock()
}

// unbind detaches the signaling connection (websocket closed).
func (p *Participant) unbind() {
	p.mu.Lock()
	p.sender = nil
	p.mu.Unlock()
}

// ownedBy reports whether the given connection currently holds this slot.
func (p *Participant) ownedBy(sender Sender) bool {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.sender == sender
}

// Clone returns a copy without the transport bound sender.
func (p *Participant) Clone() *Participant {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return &Participant{
		UserID:   p.UserID,
		DeviceID: p.DeviceID,
		Role:     p.Role,
		JoinedAt: p.JoinedAt,
		Media:    p.Media,
	}
}

// CallRecord is the persisted snapshot of a call.
type CallRecord struct {
	ID              string     `json:"id"`
	ChatID          string     `json:"chat_id"`
	Kind            CallKind   `json:"kind"`
	Topology        Topology   `json:"topology"`
	E2EE            bool       `json:"e2ee"`
	StartedBy       string     `json:"started_by"`
	StartedAt       time.Time  `json:"started_at"`
	EndedAt         *time.Time `json:"ended_at,omitempty"`
	EndReason       *string    `json:"end_reason,omitempty"`
	MaxParticipants int        `json:"max_participants"`
}

// Ended reports whether the call has been closed.
func (c CallRecord) Ended() bool { return c.EndedAt != nil }

// ParticipantRecord is a persisted join/leave of a user in a call.
type ParticipantRecord struct {
	ID          string     `json:"id"`
	CallID      string     `json:"call_id"`
	UserID      string     `json:"user_id"`
	DeviceID    string     `json:"device_id"`
	Role        Role       `json:"role"`
	JoinedAt    time.Time  `json:"joined_at"`
	LeftAt      *time.Time `json:"left_at,omitempty"`
	LeaveReason *string    `json:"leave_reason,omitempty"`
}

// ICEServer mirrors the browser RTCIceServer shape.
type ICEServer struct {
	URLs       []string `json:"urls"`
	Username   string   `json:"username,omitempty"`
	Credential string   `json:"credential,omitempty"`
}
