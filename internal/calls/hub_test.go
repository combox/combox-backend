package calls

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"
)

// --- fakes -----------------------------------------------------------------

type fakeSender struct {
	mu     sync.Mutex
	frames []Envelope
}

func newFakeSender() *fakeSender { return &fakeSender{} }

func (s *fakeSender) Send(env Envelope) error {
	s.mu.Lock()
	s.frames = append(s.frames, env)
	s.mu.Unlock()
	return nil
}

// waitFor returns (and consumes) the first frame of the given type.
func (s *fakeSender) waitFor(t *testing.T, typ string) Envelope {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s.mu.Lock()
		for i, env := range s.frames {
			if env.Type == typ {
				s.frames = append(s.frames[:i], s.frames[i+1:]...)
				s.mu.Unlock()
				return env
			}
		}
		s.mu.Unlock()
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timeout waiting for frame %s", typ)
	return Envelope{}
}

func (s *fakeSender) hasFrame(typ string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, env := range s.frames {
		if env.Type == typ {
			return true
		}
	}
	return false
}

type fakeMembers struct {
	mu     sync.Mutex
	allow  bool
	err    error
	calls  int
	lastIn string
}

func (m *fakeMembers) IsMember(_ context.Context, userID, chatID string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls++
	m.lastIn = userID + "|" + chatID
	if m.err != nil {
		return false, m.err
	}
	return m.allow, nil
}

type fakeStore struct {
	mu       sync.Mutex
	calls    map[string]CallRecord
	ended    map[string]string
	parts    []ParticipantRecord
	closed   []ParticipantRecord
	failNext bool
}

func newFakeStore() *fakeStore {
	return &fakeStore{calls: map[string]CallRecord{}, ended: map[string]string{}}
}

func (s *fakeStore) CreateCall(_ context.Context, call CallRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls[call.ID] = call
	return nil
}

func (s *fakeStore) GetCall(_ context.Context, callID string) (CallRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.calls[callID]; ok {
		return rec, nil
	}
	return CallRecord{}, ErrCallNotFound
}

func (s *fakeStore) GetActiveCallByChat(_ context.Context, chatID string) (CallRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.calls {
		if rec.ChatID == chatID && rec.EndedAt == nil {
			return rec, nil
		}
	}
	return CallRecord{}, ErrCallNotFound
}

func (s *fakeStore) EndCall(_ context.Context, callID string, _ time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ended[callID] = reason
	return nil
}

func (s *fakeStore) ListRecentCalls(context.Context, string, int) ([]CallRecord, error) {
	return nil, nil
}

func (s *fakeStore) AddParticipant(_ context.Context, rec ParticipantRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.failNext {
		s.failNext = false
		return errFake
	}
	s.parts = append(s.parts, rec)
	return nil
}

func (s *fakeStore) CloseParticipant(_ context.Context, callID, userID, deviceID string, _ time.Time, reason string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.parts {
		if s.parts[i].CallID == callID && s.parts[i].UserID == userID {
			s.parts[i].LeftAt = ptrTime(time.Now())
			s.parts[i].LeaveReason = ptrString(reason)
			s.closed = append(s.closed, s.parts[i])
		}
	}
	_ = deviceID
	return nil
}

func (s *fakeStore) ListParticipants(context.Context, string) ([]ParticipantRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]ParticipantRecord(nil), s.parts...), nil
}

var errFake = &Error{Code: "fake", Message: "fake store error"}

func ptrTime(t time.Time) *time.Time { return &t }
func ptrString(s string) *string     { return &s }

func newTestHub(t *testing.T, cfg Config, store Store, members MemberChecker) *Hub {
	t.Helper()
	return newHub(cfg.withDefaults(), store, members)
}

func join(t *testing.T, h *Hub, chatID, userID string, sender Sender, kind CallKind, role Role) (*JoinResult, error) {
	t.Helper()
	return h.Join(context.Background(), JoinRequest{
		Identity: Identity{UserID: userID, DeviceID: "dev-" + userID},
		ChatID:   chatID,
		Kind:     kind,
		Role:     role,
		Sender:   sender,
	})
}

