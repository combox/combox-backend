package calls

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

// WebSocket control frames (mirrors the gorilla/websocket constants without
// importing the transport package here).
const (
	wsTextMessage = 1
	wsPingMessage = 9

	readIdleTimeout = 60 * time.Second
	writeTimeout    = 10 * time.Second
	pingInterval    = 25 * time.Second
)

// SignalConn is the transport contract of the calls signaling channel. The
// HTTP layer hands us an upgraded *websocket.Conn which satisfies it.
type SignalConn interface {
	ReadMessage() (messageType int, data []byte, err error)
	WriteMessage(messageType int, data []byte) error
	WriteControl(messageType int, data []byte, deadline time.Time) error
	SetReadDeadline(t time.Time) error
	SetWriteDeadline(t time.Time) error
	SetPongHandler(h func(appData string) error)
	Close() error
}

// session is one authenticated signaling connection.
type session struct {
	svc     *Service
	ident   Identity
	conn    SignalConn
	writeMu sync.Mutex

	mu     sync.RWMutex
	room   *Room
	closed bool

	// gate buffers outbound frames while a join is in flight so a participant
	// never receives media/tracks/relay frames before its own `call.joined`.
	gateMu sync.Mutex
	gated  bool
	queued []Envelope
}

func newSession(svc *Service, ident Identity, conn SignalConn) *session {
	return &session{svc: svc, ident: ident, conn: conn}
}

// maxQueuedFrames caps what a join gate buffers before frames are dropped.
const maxQueuedFrames = 128

// Send implements Sender; every participant routes frames through here.
func (s *session) Send(env Envelope) error {
	s.gateMu.Lock()
	if s.gated {
		if len(s.queued) < maxQueuedFrames {
			s.queued = append(s.queued, env)
		}
		s.gateMu.Unlock()
		return nil
	}
	s.gateMu.Unlock()
	return s.writeFrame(env)
}

// beginGate starts buffering; endGate flushes the buffer once `call.joined`
// has been written, dropGate discards it (join failed or connection died).
func (s *session) beginGate() {
	s.gateMu.Lock()
	s.gated = true
	s.queued = nil
	s.gateMu.Unlock()
}

func (s *session) endGate() error {
	s.gateMu.Lock()
	s.gated = false
	queued := s.queued
	s.queued = nil
	s.gateMu.Unlock()
	for _, env := range queued {
		if err := s.writeFrame(env); err != nil {
			return err
		}
	}
	return nil
}

func (s *session) dropGate() {
	s.gateMu.Lock()
	s.gated = false
	s.queued = nil
	s.gateMu.Unlock()
}

func (s *session) writeFrame(env Envelope) error {
	data, err := json.Marshal(env)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	s.mu.RLock()
	closed := s.closed
	s.mu.RUnlock()
	if closed {
		return errSessionClosed
	}
	if err := s.conn.SetWriteDeadline(time.Now().Add(writeTimeout)); err != nil {
		return err
	}
	if err := s.conn.WriteMessage(wsTextMessage, data); err != nil {
		return err
	}
	return nil
}

var errSessionClosed = &Error{Code: "session_closed", Message: "signaling connection is closed"}

// run drives read loop + keepalive until the connection dies.
func (s *session) run(ctx context.Context) {
	defer s.cleanup()

	_ = s.conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
	s.conn.SetPongHandler(func(string) error {
		return s.conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
	})

	readDone := make(chan struct{})
	go func() {
		defer close(readDone)
		for {
			_, data, err := s.conn.ReadMessage()
			if err != nil {
				return
			}
			_ = s.conn.SetReadDeadline(time.Now().Add(readIdleTimeout))
			env, err := ParseEnvelope(data)
			if err != nil {
				_ = s.Send(errorFrame("", err))
				continue
			}
			s.dispatch(ctx, env)
		}
	}()

	ping := time.NewTicker(pingInterval)
	defer ping.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-readDone:
			return
		case <-ping.C:
			s.writeMu.Lock()
			_ = s.conn.WriteControl(wsPingMessage, []byte("ping"), time.Now().Add(5*time.Second))
			s.writeMu.Unlock()
		}
	}
}

