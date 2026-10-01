package calls

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
)

// MemberChecker answers whether a user may take part in a chat. It is
// implemented by the transport layer on top of the chat service.
type MemberChecker interface {
	IsMember(ctx context.Context, userID, chatID string) (bool, error)
}

// StreamPublisherChecker optionally answers whether a user may publish a live
// stream in a chat. Channels restrict publishing to their owner/admins while
// every other chat stays open; the hub type asserts for it so plain member
// checkers (tests, non channel deployments) keep working unchanged.
type StreamPublisherChecker interface {
	CanPublishStream(ctx context.Context, userID, chatID string) (bool, error)
}

// Sender delivers a signaling frame to a connected client.
type Sender interface {
	Send(env Envelope) error
}

// Notifier is told about call lifecycle changes. The transport layer uses it
// to alert chat members that are not connected to the call signaling channel
// yet (incoming call ring, dismissal when the call ends).
type Notifier interface {
	CallStarted(ctx context.Context, call CallRecord)
	CallEnded(ctx context.Context, call CallRecord, reason string)
}

// Identity pins a signaling connection to a user/device.
type Identity struct {
	UserID   string
	DeviceID string
}

// JoinRequest describes an attempt to enter a call.
type JoinRequest struct {
	Identity Identity
	CallID   string
	ChatID   string
	Kind     CallKind
	Role     Role
	E2EE     bool
	Sender   Sender
}

// JoinResult is returned to the session which then emits `call.joined`.
type JoinResult struct {
	Room        *Room
	Participant *Participant
	Escalated   bool
	Created     bool
	// TrackSources are the publisher track -> source maps announced before this
	// participant was registered. Capturing them under the same lock that
	// registers the participant keeps a concurrent SetTrackSources from being
	// delivered twice (once as a broadcast, once as the join replay).
	TrackSources map[string]map[string]string
}

// Room is a live call: participants, topology and (for SFU) the routing table.
type Room struct {
	ID              string
	ChatID          string
	Kind            CallKind
	Topology        Topology
	E2EE            bool
	StartedBy       string
	StartedAt       time.Time
	MaxParticipants int
	MeshLimit       int

	mu           sync.RWMutex
	participants map[string]*Participant
	peers        map[string]*sfuPeer
	routes       map[string][]*outRoute
	trackSources map[string]map[string]string
	closed       bool
}

// outRoute is one subscriber leg of a published track.
type outRoute struct {
	ownerID string
	track   *forwardedTrack
}

func newRoom(kind CallKind, chatID, startedBy string, e2ee bool, meshLimit, maxParticipants int, now time.Time) *Room {
	topology := TopologyMesh
	if kind == KindBroadcast {
		topology = TopologyBroadcast
	}
	return &Room{
		ID:              uuid.NewString(),
		ChatID:          chatID,
		Kind:            kind,
		Topology:        topology,
		E2EE:            e2ee,
		StartedBy:       startedBy,
		StartedAt:       now,
		MaxParticipants: maxParticipants,
		MeshLimit:       meshLimit,
		participants:    map[string]*Participant{},
		peers:           map[string]*sfuPeer{},
		routes:          map[string][]*outRoute{},
		trackSources:    map[string]map[string]string{},
	}
}

// roomInfo is an immutable snapshot of the room scalar state.
type roomInfo struct {
	ID        string
	ChatID    string
	Kind      CallKind
	Topology  Topology
	E2EE      bool
	MeshLimit int
}

// describe snapshots the scalar room state under the room lock.
func (r *Room) describe() roomInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return roomInfo{
		ID:        r.ID,
		ChatID:    r.ChatID,
		Kind:      r.Kind,
		Topology:  r.Topology,
		E2EE:      r.E2EE,
		MeshLimit: r.MeshLimit,
	}
}

func (r *Room) topology() Topology {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.Topology
}

// Record converts the room into its persisted form.
func (r *Room) Record() CallRecord {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return CallRecord{
		ID:              r.ID,
		ChatID:          r.ChatID,
		Kind:            r.Kind,
		Topology:        r.Topology,
		E2EE:            r.E2EE,
		StartedBy:       r.StartedBy,
		StartedAt:       r.StartedAt,
		MaxParticipants: r.MaxParticipants,
	}
}