// --- tests -----------------------------------------------------------------

func TestJoinCreatesRoomAndPersists(t *testing.T) {
	store := newFakeStore()
	h := newTestHub(t, Config{MeshLimit: 2}, store, &fakeMembers{allow: true})
	sender := newFakeSender()

	res, err := join(t, h, "chat-1", "u1", sender, KindGroup, "")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if !res.Created || res.Room.ChatID != "chat-1" || res.Room.Topology != TopologyMesh {
		t.Fatalf("unexpected result: %+v created=%v", res.Room, res.Created)
	}
	room, ok := h.ActiveCall("chat-1")
	if !ok || room.ID != res.Room.ID {
		t.Fatalf("active call not registered")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) != 1 || len(store.parts) != 1 {
		t.Fatalf("expected call + participant persisted, got %d/%d", len(store.calls), len(store.parts))
	}
}

func TestJoinChecksMembership(t *testing.T) {
	members := &fakeMembers{allow: false}
	h := newTestHub(t, Config{}, newFakeStore(), members)

	if _, err := join(t, h, "chat-1", "u1", newFakeSender(), KindGroup, ""); !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
	if members.calls != 1 || members.lastIn != "u1|chat-1" {
		t.Fatalf("membership was not consulted: calls=%d in=%q", members.calls, members.lastIn)
	}

	members.err = &Error{Code: "boom", Message: "chat backend down"}
	if _, err := join(t, h, "chat-2", "u1", newFakeSender(), KindGroup, ""); err == nil || Code(err) != "boom" {
		t.Fatalf("membership error must propagate, got %v", err)
	}
	if _, ok := h.ActiveCall("chat-2"); ok {
		t.Fatal("no room may be created when membership check fails")
	}
}

func TestMeshEscalatesToSFU(t *testing.T) {
	h := newTestHub(t, Config{MeshLimit: 2}, newFakeStore(), &fakeMembers{allow: true})
	a, b, c := newFakeSender(), newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if ra.Escalated {
		t.Fatal("first join must not escalate")
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}
	if ra.Room.topology() != TopologyMesh {
		t.Fatalf("two participants must stay mesh, got %s", ra.Room.topology())
	}

	rc, err := join(t, h, "chat-1", "u3", c, KindGroup, "")
	if err != nil {
		t.Fatalf("join u3: %v", err)
	}
	if !rc.Escalated || rc.Room.topology() != TopologySFU {
		t.Fatalf("third participant must escalate, escalated=%v topology=%s", rc.Escalated, rc.Room.topology())
	}
	escalate := a.waitFor(t, MsgEscalate)
	if escalate.Topology != TopologySFU || escalate.Reason != "mesh_limit" {
		t.Fatalf("unexpected escalate frame: %+v", escalate)
	}
	if !b.hasFrame(MsgEscalate) {
		t.Fatal("u2 must be notified about the escalation")
	}
	if c.hasFrame(MsgEscalate) {
		t.Fatal("the newcomer learns the topology from call.joined")
	}
}

func TestCallFull(t *testing.T) {
	h := newTestHub(t, Config{MaxParticipants: 2, MeshLimit: 5}, newFakeStore(), &fakeMembers{allow: true})
	if _, err := join(t, h, "chat-1", "u1", newFakeSender(), KindGroup, ""); err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", newFakeSender(), KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u3", newFakeSender(), KindGroup, ""); !errors.Is(err, ErrCallFull) {
		t.Fatalf("expected ErrCallFull, got %v", err)
	}
}

func TestBroadcastSinglePublisher(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})

	if _, err := join(t, h, "chat-1", "host", newFakeSender(), KindBroadcast, RolePublisher); err != nil {
		t.Fatalf("host join: %v", err)
	}
	if _, err := join(t, h, "chat-1", "guest", newFakeSender(), KindBroadcast, RolePublisher); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second publisher must be rejected, got %v", err)
	}
	res, err := join(t, h, "chat-1", "guest", newFakeSender(), KindBroadcast, RoleSubscriber)
	if err != nil {
		t.Fatalf("subscriber join: %v", err)
	}
	if res.Room.topology() != TopologyBroadcast {
		t.Fatalf("broadcast room topology = %s", res.Room.topology())
	}
	p, _ := res.Room.participant("guest")
	if p.Role != RoleSubscriber {
		t.Fatalf("unexpected role %s", p.Role)
	}
}