func (s *session) currentRoom() *Room {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.room
}

func (s *session) setRoom(room *Room) {
	s.mu.Lock()
	s.room = room
	s.mu.Unlock()
}

// cleanup releases the call slot owned by this connection.
func (s *session) cleanup() {
	s.dropGate()
	s.mu.Lock()
	s.closed = true
	room := s.room
	s.room = nil
	s.mu.Unlock()
	if room != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		s.svc.hub.Leave(ctx, room, s.ident.UserID, s, "disconnected")
	}
	_ = s.conn.Close()
}

func (s *session) fail(reqID string, err error) {
	_ = s.Send(errorFrame(reqID, err))
}

func (s *session) dispatch(ctx context.Context, env Envelope) {
	switch env.Type {
	case MsgJoin:
		s.handleJoin(ctx, env)
	case MsgLeave:
		s.handleLeave(ctx, env)
	case MsgDecline:
		s.handleDecline(ctx, env)
	case MsgOffer:
		s.handleOffer(env)
	case MsgAnswer:
		s.handleAnswer(env)
	case MsgCandidate:
		s.handleCandidate(env)
	case MsgRelay:
		s.handleRelay(env)
	case MsgMedia:
		s.handleMedia(env)
	case MsgTracks:
		s.handleTracks(env)
	case MsgPing:
		pong := newFrame(MsgPong)
		pong.ID = env.ID
		pong.CallID = env.CallID
		_ = s.Send(pong)
	}
}

func (s *session) handleJoin(ctx context.Context, env Envelope) {
	if s.currentRoom() != nil {
		s.fail(env.ID, ErrDuplicateJoin)
		return
	}
	role := env.Role
	s.beginGate()
	res, err := s.svc.hub.Join(ctx, JoinRequest{
		Identity: Identity{UserID: s.ident.UserID, DeviceID: env.DeviceID},
		CallID:   env.CallID,
		ChatID:   env.ChatID,
		Kind:     env.Kind,
		Role:     role,
		E2EE:     env.E2EE,
		Sender:   s,
	})
	if err != nil {
		s.dropGate()
		s.fail(env.ID, err)
		return
	}
	room := res.Room
	s.setRoom(room)
	info := room.describe()

	joined := newFrame(MsgJoined)
	joined.ID = env.ID
	joined.CallID = info.ID
	joined.ChatID = info.ChatID
	joined.Kind = info.Kind
	joined.Topology = info.Topology
	joined.Role = res.Participant.Role
	joined.E2EE = info.E2EE
	joined.MeshLimit = info.MeshLimit
	joined.Participants = room.snapshotParticipants()
	joined.ICEServers = s.svc.ICEServers(s.ident.UserID)
	if err := s.writeFrame(joined); err != nil {
		s.dropGate()
		s.fail(env.ID, err)
		return
	}
	// Track -> source maps announced before this join are replayed so the
	// newcomer can label already flowing streams (screen vs camera). The maps
	// were captured with the registration, so a concurrent announcement is
	// either in this snapshot or relayed to us as a broadcast, never both.
	for owner, tracks := range res.TrackSources {
		if owner == s.ident.UserID {
			continue
		}
		notice := newFrame(MsgTracks)
		notice.CallID = info.ID
		notice.FromUserID = owner
		notice.Tracks = tracks
		_ = s.Send(notice)
	}
	if err := s.endGate(); err != nil {
		return
	}
	if info.Topology != TopologyMesh {
		if err := s.svc.hub.Activate(room, s.ident.UserID); err != nil {
			s.fail("", err)
		}
	}
}

func (s *session) handleLeave(ctx context.Context, env Envelope) {
	room := s.currentRoom()
	if room == nil {
		s.fail(env.ID, ErrNotParticipant)
		return
	}
	s.setRoom(nil)
	s.svc.hub.Leave(ctx, room, s.ident.UserID, s, "left")
	ack := newFrame(MsgLeave)
	ack.ID = env.ID
	ack.CallID = room.ID
	_ = s.Send(ack)
}