// snapshotParticipants returns copies without the transport senders.
func (r *Room) snapshotParticipants() []*Participant {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*Participant, 0, len(r.participants))
	for _, p := range r.participants {
		out = append(out, p.Clone())
	}
	return out
}

// participant returns the live participant entry.
func (r *Room) participant(userID string) (*Participant, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.participants[userID]
	return p, ok
}

func (r *Room) hasPublisher() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, p := range r.participants {
		if p.Role == RolePublisher {
			return true
		}
	}
	return false
}

func (r *Room) isParticipant(userID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, ok := r.participants[userID]
	return ok
}

// broadcast sends a frame to every participant except the excluded one.
func (r *Room) broadcast(env Envelope, excludeUserID string) {
	r.mu.RLock()
	targets := make([]*Participant, 0, len(r.participants))
	for id, p := range r.participants {
		if id == excludeUserID {
			continue
		}
		targets = append(targets, p)
	}
	r.mu.RUnlock()
	for _, p := range targets {
		_ = p.Send(env)
	}
}

// sendTo delivers a frame to a single participant.
func (r *Room) sendTo(userID string, env Envelope) error {
	p, ok := r.participant(userID)
	if !ok {
		return ErrNotParticipant
	}
	return p.Send(env)
}

// Hub is the in-memory registry of live calls.
type Hub struct {
	cfg      Config
	store    Store
	members  MemberChecker
	notifier Notifier
	now      func() time.Time

	mu     sync.RWMutex
	rooms  map[string]*Room
	byChat map[string]string

	newPeer func(room *Room, p *Participant) (*sfuPeer, error)
}

func newHub(cfg Config, store Store, members MemberChecker) *Hub {
	if store == nil {
		store = noopStore{}
	}
	return &Hub{
		cfg:     cfg,
		store:   store,
		members: members,
		now:     func() time.Time { return time.Now().UTC() },
		rooms:   map[string]*Room{},
		byChat:  map[string]string{},
	}
}

// ActiveCall returns the live room for a chat, if any.
func (h *Hub) ActiveCall(chatID string) (*Room, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	id, ok := h.byChat[chatID]
	if !ok {
		return nil, false
	}
	room, ok := h.rooms[id]
	if !ok || room.isClosed() {
		return nil, false
	}
	return room, true
}

func (r *Room) isClosed() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.closed
}

