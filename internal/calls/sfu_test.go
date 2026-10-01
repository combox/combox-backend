package calls

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// loopbackPeer is a browser-like client: a real pion PeerConnection driven by
// the signaling frames the server writes to the pipe connection.
type loopbackPeer struct {
	t    *testing.T
	name string
	conn *pipeConn
	pc   *webrtc.PeerConnection
	svc  *Service

	mu           sync.Mutex
	callID       string
	remoteSet    bool
	pendingRem   []webrtc.ICECandidateInit
	pendingSend  []Envelope
	joined       chan Envelope
	answered     chan struct{}
	onTrack      chan *webrtc.TrackRemote
	iceState     chan webrtc.ICEConnectionState
	joinedOnce   sync.Once
	answeredOnce sync.Once
}

func newLoopbackClientAPI(t *testing.T) *webrtc.API {
	t.Helper()
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		t.Fatalf("register codecs: %v", err)
	}
	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		t.Fatalf("register interceptors: %v", err)
	}
	se := &webrtc.SettingEngine{}
	se.SetIncludeLoopbackCandidate(true)
	return webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
		webrtc.WithSettingEngine(*se),
	)
}

func newLoopbackPeer(t *testing.T, svc *Service, userID string) *loopbackPeer {
	t.Helper()
	conn := newPipeConn()
	pc, err := newLoopbackClientAPI(t).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatalf("client peer connection: %v", err)
	}

	p := &loopbackPeer{
		t:        t,
		name:     userID,
		conn:     conn,
		pc:       pc,
		svc:      svc,
		joined:   make(chan Envelope, 4),
		answered: make(chan struct{}, 4),
		onTrack:  make(chan *webrtc.TrackRemote, 8),
		iceState: make(chan webrtc.ICEConnectionState, 8),
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		p.mu.Lock()
		callID := p.callID
		p.mu.Unlock()
		if callID == "" {
			return
		}
		init := c.ToJSON()
		env := Envelope{Type: MsgCandidate, CallID: callID, Candidate: init.Candidate}
		if init.SDPMid != nil {
			env.SDPMid = *init.SDPMid
		}
		if init.SDPMLineIndex != nil {
			idx := int(*init.SDPMLineIndex)
			env.SDPMLineIndex = &idx
		}
		conn.clientSend(t, env)
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		select {
		case p.onTrack <- track:
		default:
		}
	})
	pc.OnICEConnectionStateChange(func(s webrtc.ICEConnectionState) {
		select {
		case p.iceState <- s:
		default:
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	t.Cleanup(func() {
		_ = pc.Close()
		_ = conn.Close()
	})
	go svc.HandleConn(ctx, Identity{UserID: userID, DeviceID: "dev-" + userID}, conn)
	go p.readLoop()
	return p
}

func (p *loopbackPeer) readLoop() {
	for {
		select {
		case <-p.conn.closed:
			return
		default:
		}
		select {
		case raw := <-p.conn.out:
			var env Envelope
			if err := json.Unmarshal(raw, &env); err != nil {
				continue
			}
			p.handle(env)
		case <-p.conn.closed:
			return
		}
	}
}

func (p *loopbackPeer) handle(env Envelope) {
	switch env.Type {
	case MsgJoined:
		p.mu.Lock()
		p.callID = env.CallID
		p.mu.Unlock()
		p.joinedOnce.Do(func() { p.joined <- env })
	case MsgAnswer:
		if env.SDP == "" {
			return // acknowledgement frame
		}
		p.applyAnswer(env.SDP)
	case MsgOffer:
		p.applyServerOffer(env.SDP)
	case MsgCandidate:
		p.applyRemoteCandidate(env)
	case MsgError:
		p.t.Logf("%s: server error %s (%s)", p.name, env.Code, env.Message)
	}
}

func (p *loopbackPeer) applyAnswer(sdp string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}); err != nil {
		p.t.Errorf("%s: set remote answer: %v", p.name, err)
		return
	}
	p.remoteSet = true
	p.flushPending()
	p.answeredOnce.Do(func() { p.answered <- struct{}{} })
}

func (p *loopbackPeer) applyServerOffer(sdp string) {
	p.mu.Lock()
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.mu.Unlock()
		return // colliding offer, the server is the impolite side
	}
	if err := p.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}); err != nil {
		p.mu.Unlock()
		p.t.Errorf("%s: set remote offer: %v", p.name, err)
		return
	}
	p.remoteSet = true
	p.flushPending()
	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		p.mu.Unlock()
		p.t.Errorf("%s: create answer: %v", p.name, err)
		return
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		p.mu.Unlock()
		p.t.Errorf("%s: set local answer: %v", p.name, err)
		return
	}
	callID := p.callID
	p.mu.Unlock()
	p.conn.clientSend(p.t, Envelope{Type: MsgAnswer, CallID: callID, SDP: answer.SDP})
}