func TestRelayForwardsInMeshOnly(t *testing.T) {
	h := newTestHub(t, Config{MeshLimit: 2}, newFakeStore(), &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	env := Envelope{Type: MsgRelay, CallID: ra.Room.ID, TargetUserID: "u2", RelayKind: RelayOffer, SDP: "v=0"}
	if err := h.Relay(ra.Room, "u1", env); err != nil {
		t.Fatalf("relay: %v", err)
	}
	forwarded := b.waitFor(t, MsgRelay)
	if forwarded.FromUserID != "u1" || forwarded.SDP != "v=0" {
		t.Fatalf("unexpected relay frame: %+v", forwarded)
	}

	if err := h.Relay(ra.Room, "u1", Envelope{Type: MsgRelay, CallID: ra.Room.ID, TargetUserID: "ghost", RelayKind: RelayCandidate, Candidate: "c"}); err == nil {
		t.Fatal("relay to unknown target must fail")
	}
	if err := h.Relay(ra.Room, "ghost", env); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("expected ErrNotParticipant, got %v", err)
	}

	// Third participant forces SFU, the mesh relay must stop working.
	if _, err := join(t, h, "chat-1", "u3", newFakeSender(), KindGroup, ""); err != nil {
		t.Fatalf("join u3: %v", err)
	}
	if err := h.Relay(ra.Room, "u1", env); !errors.Is(err, ErrTopology) {
		t.Fatalf("expected ErrTopology after escalation, got %v", err)
	}
}