// Join creates or enters a call room and applies the topology rules.
func (h *Hub) Join(ctx context.Context, req JoinRequest) (*JoinResult, error) {
	if req.Identity.UserID == "" || req.ChatID == "" {
		return nil, ErrInvalidMessage
	}
	kind := req.Kind
	if kind == "" {
		kind = KindGroup
	}
	if !kind.Valid() {
		return nil, ErrInvalidMessage
	}

	room, created, err := h.resolveRoom(ctx, req, kind)
	if err != nil {
		return nil, err
	}

	// A live stream may only be published by whoever the chat allows to
	// post: channels hand that to their owner/admins, everybody else joins
	// as a subscriber. The room already exists at this point (its kind is
	// only known then), so a rejected join has to throw the room away when it
	// created it: an empty broadcast room left behind would shadow the next
	// call in that chat and turn it into a stream nobody may publish.
	if req.Role == RolePublisher && room.describe().Kind == KindBroadcast {
		if err := h.checkStreamPublisher(ctx, req); err != nil {
			if created {
				h.discardRoom(room)
			}
			return nil, err
		}
	}

	room.mu.Lock()
	if room.closed {
		room.mu.Unlock()
		return nil, ErrCallEnded
	}
	if existing, ok := room.participants[req.Identity.UserID]; ok {
		// Reconnect from the same user: rebind the transport, keep the slot.
		existing.rebind(req.Sender, req.Identity.DeviceID)
		result := &JoinResult{Room: room, Participant: existing, TrackSources: room.trackSourcesSnapshotLocked()}
		room.mu.Unlock()
		h.persistParticipant(ctx, room, existing)
		return result, nil
	}
	if len(room.participants) >= room.MaxParticipants {
		room.mu.Unlock()
		return nil, ErrCallFull
	}

	role := RolePublisher
	if room.Kind == KindBroadcast {
		role = req.Role
		if role == "" {
			role = RoleSubscriber
		}
		if !role.Valid() {
			room.mu.Unlock()
			return nil, ErrInvalidMessage
		}
		if role == RolePublisher && room.hasPublisherLocked() {
			room.mu.Unlock()
			return nil, ErrForbidden
		}
		room.Topology = TopologyBroadcast
	}

	escalated := false
	if room.Topology == TopologyMesh && len(room.participants) >= room.MeshLimit {
		room.Topology = TopologySFU
		escalated = true
	}

	p := &Participant{
		UserID:   req.Identity.UserID,
		DeviceID: req.Identity.DeviceID,
		Role:     role,
		JoinedAt: h.now(),
		Media:    MediaState{Mic: true, Camera: true},
		sender:   req.Sender,
	}
	room.participants[p.UserID] = p
	trackSources := room.trackSourcesSnapshotLocked()
	room.mu.Unlock()

	if created {
		_ = h.store.CreateCall(ctx, room.Record())
		if h.notifier != nil {
			h.notifier.CallStarted(ctx, room.Record())
		}
	}
	h.persistParticipant(ctx, room, p)

	if escalated {
		escalateFrame := newFrame(MsgEscalate)
		escalateFrame.CallID = room.ID
		escalateFrame.Topology = TopologySFU
		escalateFrame.Reason = "mesh_limit"
		room.broadcast(escalateFrame, p.UserID)
	}

	joinedFrame := newFrame(MsgPeerJoined)
	joinedFrame.CallID = room.ID
	joinedFrame.Participants = []*Participant{p.Clone()}
	room.broadcast(joinedFrame, p.UserID)

	// SFU/broadcast rooms are activated by the session after `call.joined`
	// has been written, so the first offer never overtakes the join reply.
	return &JoinResult{Room: room, Participant: p, Escalated: escalated, Created: created, TrackSources: trackSources}, nil
}

// hasPublisherLocked assumes room.mu is held for writing.
func (r *Room) hasPublisherLocked() bool {
	for _, p := range r.participants {
		if p.Role == RolePublisher {
			return true
		}
	}
	return false
}

// checkStreamPublisher asks the chat backend whether the joiner may publish a
// live stream in that chat. Members checkers without the optional capability
// skip the restriction (nothing to ask).
func (h *Hub) checkStreamPublisher(ctx context.Context, req JoinRequest) error {
	checker, ok := h.members.(StreamPublisherChecker)
	if !ok || checker == nil {
		return nil
	}
	allowed, err := checker.CanPublishStream(ctx, req.Identity.UserID, req.ChatID)
	if err != nil {
		return err
	}
	if !allowed {
		return ErrForbidden
	}
	return nil
}

// Decline lets a callee reject a ringing call they never joined: the caller
// (the only participant left waiting) is closed out with reason "declined".
// Once more than one participant is in the room the call is already live and
// the decline is a no-op.
func (h *Hub) Decline(ctx context.Context, userID, callID string) error {
	callID = strings.TrimSpace(callID)
	if userID == "" || callID == "" {
		return ErrInvalidMessage
	}
	h.mu.RLock()
	room, ok := h.rooms[callID]
	h.mu.RUnlock()
	if !ok || room.isClosed() {
		return ErrCallNotFound
	}
	if h.members != nil {
		member, err := h.members.IsMember(ctx, userID, room.ChatID)
		if err != nil {
			return err
		}
		if !member {
			return ErrNotMember
		}
	}
	if room.describe().Kind == KindBroadcast {
		// Watching a stream is optional: there is nobody to reject.
		return nil
	}
	room.mu.RLock()
	_, participating := room.participants[userID]
	waiting := len(room.participants) == 1
	room.mu.RUnlock()
	if participating || !waiting {
		return nil
	}
	h.End(ctx, room, ReasonDeclined)
	return nil
}

