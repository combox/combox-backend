package calls

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"
)

// pipeConn is an in-memory SignalConn: frames written by the server land in
// out, frames the test writes to in are read by the session.
type pipeConn struct {
	in     chan []byte
	out    chan []byte
	closed chan struct{}
	once   sync.Once
}

func newPipeConn() *pipeConn {
	return &pipeConn{
		in:     make(chan []byte, 32),
		out:    make(chan []byte, 32),
		closed: make(chan struct{}),
	}
}

func (c *pipeConn) ReadMessage() (int, []byte, error) {
	select {
	case data := <-c.in:
		return wsTextMessage, data, nil
	case <-c.closed:
		return 0, nil, errors.New("connection closed")
	}
}

func (c *pipeConn) WriteMessage(_ int, data []byte) error {
	select {
	case c.out <- append([]byte(nil), data...):
		return nil
	case <-c.closed:
		return errors.New("connection closed")
	}
}

func (c *pipeConn) WriteControl(int, []byte, time.Time) error { return nil }
func (c *pipeConn) SetReadDeadline(time.Time) error           { return nil }
func (c *pipeConn) SetWriteDeadline(time.Time) error          { return nil }
func (c *pipeConn) SetPongHandler(func(string) error)         {}
func (c *pipeConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *pipeConn) clientSend(t *testing.T, env Envelope) {
	t.Helper()
	raw, err := json.Marshal(env)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	select {
	case c.in <- raw:
	case <-time.After(2 * time.Second):
		t.Fatal("client write blocked")
	}
}

func (c *pipeConn) clientRecv(t *testing.T) Envelope {
	t.Helper()
	select {
	case raw := <-c.out:
		var env Envelope
		if err := json.Unmarshal(raw, &env); err != nil {
			t.Fatalf("decode frame %q: %v", raw, err)
		}
		return env
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for a server frame")
	}
	return Envelope{}
}

func (c *pipeConn) clientRecvType(t *testing.T, typ string) Envelope {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		env := c.clientRecv(t)
		if env.Type == typ {
			return env
		}
	}
	t.Fatalf("timeout waiting for frame %s", typ)
	return Envelope{}
}

// startSession wires a signaling connection into the service and returns it.
func startSession(t *testing.T, svc *Service, userID string) *pipeConn {
	t.Helper()
	conn := newPipeConn()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go svc.HandleConn(ctx, Identity{UserID: userID, DeviceID: "dev-" + userID}, conn)
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func newTestService(t *testing.T, cfg Config, members MemberChecker) *Service {
	t.Helper()
	if cfg.STUNURLs == nil {
		cfg.STUNURLs = []string{"stun:stun.example:3478"}
	}
	svc, err := New(cfg, noopStore{}, members)
	if err != nil {
		t.Fatalf("new service: %v", err)
	}
	return svc
}

func TestSessionJoinLeaveAndPing(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true, MeshLimit: 3}, &fakeMembers{allow: true})
	conn := startSession(t, svc, "u1")

	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup, DeviceID: "dev-1"})
	joined := conn.clientRecvType(t, MsgJoined)
	if joined.ChatID != "chat-1" || joined.Topology != TopologyMesh {
		t.Fatalf("unexpected joined frame: %+v", joined)
	}
	if len(joined.Participants) != 1 || joined.Participants[0].UserID != "u1" {
		t.Fatalf("unexpected participants: %+v", joined.Participants)
	}
	if len(joined.ICEServers) != 1 || joined.ICEServers[0].URLs[0] != "stun:stun.example:3478" {
		t.Fatalf("joined frame must carry ICE servers: %+v", joined.ICEServers)
	}
	if joined.MeshLimit != 3 {
		t.Fatalf("mesh limit = %d", joined.MeshLimit)
	}

	conn.clientSend(t, Envelope{Type: MsgPing, ID: "p1"})
	if pong := conn.clientRecvType(t, MsgPong); pong.ID != "p1" {
		t.Fatalf("pong id = %q", pong.ID)
	}

	conn.clientSend(t, Envelope{Type: MsgLeave, CallID: joined.CallID})
	ack := conn.clientRecvType(t, MsgLeave)
	if ack.CallID != joined.CallID {
		t.Fatalf("leave ack call id = %q", ack.CallID)
	}

	// Leaving as the last participant ends the call, so the next join starts
	// a brand new one.
	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	if again := conn.clientRecvType(t, MsgJoined); again.CallID == joined.CallID {
		t.Fatalf("a new call must start after the previous one ended, got %q", again.CallID)
	}
}

func TestSessionRejectsNonMember(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: false})
	conn := startSession(t, svc, "u1")

	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	errFrame := conn.clientRecvType(t, MsgError)
	if errFrame.Code != ErrNotMember.Code {
		t.Fatalf("error code = %q, want %q", errFrame.Code, ErrNotMember.Code)
	}
}

func TestSessionDuplicateJoin(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	conn := startSession(t, svc, "u1")

	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	conn.clientRecvType(t, MsgJoined)

	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	errFrame := conn.clientRecvType(t, MsgError)
	if errFrame.Code != ErrDuplicateJoin.Code {
		t.Fatalf("error code = %q, want %q", errFrame.Code, ErrDuplicateJoin.Code)
	}
}