func (p *loopbackPeer) applyRemoteCandidate(env Envelope) {
	var lineIndex *uint16
	if env.SDPMLineIndex != nil {
		v := uint16(*env.SDPMLineIndex)
		lineIndex = &v
	}
	mid := env.SDPMid
	init := webrtc.ICECandidateInit{Candidate: env.Candidate, SDPMid: &mid, SDPMLineIndex: lineIndex}

	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.remoteSet {
		p.pendingRem = append(p.pendingRem, init)
		return
	}
	if err := p.pc.AddICECandidate(init); err != nil {
		p.t.Errorf("%s: add ice candidate: %v", p.name, err)
	}
}

func (p *loopbackPeer) flushPending() {
	for _, c := range p.pendingRem {
		_ = p.pc.AddICECandidate(c)
	}
	p.pendingRem = nil
}

func (p *loopbackPeer) join(kind CallKind, role Role) Envelope {
	p.conn.clientSend(p.t, Envelope{Type: MsgJoin, ChatID: "chat-loopback", Kind: kind, Role: role, DeviceID: "dev-" + p.name})
	select {
	case env := <-p.joined:
		return env
	case <-time.After(10 * time.Second):
		p.t.Fatalf("%s: timeout waiting for call.joined", p.name)
	}
	return Envelope{}
}

func (p *loopbackPeer) negotiate() {
	p.mu.Lock()
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.mu.Unlock()
		p.t.Fatalf("%s: create offer: %v", p.name, err)
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.mu.Unlock()
		p.t.Fatalf("%s: set local offer: %v", p.name, err)
	}
	callID := p.callID
	p.mu.Unlock()
	p.conn.clientSend(p.t, Envelope{Type: MsgOffer, ID: "offer-1", CallID: callID, SDP: offer.SDP})
}

func (p *loopbackPeer) waitConnected(timeout time.Duration) {
	deadline := time.After(timeout)
	for {
		select {
		case state := <-p.iceState:
			if state == webrtc.ICEConnectionStateConnected || state == webrtc.ICEConnectionStateCompleted {
				return
			}
			if state == webrtc.ICEConnectionStateFailed {
				p.t.Fatalf("%s: ice failed", p.name)
			}
		case <-deadline:
			p.t.Fatalf("%s: timeout waiting for ice to connect", p.name)
		}
	}
}

// TestSFUBroadcastDeliversMedia drives a full broadcast call: a publisher
// pushes RTP into the SFU and a subscriber receives it through a server side
// PeerConnection. The media path runs over loopback.
func TestSFUBroadcastDeliversMedia(t *testing.T) {
	if testing.Short() {
		t.Skip("loopback webrtc test skipped in short mode")
	}

	svc := newTestService(t, Config{Enabled: true, AllowLoopback: true, MeshLimit: 10}, &fakeMembers{allow: true})

	publisher := newLoopbackPeer(t, svc, "u1")
	subscriber := newLoopbackPeer(t, svc, "u2")

	track, err := webrtc.NewTrackLocalStaticRTP(
		webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus},
		"audio", "loopback-stream",
	)
	if err != nil {
		t.Fatalf("create track: %v", err)
	}
	sender, err := publisher.pc.AddTrack(track)
	if err != nil {
		t.Fatalf("publisher add track: %v", err)
	}
	go func() {
		for {
			if _, _, err := sender.ReadRTCP(); err != nil {
				return
			}
		}
	}()

	if _, err := subscriber.pc.AddTransceiverFromKind(webrtc.RTPCodecTypeAudio, webrtc.RTPTransceiverInit{
		Direction: webrtc.RTPTransceiverDirectionRecvonly,
	}); err != nil {
		t.Fatalf("subscriber transceiver: %v", err)
	}

	publisher.join(KindBroadcast, RolePublisher)
	subscriber.join(KindBroadcast, RoleSubscriber)

	publisher.negotiate()
	subscriber.negotiate()

	publisher.waitConnected(20 * time.Second)
	subscriber.waitConnected(20 * time.Second)

	// Feed RTP until the subscriber sees the track.
	stop := make(chan struct{})
	defer close(stop)
	go func() {
		var seq uint16
		var ts uint32
		for {
			select {
			case <-stop:
				return
			default:
			}
			pkt := &rtp.Packet{
				Header: rtp.Header{
					Version:        2,
					PayloadType:    111,
					SequenceNumber: seq,
					Timestamp:      ts,
					SSRC:           4242,
				},
				Payload: make([]byte, 20),
			}
			seq++
			ts += 960
			_ = track.WriteRTP(pkt)
			time.Sleep(10 * time.Millisecond)
		}
	}()

	select {
	case tr := <-subscriber.onTrack:
		if tr.Kind() != webrtc.RTPCodecTypeAudio {
			t.Fatalf("unexpected track kind %s", tr.Kind())
		}
		// Read one packet to prove media actually flows end to end.
		if _, _, err := tr.ReadRTP(); err != nil {
			t.Fatalf("read forwarded rtp: %v", err)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timeout: subscriber never received the publisher track")
	}
}