// resolveRoom picks (or creates) the room for a join request.
func (h *Hub) resolveRoom(ctx context.Context, req JoinRequest, kind CallKind) (*Room, bool, error) {
	// Membership is verified for every entry path: knowing a call id must not
	// be enough to join someone else's call.
	if h.members != nil {
		member, err := h.members.IsMember(ctx, req.Identity.UserID, req.ChatID)
		if err != nil {
			return nil, false, err
		}
		if !member {
			return nil, false, ErrNotMember
		}
	}

	h.mu.Lock()
	defer h.mu.Unlock()

	if req.CallID != "" {
		room, ok := h.rooms[req.CallID]
		if !ok || room.isClosed() {
			return nil, false, ErrCallNotFound
		}
		if req.ChatID != "" && room.ChatID != req.ChatID {
			return nil, false, ErrCallNotFound
		}
		return room, false, nil
	}
	if id, ok := h.byChat[req.ChatID]; ok {
		if room, exists := h.rooms[id]; exists && !room.isClosed() {
			return room, false, nil
		}
	}
	room := newRoom(kind, req.ChatID, req.Identity.UserID, req.E2EE, h.cfg.MeshLimit, h.cfg.MaxParticipants, h.now())
	h.rooms[room.ID] = room
	h.byChat[room.ChatID] = room.ID
	return room, true, nil
}

// Activate ensures SFU peers exist for every participant of a room and
// attaches already published tracks to peers that were created late.
func (h *Hub) Activate(room *Room, userID string) error {
	room.mu.RLock()
	topology := room.Topology
	closed := room.closed
	targets := make([]*Participant, 0, len(room.participants))
	for _, p := range room.participants {
		targets = append(targets, p)
	}
	room.mu.RUnlock()
	if closed || topology == TopologyMesh {
		return nil
	}
	if h.newPeer == nil {
		return newError("internal", "sfu is not initialised")
	}

	for _, p := range targets {
		if _, err := h.ensurePeer(room, p); err != nil {
			h.cfg.Logger.Error("calls: create sfu peer",
				"call_id", room.ID, "user_id", p.UserID, "error", err.Error())
			if p.UserID == userID {
				return err
			}
		}
	}
	room.attachExistingPublished()
	return nil
}

// PeerFor returns the SFU peer of a participant, creating it on demand.
func (h *Hub) PeerFor(room *Room, userID string) (*sfuPeer, error) {
	room.mu.RLock()
	peer, ok := room.peers[userID]
	closed := room.closed
	room.mu.RUnlock()
	if closed {
		return nil, ErrCallEnded
	}
	if ok && !peer.isClosed() {
		return peer, nil
	}
	part, exists := room.participant(userID)
	if !exists {
		return nil, ErrNotParticipant
	}
	return h.ensurePeer(room, part)
}

// ensurePeer creates (or repairs) the server side peer of a participant.
func (h *Hub) ensurePeer(room *Room, p *Participant) (*sfuPeer, error) {
	if h.newPeer == nil {
		return nil, newError("internal", "sfu is not initialised")
	}
	room.mu.Lock()
	if room.closed {
		room.mu.Unlock()
		return nil, ErrCallEnded
	}
	if existing, ok := room.peers[p.UserID]; ok && !existing.isClosed() {
		room.mu.Unlock()
		return existing, nil
	}
	room.mu.Unlock()

	peer, err := h.newPeer(room, p)
	if err != nil {
		return nil, err
	}

	room.mu.Lock()
	if room.closed {
		room.mu.Unlock()
		peer.close("room_closed")
		return nil, ErrCallEnded
	}
	if _, ok := room.participants[p.UserID]; !ok {
		room.mu.Unlock()
		peer.close("left")
		return nil, ErrNotParticipant
	}
	if existing, ok := room.peers[p.UserID]; ok && !existing.isClosed() {
		room.mu.Unlock()
		peer.close("replaced")
		return existing, nil
	}
	room.peers[p.UserID] = peer
	room.mu.Unlock()
	return peer, nil
}