// handleDecline lets a callee reject a ringing call without joining it, so the
// caller learns immediately instead of waiting for the no-answer timeout.
func (s *session) handleDecline(ctx context.Context, env Envelope) {
	if s.currentRoom() != nil {
		// Already inside a call: declining is not applicable.
		return
	}
	if err := s.svc.hub.Decline(ctx, s.ident.UserID, env.CallID); err != nil {
		s.fail(env.ID, err)
		return
	}
	ack := newFrame(MsgDecline)
	ack.ID = env.ID
	ack.CallID = env.CallID
	_ = s.Send(ack)
}

func (s *session) peerFor(env Envelope) (*Room, *sfuPeer, error) {
	room := s.currentRoom()
	if room == nil || (env.CallID != "" && env.CallID != room.ID) {
		return nil, nil, ErrNotParticipant
	}
	peer, err := s.svc.hub.PeerFor(room, s.ident.UserID)
	if err != nil {
		return nil, nil, err
	}
	return room, peer, nil
}

func (s *session) handleOffer(env Envelope) {
	room, peer, err := s.peerFor(env)
	if err != nil {
		s.fail(env.ID, err)
		return
	}
	if room.topology() == TopologyMesh {
		s.fail(env.ID, ErrTopology)
		return
	}
	// The answer is sent by the peer itself (it carries the request id), so
	// there is no separate acknowledgement here.
	if err := peer.handleOffer(env.SDP, env.ID); err != nil {
		s.fail(env.ID, err)
	}
}

func (s *session) handleAnswer(env Envelope) {
	_, peer, err := s.peerFor(env)
	if err != nil {
		s.fail(env.ID, err)
		return
	}
	if err := peer.handleAnswer(env.SDP); err != nil {
		s.fail(env.ID, err)
	}
}

func (s *session) handleCandidate(env Envelope) {
	room := s.currentRoom()
	if room == nil {
		s.fail(env.ID, ErrNotParticipant)
		return
	}
	if room.topology() == TopologyMesh {
		// Mesh candidates travel through `call.relay`.
		return
	}
	_, peer, err := s.peerFor(env)
	if err != nil {
		s.fail(env.ID, err)
		return
	}
	if err := peer.handleCandidate(env.Candidate, env.SDPMid, env.SDPMLineIndex); err != nil {
		s.fail(env.ID, err)
	}
}

func (s *session) handleRelay(env Envelope) {
	room := s.currentRoom()
	if room == nil {
		s.fail(env.ID, ErrNotParticipant)
		return
	}
	if err := s.svc.hub.Relay(room, s.ident.UserID, env); err != nil {
		s.fail(env.ID, err)
		return
	}
	if env.ID != "" {
		ack := newFrame(MsgRelay)
		ack.ID = env.ID
		ack.CallID = room.ID
		ack.Reason = "ok"
		_ = s.Send(ack)
	}
}

func (s *session) handleMedia(env Envelope) {
	room := s.currentRoom()
	if room == nil {
		s.fail(env.ID, ErrNotParticipant)
		return
	}
	if err := s.svc.hub.UpdateMedia(room, s.ident.UserID, *env.Media); err != nil {
		s.fail(env.ID, err)
		return
	}
	if env.ID != "" {
		ack := newFrame(MsgMedia)
		ack.ID = env.ID
		ack.CallID = room.ID
		ack.Reason = "ok"
		_ = s.Send(ack)
	}
}

func (s *session) handleTracks(env Envelope) {
	room := s.currentRoom()
	if room == nil {
		s.fail(env.ID, ErrNotParticipant)
		return
	}
	sources := map[string]string{}
	for id, source := range env.Tracks {
		if src := normalizeTrackSource(source); src != "" {
			sources[id] = src
		}
	}
	if err := s.svc.hub.SetTrackSources(room, s.ident.UserID, sources); err != nil {
		s.fail(env.ID, err)
	}
}
