package calls

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
)

// Service is the public facade of the calls subsystem: it owns the pion
// factory, the room registry and the signaling entry point.
type Service struct {
	cfg         Config
	store       Store
	members     MemberChecker
	hub         *Hub
	api         *webrtc.API
	stunServers []webrtc.ICEServer
	log         *slog.Logger
	now         func() time.Time
}

// New builds the calls service. store may be nil (memory only), members may
// be nil (join authorization is skipped — only for tests).
func New(cfg Config, store Store, members MemberChecker) (*Service, error) {
	cfg = cfg.withDefaults()

	api, err := newWebRTCAPI(cfg)
	if err != nil {
		return nil, err
	}

	stun := make([]webrtc.ICEServer, 0, len(cfg.STUNURLs))
	if len(cfg.STUNURLs) > 0 {
		stun = append(stun, webrtc.ICEServer{URLs: append([]string(nil), cfg.STUNURLs...)})
	}

	svc := &Service{
		cfg:         cfg,
		store:       store,
		members:     members,
		api:         api,
		stunServers: stun,
		log:         cfg.Logger,
		now:         func() time.Time { return time.Now().UTC() },
	}
	svc.hub = newHub(cfg, store, members)
	svc.hub.newPeer = svc.newPeer
	return svc, nil
}

// Enabled reports whether the subsystem should be wired into the router.
func (s *Service) Enabled() bool { return s.cfg.Enabled }

// SetNotifier subscribes a call lifecycle listener (ring/dismiss events).
func (s *Service) SetNotifier(n Notifier) {
	if s.hub != nil {
		s.hub.notifier = n
	}
}

// Config exposes the (defaults applied) configuration.
func (s *Service) Config() Config { return s.cfg }

// ICEServers mints the RTCIceServer list for a user.
func (s *Service) ICEServers(userID string) []ICEServer {
	return BuildICEServers(s.cfg, userID, s.now())
}

// ActiveCall returns the live call of a chat together with its participants.
func (s *Service) ActiveCall(chatID string) (CallRecord, []*Participant, bool) {
	room, ok := s.hub.ActiveCall(strings.TrimSpace(chatID))
	if !ok {
		return CallRecord{}, nil, false
	}
	return room.Record(), room.snapshotParticipants(), true
}

// LiveCalls lists every active call (diagnostics).
func (s *Service) LiveCalls() []CallRecord { return s.hub.Rooms() }

// HandleConn runs one signaling connection until it is closed.
func (s *Service) HandleConn(ctx context.Context, ident Identity, conn SignalConn) {
	if !s.cfg.Enabled {
		_ = conn.Close()
		return
	}
	sess := newSession(s, ident, conn)
	sess.run(ctx)
}

// Close tears every room down.
func (s *Service) Close(ctx context.Context) {
	if s.hub == nil {
		return
	}
	s.hub.CloseAll(ctx, "shutdown")
}

// newPeer builds the server side PeerConnection of one participant.
func (s *Service) newPeer(room *Room, part *Participant) (*sfuPeer, error) {
	pc, err := s.api.NewPeerConnection(webrtc.Configuration{ICEServers: s.stunServers})
	if err != nil {
		return nil, err
	}
	peer := newSFUPeer(room, part.UserID, pc, s.stunServers)
	return peer, nil
}

func newWebRTCAPI(cfg Config) (*webrtc.API, error) {
	me := &webrtc.MediaEngine{}
	if err := me.RegisterDefaultCodecs(); err != nil {
		return nil, err
	}
	// RFC 6464 audio levels so browsers can report speaking states over media.
	_ = me.RegisterHeaderExtension(
		webrtc.RTPHeaderExtensionCapability{URI: audioLevelExtensionURI},
		webrtc.RTPCodecTypeAudio,
	)

	ir := &interceptor.Registry{}
	if err := webrtc.RegisterDefaultInterceptors(me, ir); err != nil {
		return nil, err
	}

	se := &webrtc.SettingEngine{}
	if cfg.ICEPortMin > 0 && cfg.ICEPortMax >= cfg.ICEPortMin {
		if err := se.SetEphemeralUDPPortRange(cfg.ICEPortMin, cfg.ICEPortMax); err != nil {
			return nil, err
		}
	}
	se.SetIncludeLoopbackCandidate(cfg.AllowLoopback)

	return webrtc.NewAPI(
		webrtc.WithMediaEngine(me),
		webrtc.WithInterceptorRegistry(ir),
		webrtc.WithSettingEngine(*se),
	), nil
}