// Leave removes a participant and closes the room when it becomes empty.
// When sender is non-nil the participant is only removed if that connection
// still owns the slot (protects against a stale socket of a reconnected user).
func (h *Hub) Leave(ctx context.Context, room *Room, userID string, sender Sender, reason string) {
	room.mu.Lock()
	if room.closed {
		room.mu.Unlock()
		return
	}
	p, ok := room.participants[userID]
	if !ok {
		room.mu.Unlock()
		return
	}
	if sender != nil && !p.ownedBy(sender) {
		room.mu.Unlock()
		return
	}
	delete(room.participants, userID)
	peer := room.peers[userID]
	delete(room.peers, userID)
	remaining := len(room.participants)
	empty := remaining == 0
	// A call that is not a broadcast ends for everybody as soon as only one
	// participant is left: hanging up from one side closes it for the other
	// side too instead of leaving a single person in a silent room.
	lonely := !empty && remaining == 1 && room.Kind != KindBroadcast
	if empty {
		room.closed = true
	}
	room.mu.Unlock()

	p.unbind()
	if peer != nil {
		peer.close(reason)
	}
	room.removeRoutesFor(userID)
	h.persistClose(ctx, room, userID, reason)

	if !empty {
		left := newFrame(MsgPeerLeft)
		left.CallID = room.ID
		left.Reason = reason
		left.Participants = []*Participant{{UserID: userID}}
		room.broadcast(left, "")
		if lonely {
			room.dropPeerCaches()
			h.End(ctx, room, reason)
		}
		return
	}

	room.dropPeerCaches()
	h.forget(room, reason)
}

// Relay forwards a mesh frame between two participants.
func (h *Hub) Relay(room *Room, fromUserID string, env Envelope) error {
	room.mu.RLock()
	topology := room.Topology
	_, fromOK := room.participants[fromUserID]
	target, targetOK := room.participants[env.TargetUserID]
	room.mu.RUnlock()

	if topology != TopologyMesh {
		return ErrTopology
	}
	if !fromOK {
		return ErrNotParticipant
	}
	if !targetOK {
		return newError("peer_not_found", "target participant is not in the call")
	}
	env.FromUserID = fromUserID
	return target.Send(env)
}

// UpdateMedia relays the organoleptic media controls to the other peers.
func (h *Hub) UpdateMedia(room *Room, userID string, media MediaState) error {
	room.mu.Lock()
	p, ok := room.participants[userID]
	if !ok {
		room.mu.Unlock()
		return ErrNotParticipant
	}
	p.Media = media
	others := make([]*Participant, 0, len(room.participants))
	for id, other := range room.participants {
		if id != userID {
			others = append(others, other)
		}
	}
	room.mu.Unlock()

	frame := newFrame(MsgMedia)
	frame.CallID = room.ID
	frame.Media = &media
	frame.Participants = []*Participant{{UserID: userID}}
	for _, other := range others {
		_ = other.Send(frame)
	}
	return nil
}

// SetTrackSources records the publisher mapping track id -> source label and
// relays it to the other participants: receivers need it to tell a screen
// share from a camera (the raw track id carries no source).
func (h *Hub) SetTrackSources(room *Room, userID string, sources map[string]string) error {
	room.mu.Lock()
	if _, ok := room.participants[userID]; !ok {
		room.mu.Unlock()
		return ErrNotParticipant
	}
	var cp map[string]string
	if len(sources) > 0 {
		cp = make(map[string]string, len(sources))
		for k, v := range sources {
			key := normalizeTrackKey(k)
			src := normalizeTrackSource(v)
			if key == "" || src == "" {
				continue
			}
			cp[key] = src
		}
	}
	if len(cp) == 0 {
		delete(room.trackSources, userID)
		cp = nil
	} else {
		if room.trackSources == nil {
			room.trackSources = map[string]map[string]string{}
		}
		room.trackSources[userID] = cp
	}
	// Targets are collected while the write lock is still held: a participant
	// registering concurrently either sees this map in its join snapshot or is
	// in this list, never both (otherwise peers would receive the same map
	// twice as a broadcast and as the join replay).
	targets := make([]*Participant, 0, len(room.participants))
	for id, p := range room.participants {
		if id == userID {
			continue
		}
		targets = append(targets, p)
	}
	room.mu.Unlock()

	frame := newFrame(MsgTracks)
	frame.CallID = room.ID
	frame.FromUserID = userID
	frame.Tracks = cp
	for _, p := range targets {
		_ = p.Send(frame)
	}
	return nil
}

// trackSourcesSnapshotLocked copies the announced track maps of every
// publisher. room.mu must be held for writing so the copy is consistent with
// the participant registration it travels with.
func (r *Room) trackSourcesSnapshotLocked() map[string]map[string]string {
	if len(r.trackSources) == 0 {
		return nil
	}
	out := make(map[string]map[string]string, len(r.trackSources))
	for owner, sources := range r.trackSources {
		cp := make(map[string]string, len(sources))
		for k, v := range sources {
			cp[k] = v
		}
		out[owner] = cp
	}
	return out
}