func TestLeaveBroadcastsAndEndsEmptyCall(t *testing.T) {
	store := newFakeStore()
	h := newTestHub(t, Config{}, store, &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	h.Leave(context.Background(), ra.Room, "u1", a, "left")
	left := b.waitFor(t, MsgPeerLeft)
	if left.Reason != "left" || len(left.Participants) != 1 || left.Participants[0].UserID != "u1" {
		t.Fatalf("unexpected peer_left: %+v", left)
	}
	// A non-broadcast call is over for the remaining side as well.
	ended := b.waitFor(t, MsgCallEnded)
	if ended.CallID != ra.Room.ID || ended.Reason != "left" {
		t.Fatalf("unexpected call_ended: %+v", ended)
	}
	if _, ok := h.ActiveCall("chat-1"); ok {
		t.Fatal("call must end when one participant remains")
	}

	h.Leave(context.Background(), ra.Room, "u2", b, "left")
	if _, ok := h.ActiveCall("chat-1"); ok {
		t.Fatal("empty call must be forgotten")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.ended[ra.Room.ID] != "left" {
		t.Fatalf("call was not ended in the store: %v", store.ended)
	}
	if len(store.closed) != 2 {
		t.Fatalf("both participants must be closed, got %d", len(store.closed))
	}
}

func TestStaleConnectionCannotKickReconnect(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	oldConn, newConn := newFakeSender(), newFakeSender()

	res, err := join(t, h, "chat-1", "u1", oldConn, KindGroup, "")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	res2, err := join(t, h, "chat-1", "u1", newConn, KindGroup, "")
	if err != nil {
		t.Fatalf("reconnect: %v", err)
	}
	if res2.Participant != res.Participant {
		t.Fatal("reconnect must keep the same participant slot")
	}

	h.Leave(context.Background(), res.Room, "u1", oldConn, "disconnected")
	if !res.Room.isParticipant("u1") {
		t.Fatal("stale connection must not remove the participant")
	}

	h.Leave(context.Background(), res.Room, "u1", newConn, "disconnected")
	if res.Room.isParticipant("u1") {
		t.Fatal("the owning connection must be able to leave")
	}
}

func TestUpdateMediaRelaysToPeers(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	state := MediaState{Mic: false, Camera: true, Screen: false, Speaking: true}
	if err := h.UpdateMedia(ra.Room, "u1", state); err != nil {
		t.Fatalf("update media: %v", err)
	}
	frame := b.waitFor(t, MsgMedia)
	if frame.Media == nil || *frame.Media != state || frame.Participants[0].UserID != "u1" {
		t.Fatalf("unexpected media frame: %+v", frame.Media)
	}
	if err := h.UpdateMedia(ra.Room, "ghost", state); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("expected ErrNotParticipant, got %v", err)
	}
}

func TestEndNotifiesAndPersists(t *testing.T) {
	store := newFakeStore()
	h := newTestHub(t, Config{}, store, &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	h.End(context.Background(), ra.Room, "host_ended")
	for _, s := range []*fakeSender{a, b} {
		frame := s.waitFor(t, MsgCallEnded)
		if frame.Reason != "host_ended" {
			t.Fatalf("unexpected end reason: %q", frame.Reason)
		}
	}
	if _, ok := h.ActiveCall("chat-1"); ok {
		t.Fatal("ended call must be gone")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.ended[ra.Room.ID] != "host_ended" {
		t.Fatalf("store missing end record: %v", store.ended)
	}
}

func TestStoreFailureDoesNotBreakJoin(t *testing.T) {
	store := newFakeStore()
	h := newTestHub(t, Config{}, store, &fakeMembers{allow: true})
	store.failNext = true

	res, err := join(t, h, "chat-1", "u1", newFakeSender(), KindGroup, "")
	if err != nil {
		t.Fatalf("join must survive store errors: %v", err)
	}
	if !res.Created {
		t.Fatal("expected a created room")
	}
}

type fakeNotifier struct {
	mu      sync.Mutex
	started []CallRecord
	ended   []CallRecord
	reasons []string
}

func (f *fakeNotifier) CallStarted(_ context.Context, call CallRecord) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.started = append(f.started, call)
}

func (f *fakeNotifier) CallEnded(_ context.Context, call CallRecord, reason string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ended = append(f.ended, call)
	f.reasons = append(f.reasons, reason)
}

func TestNotifierReportsCallLifecycle(t *testing.T) {
	notify := &fakeNotifier{}
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	h.notifier = notify

	res, err := join(t, h, "chat-1", "u1", newFakeSender(), KindGroup, "")
	if err != nil {
		t.Fatalf("join: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", newFakeSender(), KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	notify.mu.Lock()
	started := len(notify.started)
	notify.mu.Unlock()
	if started != 1 {
		t.Fatalf("expected exactly one call.started, got %d", started)
	}

	h.Leave(context.Background(), res.Room, "u1", nil, "left")
	h.Leave(context.Background(), res.Room, "u2", nil, "disconnected")

	notify.mu.Lock()
	defer notify.mu.Unlock()
	if len(notify.ended) != 1 {
		t.Fatalf("expected exactly one call.ended, got %d", len(notify.ended))
	}
	// u1 leaving makes u2 the last participant, so the call ends right there.
	if notify.reasons[0] != "left" || notify.ended[0].ID != res.Room.ID {
		t.Fatalf("unexpected end notification: %+v reasons=%v", notify.ended[0], notify.reasons)
	}
}

// fakeStreamMembers adds the optional live stream publisher check.
type fakeStreamMembers struct {
	*fakeMembers
	allowed map[string]bool
}

func (f *fakeStreamMembers) CanPublishStream(_ context.Context, userID, chatID string) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.allowed[userID+"|"+chatID], nil
}

func TestBroadcastPublisherRequiresStreamPermission(t *testing.T) {
	members := &fakeStreamMembers{
		fakeMembers: &fakeMembers{allow: true},
		allowed:     map[string]bool{"owner|chat-1": true},
	}
	h := newTestHub(t, Config{}, newFakeStore(), members)

	if _, err := join(t, h, "chat-1", "owner", newFakeSender(), KindBroadcast, RolePublisher); err != nil {
		t.Fatalf("owner must publish: %v", err)
	}
	if _, err := join(t, h, "chat-1", "intruder", newFakeSender(), KindBroadcast, RolePublisher); !errors.Is(err, ErrForbidden) {
		t.Fatalf("second publisher must be rejected, got %v", err)
	}
	if _, err := join(t, h, "chat-1", "viewer", newFakeSender(), KindBroadcast, RoleSubscriber); err != nil {
		t.Fatalf("subscriber must join: %v", err)
	}
	if _, err := join(t, h, "chat-2", "intruder", newFakeSender(), KindBroadcast, RolePublisher); !errors.Is(err, ErrForbidden) {
		t.Fatalf("publisher without permission must be rejected, got %v", err)
	}
	// Member checkers without the optional capability keep publishing open.
	plain := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	if _, err := join(t, plain, "chat-1", "anyone", newFakeSender(), KindBroadcast, RolePublisher); err != nil {
		t.Fatalf("plain member checker must not restrict publishers: %v", err)
	}
}

func TestRejectedStreamJoinLeavesNoRoomBehind(t *testing.T) {
	members := &fakeStreamMembers{
		fakeMembers: &fakeMembers{allow: true},
		allowed:     map[string]bool{"owner|chat-1": true},
	}
	store := newFakeStore()
	h := newTestHub(t, Config{}, store, members)
	notify := &fakeNotifier{}
	h.notifier = notify

	if _, err := join(t, h, "chat-1", "intruder", newFakeSender(), KindBroadcast, RolePublisher); !errors.Is(err, ErrForbidden) {
		t.Fatalf("publisher without permission must be rejected, got %v", err)
	}
	if _, ok := h.ActiveCall("chat-1"); ok {
		t.Fatal("a rejected join must not leave an active room behind")
	}

	// The channel owner can still start the stream afterwards, and it is a
	// fresh room rather than a shadowed broadcast nobody may publish into.
	res, err := join(t, h, "chat-1", "owner", newFakeSender(), KindBroadcast, RolePublisher)
	if err != nil {
		t.Fatalf("owner must publish: %v", err)
	}
	if !res.Created {
		t.Fatal("the owner's join must create the room")
	}
	if role := res.Participant.Role; role != RolePublisher {
		t.Fatalf("owner role = %q, want publisher", role)
	}
	if res.Room.Kind != KindBroadcast {
		t.Fatalf("room kind = %q, want broadcast", res.Room.Kind)
	}

	// Nothing was ever created or ended for the rejected attempt.
	store.mu.Lock()
	defer store.mu.Unlock()
	if len(store.calls) != 1 || len(store.ended) != 0 {
		t.Fatalf("store calls=%d ended=%d, want a single started call", len(store.calls), len(store.ended))
	}
	notify.mu.Lock()
	defer notify.mu.Unlock()
	if len(notify.started) != 1 || len(notify.ended) != 0 {
		t.Fatalf("notifier started=%d ended=%d, want exactly one started", len(notify.started), len(notify.ended))
	}
}

func TestTrackSourcesRelayedToPeers(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	ra, err := join(t, h, "chat-1", "u1", a, KindGroup, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindGroup, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	if err := h.SetTrackSources(ra.Room, "u1", map[string]string{"TrackA": "screen", "TrackB": "nonsense"}); err != nil {
		t.Fatalf("set track sources: %v", err)
	}
	frame := b.waitFor(t, MsgTracks)
	if frame.FromUserID != "u1" || frame.CallID != ra.Room.ID {
		t.Fatalf("unexpected tracks frame: %+v", frame)
	}
	if frame.Tracks["tracka"] != "screen" || len(frame.Tracks) != 1 {
		t.Fatalf("expected a single normalized mapping, got %+v", frame.Tracks)
	}
	if a.hasFrame(MsgTracks) {
		t.Fatal("the publisher must not receive its own mapping back")
	}
	if src := ra.Room.sourceFor("u1", "TrackA", 0); src != "screen" {
		t.Fatalf("sourceFor = %q, want screen", src)
	}

	if err := h.SetTrackSources(ra.Room, "ghost", map[string]string{"t": "screen"}); !errors.Is(err, ErrNotParticipant) {
		t.Fatalf("expected ErrNotParticipant, got %v", err)
	}
}

func TestDeclineEndsUnansweredCall(t *testing.T) {
	store := newFakeStore()
	h := newTestHub(t, Config{}, store, &fakeMembers{allow: true})
	caller := newFakeSender()

	res, err := join(t, h, "chat-1", "u1", caller, KindP2P, "")
	if err != nil {
		t.Fatalf("caller join: %v", err)
	}
	if err := h.Decline(context.Background(), "u2", res.Room.ID); err != nil {
		t.Fatalf("decline: %v", err)
	}
	frame := caller.waitFor(t, MsgCallEnded)
	if frame.Reason != ReasonDeclined || frame.CallID != res.Room.ID {
		t.Fatalf("unexpected call_ended: %+v", frame)
	}
	if _, ok := h.ActiveCall("chat-1"); ok {
		t.Fatal("declined call must be gone")
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.ended[res.Room.ID] != ReasonDeclined {
		t.Fatalf("store end reason = %q, want %q", store.ended[res.Room.ID], ReasonDeclined)
	}
}

func TestDeclineIsNoopForLiveCall(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	a, b := newFakeSender(), newFakeSender()

	res, err := join(t, h, "chat-1", "u1", a, KindP2P, "")
	if err != nil {
		t.Fatalf("join u1: %v", err)
	}
	if _, err := join(t, h, "chat-1", "u2", b, KindP2P, ""); err != nil {
		t.Fatalf("join u2: %v", err)
	}

	if err := h.Decline(context.Background(), "u3", res.Room.ID); err != nil {
		t.Fatalf("decline of a live call must be a no-op, got %v", err)
	}
	if err := h.Decline(context.Background(), "u2", res.Room.ID); err != nil {
		t.Fatalf("a participant decline must be a no-op, got %v", err)
	}
	if a.hasFrame(MsgCallEnded) || b.hasFrame(MsgCallEnded) {
		t.Fatal("a live call must not end because somebody declined")
	}
	if !res.Room.isParticipant("u1") || !res.Room.isParticipant("u2") {
		t.Fatal("participants must stay in the call")
	}
}

func TestDeclineValidatesCallAndMembership(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	if _, err := join(t, h, "chat-1", "u1", newFakeSender(), KindP2P, ""); err != nil {
		t.Fatalf("join: %v", err)
	}
	if err := h.Decline(context.Background(), "u2", "nope"); !errors.Is(err, ErrCallNotFound) {
		t.Fatalf("expected ErrCallNotFound, got %v", err)
	}
	if err := h.Decline(context.Background(), "u2", "  "); !errors.Is(err, ErrInvalidMessage) {
		t.Fatalf("expected ErrInvalidMessage, got %v", err)
	}

	restricted := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: false})
	if _, err := join(t, restricted, "chat-9", "u1", newFakeSender(), KindP2P, ""); !errors.Is(err, ErrNotMember) {
		t.Fatalf("expected ErrNotMember, got %v", err)
	}
}

func TestDeclineSkipsBroadcast(t *testing.T) {
	h := newTestHub(t, Config{}, newFakeStore(), &fakeMembers{allow: true})
	host := newFakeSender()

	res, err := join(t, h, "chat-1", "host", host, KindBroadcast, RolePublisher)
	if err != nil {
		t.Fatalf("host join: %v", err)
	}
	if err := h.Decline(context.Background(), "viewer", res.Room.ID); err != nil {
		t.Fatalf("broadcast decline must be a no-op, got %v", err)
	}
	if host.hasFrame(MsgCallEnded) {
		t.Fatal("a broadcast must not end because a viewer declined")
	}
	if !res.Room.isParticipant("host") {
		t.Fatal("host must stay in the broadcast")
	}
}