func TestSessionMeshRejectsServerSignaling(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	conn := startSession(t, svc, "u1")

	conn.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	joined := conn.clientRecvType(t, MsgJoined)

	conn.clientSend(t, Envelope{Type: MsgOffer, CallID: joined.CallID, SDP: "v=0"})
	errFrame := conn.clientRecvType(t, MsgError)
	if errFrame.Code != ErrTopology.Code {
		t.Fatalf("error code = %q, want %q", errFrame.Code, ErrTopology.Code)
	}
}

func TestSessionRelaysBetweenMembers(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	alice := startSession(t, svc, "u1")
	bob := startSession(t, svc, "u2")

	alice.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	joined := alice.clientRecvType(t, MsgJoined)

	// Bob must join before he can be a relay target.
	bob.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	bob.clientRecvType(t, MsgJoined)
	alice.clientRecvType(t, MsgPeerJoined)

	alice.clientSend(t, Envelope{
		Type: MsgRelay, ID: "r1", CallID: joined.CallID,
		TargetUserID: "u2", RelayKind: RelayOffer, SDP: "v=0",
	})
	forwarded := bob.clientRecvType(t, MsgRelay)
	if forwarded.FromUserID != "u1" || forwarded.SDP != "v=0" {
		t.Fatalf("unexpected relay frame: %+v", forwarded)
	}

	ack := alice.clientRecvType(t, MsgRelay)
	if ack.ID != "r1" || ack.Reason != "ok" {
		t.Fatalf("unexpected relay ack: %+v", ack)
	}
}

func TestSessionDisconnectRemovesParticipant(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	alice := startSession(t, svc, "u1")
	bob := startSession(t, svc, "u2")

	alice.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	alice.clientRecvType(t, MsgJoined)
	bob.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	bob.clientRecvType(t, MsgJoined)
	alice.clientRecvType(t, MsgPeerJoined)

	_ = alice.Close()

	left := bob.clientRecvType(t, MsgPeerLeft)
	if left.Reason != "disconnected" || len(left.Participants) != 1 || left.Participants[0].UserID != "u1" {
		t.Fatalf("unexpected peer_left: %+v", left)
	}
	// Alice was the only other participant: the call ends for bob as well.
	ended := bob.clientRecvType(t, MsgCallEnded)
	if ended.Reason != "disconnected" {
		t.Fatalf("unexpected call_ended: %+v", ended)
	}
	if _, _, ok := svc.ActiveCall("chat-1"); ok {
		t.Fatal("bob must no longer be in the call")
	}
}

func TestSessionDisabledServiceClosesConnection(t *testing.T) {
	svc := newTestService(t, Config{Enabled: false}, &fakeMembers{allow: true})
	conn := startSession(t, svc, "u1")

	select {
	case <-conn.closed:
	case <-time.After(2 * time.Second):
		t.Fatal("disabled service must close the connection")
	}
}

func TestSessionDeclineEndsCallForCaller(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	caller := startSession(t, svc, "u1")
	callee := startSession(t, svc, "u2")

	caller.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindP2P})
	joined := caller.clientRecvType(t, MsgJoined)

	callee.clientSend(t, Envelope{Type: MsgDecline, ID: "d1", CallID: joined.CallID})
	ack := callee.clientRecvType(t, MsgDecline)
	if ack.ID != "d1" || ack.CallID != joined.CallID {
		t.Fatalf("unexpected decline ack: %+v", ack)
	}

	ended := caller.clientRecvType(t, MsgCallEnded)
	if ended.CallID != joined.CallID || ended.Reason != ReasonDeclined {
		t.Fatalf("unexpected call_ended: %+v", ended)
	}
	if _, _, ok := svc.ActiveCall("chat-1"); ok {
		t.Fatal("declined call must be closed")
	}
}

func TestSessionDeclineWithoutCallIDIsRejected(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	conn := startSession(t, svc, "u1")

	conn.clientSend(t, Envelope{Type: MsgDecline})
	if errFrame := conn.clientRecvType(t, MsgError); errFrame.Code != ErrInvalidMessage.Code {
		t.Fatalf("expected %s, got %q", ErrInvalidMessage.Code, errFrame.Code)
	}
}

func TestSessionRelaysTrackSourcesToPeers(t *testing.T) {
	svc := newTestService(t, Config{Enabled: true}, &fakeMembers{allow: true})
	alice := startSession(t, svc, "u1")
	bob := startSession(t, svc, "u2")

	alice.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	joined := alice.clientRecvType(t, MsgJoined)

	alice.clientSend(t, Envelope{Type: MsgTracks, CallID: joined.CallID, Tracks: map[string]string{"t-1": "screen"}})

	bob.clientSend(t, Envelope{Type: MsgJoin, ChatID: "chat-1", Kind: KindGroup})
	bob.clientRecvType(t, MsgJoined)
	// The replay of the already announced map follows the join reply.
	replay := bob.clientRecvType(t, MsgTracks)
	if replay.FromUserID != "u1" || replay.Tracks["t-1"] != "screen" {
		t.Fatalf("unexpected tracks replay: %+v", replay)
	}

	// A late announcement reaches the already connected peer as well.
	alice.clientSend(t, Envelope{Type: MsgTracks, CallID: joined.CallID, Tracks: map[string]string{"t-2": "camera"}})
	relayed := bob.clientRecvType(t, MsgTracks)
	if relayed.FromUserID != "u1" || relayed.Tracks["t-2"] != "camera" {
		t.Fatalf("unexpected relayed tracks: %+v", relayed)
	}
}