// End explicitly finishes a live call.
func (h *Hub) End(ctx context.Context, room *Room, reason string) {
	room.mu.Lock()
	if room.closed {
		room.mu.Unlock()
		return
	}
	room.closed = true
	peers := make([]*sfuPeer, 0, len(room.peers))
	for _, p := range room.peers {
		peers = append(peers, p)
	}
	room.peers = map[string]*sfuPeer{}
	participants := make([]*Participant, 0, len(room.participants))
	for _, p := range room.participants {
		participants = append(participants, p)
	}
	room.participants = map[string]*Participant{}
	room.mu.Unlock()

	for _, peer := range peers {
		peer.close(reason)
	}
	room.removeAllRoutes()
	// Everyone still in the room is closed out with the same reason: the call
	// is over for them too, so their participant rows must not stay open.
	for _, p := range participants {
		h.persistClose(ctx, room, p.UserID, reason)
	}
	h.forget(room, reason)

	frame := newFrame(MsgCallEnded)
	frame.CallID = room.ID
	frame.Reason = reason
	for _, p := range participants {
		_ = p.Send(frame)
	}
}

// discardRoom drops a room that a join attempt created but nobody ever
// entered. Unlike forget it touches neither the store nor the notifier: the
// call was never announced, so EndCall / call.ended would be phantom events.
func (h *Hub) discardRoom(room *Room) {
	room.mu.Lock()
	if room.closed || len(room.participants) > 0 {
		room.mu.Unlock()
		return
	}
	room.closed = true
	room.mu.Unlock()

	h.mu.Lock()
	if h.byChat[room.ChatID] == room.ID {
		delete(h.byChat, room.ChatID)
	}
	delete(h.rooms, room.ID)
	h.mu.Unlock()
}

func (h *Hub) forget(room *Room, reason string) {
	h.mu.Lock()
	if current, ok := h.byChat[room.ChatID]; ok && current == room.ID {
		delete(h.byChat, room.ChatID)
	}
	delete(h.rooms, room.ID)
	h.mu.Unlock()

	callCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.store.EndCall(callCtx, room.ID, h.now(), reason); err != nil {
		h.cfg.Logger.Error("calls: end call", "call_id", room.ID, "error", err.Error())
	}
	if h.notifier != nil {
		h.notifier.CallEnded(callCtx, room.Record(), reason)
	}
}

// CloseAll tears every room down (process shutdown).
func (h *Hub) CloseAll(ctx context.Context, reason string) {
	h.mu.RLock()
	rooms := make([]*Room, 0, len(h.rooms))
	for _, r := range h.rooms {
		rooms = append(rooms, r)
	}
	h.mu.RUnlock()
	for _, r := range rooms {
		h.End(ctx, r, reason)
	}
}

// Rooms returns a snapshot of live call ids (diagnostics).
func (h *Hub) Rooms() []CallRecord {
	h.mu.RLock()
	defer h.mu.RUnlock()
	out := make([]CallRecord, 0, len(h.rooms))
	for _, r := range h.rooms {
		out = append(out, r.Record())
	}
	return out
}

func (h *Hub) persistParticipant(ctx context.Context, room *Room, p *Participant) {
	rec := ParticipantRecord{
		CallID:   room.ID,
		UserID:   p.UserID,
		DeviceID: p.DeviceID,
		Role:     p.Role,
		JoinedAt: p.JoinedAt,
	}
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.store.AddParticipant(writeCtx, rec); err != nil {
		h.cfg.Logger.Error("calls: persist participant",
			"call_id", room.ID, "user_id", p.UserID, "error", err.Error())
	}
}

func (h *Hub) persistClose(ctx context.Context, room *Room, userID, reason string) {
	writeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := h.store.CloseParticipant(writeCtx, room.ID, userID, "", h.now(), reason); err != nil {
		h.cfg.Logger.Error("calls: persist leave",
			"call_id", room.ID, "user_id", userID, "error", err.Error())
	}
}
