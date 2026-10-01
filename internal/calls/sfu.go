package calls

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pion/rtcp"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
)

// audioLevelExtensionURI negotiates RFC 6464 audio levels between the SFU and
// browsers so the server can correlate speaking indicators with media.
const audioLevelExtensionURI = "urn:ietf:params:rtp-hdrext:ssrc-audio-level"

// publishedTrack is one inbound RTP stream owned by a publisher.
type publishedTrack struct {
	key     string
	ownerID string
	peer    *sfuPeer
	track   *webrtc.TrackRemote
}

// forwardedTrack is one outbound leg of a published track towards a subscriber.
type forwardedTrack struct {
	key    string
	peer   *sfuPeer
	sender *webrtc.RTPSender
	track  *webrtc.TrackLocalStaticRTP
}

// sfuPeer wraps a server side pion PeerConnection bound to one participant.
type sfuPeer struct {
	room   *Room
	userID string
	pc     *webrtc.PeerConnection
	stun   []webrtc.ICEServer

	mu            sync.Mutex
	closed        bool
	remoteSet     bool
	localDescSent bool
	needNegotiate bool
	pendingRemote []webrtc.ICECandidateInit
	pendingLocal  []webrtc.ICECandidateInit
	usedTrackIDs  map[string]int

	published map[string]*publishedTrack
	forwarded map[string]*forwardedTrack
}

func newSFUPeer(room *Room, userID string, pc *webrtc.PeerConnection, stun []webrtc.ICEServer) *sfuPeer {
	p := &sfuPeer{
		room:         room,
		userID:       userID,
		pc:           pc,
		stun:         stun,
		usedTrackIDs: map[string]int{},
		published:    map[string]*publishedTrack{},
		forwarded:    map[string]*forwardedTrack{},
	}

	pc.OnICECandidate(func(c *webrtc.ICECandidate) {
		if c == nil {
			return
		}
		p.mu.Lock()
		if !p.localDescSent {
			p.pendingLocal = append(p.pendingLocal, c.ToJSON())
			p.mu.Unlock()
			return
		}
		p.mu.Unlock()
		p.sendCandidate(c.ToJSON())
	})
	pc.OnNegotiationNeeded(func() {
		p.negotiate()
	})
	pc.OnTrack(func(track *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		p.onTrack(track)
	})
	pc.OnConnectionStateChange(func(state webrtc.PeerConnectionState) {
		if state != webrtc.PeerConnectionStateFailed {
			return
		}
		// Terminal for this peer: drop it so the next offer from the client
		// gets a fresh server side peer connection.
		go func() {
			errFrame := newFrame(MsgError)
			errFrame.CallID = p.room.ID
			errFrame.Code = "ice_failed"
			errFrame.Message = "media connection failed"
			_ = p.send(errFrame)
			p.close("ice_failed")
			p.room.removeRoutesFor(p.userID)
		}()
	})
	return p
}

func (p *sfuPeer) isClosed() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.closed
}

func (p *sfuPeer) send(env Envelope) error {
	return p.room.sendTo(p.userID, env)
}

func (p *sfuPeer) sendCandidate(c webrtc.ICECandidateInit) {
	env := newFrame(MsgCandidate)
	env.CallID = p.room.ID
	env.Candidate = c.Candidate
	if c.SDPMid != nil {
		env.SDPMid = *c.SDPMid
	}
	if c.SDPMLineIndex != nil {
		idx := int(*c.SDPMLineIndex)
		env.SDPMLineIndex = &idx
	}
	_ = p.send(env)
}

// negotiate creates and publishes a server side offer (impolite side).
func (p *sfuPeer) negotiate() {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		p.needNegotiate = true
		p.mu.Unlock()
		return
	}
	p.needNegotiate = false
	offer, err := p.pc.CreateOffer(nil)
	if err != nil {
		p.mu.Unlock()
		return
	}
	if err := p.pc.SetLocalDescription(offer); err != nil {
		p.mu.Unlock()
		return
	}
	env := newFrame(MsgOffer)
	env.CallID = p.room.ID
	env.SDP = offer.SDP
	p.localDescSent = false
	sendErr := p.send(env)
	p.localDescSent = true
	pending := p.pendingLocal
	p.pendingLocal = nil
	p.mu.Unlock()

	if sendErr == nil {
		for _, c := range pending {
			p.sendCandidate(c)
		}
	}
}

// handleOffer consumes a client offer and answers it. The answer reuses the
// request id of the offer so the client can correlate the reply.
func (p *sfuPeer) handleOffer(sdp, reqID string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrInvalidState
	}
	if p.pc.SignalingState() != webrtc.SignalingStateStable {
		// Impolite peer: drop the colliding offer, the client re-offers once
		// the current negotiation settles (perfect negotiation).
		return ErrInvalidState
	}
	offer := webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: sdp}
	if err := p.pc.SetRemoteDescription(offer); err != nil {
		return newError(ErrInvalidState.Code, err.Error())
	}
	p.remoteSet = true
	p.flushPendingRemote()

	answer, err := p.pc.CreateAnswer(nil)
	if err != nil {
		return newError(ErrInvalidState.Code, err.Error())
	}
	if err := p.pc.SetLocalDescription(answer); err != nil {
		return newError(ErrInvalidState.Code, err.Error())
	}
	env := newFrame(MsgAnswer)
	env.ID = reqID
	env.CallID = p.room.ID
	env.SDP = answer.SDP
	p.localDescSent = false
	sendErr := p.send(env)
	p.localDescSent = true
	pending := p.pendingLocal
	p.pendingLocal = nil
	if sendErr != nil {
		return sendErr
	}
	for _, c := range pending {
		p.sendCandidate(c)
	}
	if p.needNegotiate {
		p.needNegotiate = false
		go p.negotiate()
	}
	return nil
}

// handleAnswer applies the client answer to a server offer.
func (p *sfuPeer) handleAnswer(sdp string) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrInvalidState
	}
	if p.pc.SignalingState() != webrtc.SignalingStateHaveLocalOffer {
		return ErrInvalidState
	}
	answer := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: sdp}
	if err := p.pc.SetRemoteDescription(answer); err != nil {
		return newError(ErrInvalidState.Code, err.Error())
	}
	p.remoteSet = true
	p.flushPendingRemote()
	if p.needNegotiate {
		p.needNegotiate = false
		go p.negotiate()
	}
	return nil
}

// handleCandidate trickles a client ICE candidate into the peer connection.
func (p *sfuPeer) handleCandidate(candidate, sdpMid string, sdpMLineIndex *int) error {
	var lineIndex *uint16
	if sdpMLineIndex != nil {
		v := uint16(*sdpMLineIndex)
		lineIndex = &v
	}
	init := webrtc.ICECandidateInit{
		Candidate:     candidate,
		SDPMLineIndex: lineIndex,
	}
	if strings.TrimSpace(sdpMid) != "" {
		mid := sdpMid
		init.SDPMid = &mid
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return ErrInvalidState
	}
	if !p.remoteSet {
		p.pendingRemote = append(p.pendingRemote, init)
		return nil
	}
	if err := p.pc.AddICECandidate(init); err != nil {
		return newError(ErrInvalidState.Code, err.Error())
	}
	return nil
}

func (p *sfuPeer) flushPendingRemote() {
	for _, c := range p.pendingRemote {
		_ = p.pc.AddICECandidate(c)
	}
	p.pendingRemote = nil
}

// onTrack registers an inbound publisher stream and fans it out.
func (p *sfuPeer) onTrack(track *webrtc.TrackRemote) {
	if !p.room.allowsPublish(p.userID) {
		return
	}
	key := publishKey(p.userID, track.ID())
	src := &publishedTrack{key: key, ownerID: p.userID, peer: p, track: track}

	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.published[key] = src
	p.mu.Unlock()

	p.room.attachPublisher(src)
	go p.pump(src)
}

// pump reads the inbound stream exactly once and fans packets out to every
// subscriber leg of this track.
func (p *sfuPeer) pump(src *publishedTrack) {
	for {
		pkt, _, err := src.track.ReadRTP()
		if err != nil {
			break
		}
		p.room.fanout(src.key, pkt)
	}
	p.dropPublished(src)
}

func (p *sfuPeer) dropPublished(src *publishedTrack) {
	p.mu.Lock()
	if current, ok := p.published[src.key]; ok && current == src {
		delete(p.published, src.key)
	}
	p.mu.Unlock()
	p.room.detachPublisher(src)
}

func (p *sfuPeer) publishedSnapshot() []*publishedTrack {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]*publishedTrack, 0, len(p.published))
	for _, t := range p.published {
		out = append(out, t)
	}
	return out
}

// addForwarded wires an inbound publisher track onto this (subscriber) peer.
func (p *sfuPeer) addForwarded(src *publishedTrack) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	if _, ok := p.forwarded[src.key]; ok {
		p.mu.Unlock()
		return
	}
	trackID := p.uniqueTrackID(src.ownerID + "#" + p.room.sourceFor(src.ownerID, src.track.ID(), src.track.Kind()))
	local, err := webrtc.NewTrackLocalStaticRTP(
		src.track.Codec().RTPCodecCapability,
		trackID,
		p.room.ID,
	)
	if err != nil {
		p.mu.Unlock()
		return
	}
	sender, err := p.pc.AddTrack(local)
	if err != nil {
		p.mu.Unlock()
		return
	}
	ft := &forwardedTrack{key: src.key, peer: p, sender: sender, track: local}
	p.forwarded[src.key] = ft
	p.mu.Unlock()

	p.room.addRoute(&outRoute{ownerID: src.ownerID, track: ft})
	go p.watchForwardedRTCP(ft, src)
}

// removeForwarded detaches a publisher track from this subscriber.
func (p *sfuPeer) removeForwarded(key string) {
	p.mu.Lock()
	ft, ok := p.forwarded[key]
	if ok {
		delete(p.forwarded, key)
	}
	closed := p.closed
	p.mu.Unlock()
	if !ok {
		return
	}
	p.room.removeRoute(key, ft)
	if !closed {
		_ = p.pc.RemoveTrack(ft.sender)
	}
}

// watchForwardedRTCP turns subscriber PLI into a keyframe request towards the
// publisher (RFC 6464 style feedback loop).
func (p *sfuPeer) watchForwardedRTCP(ft *forwardedTrack, src *publishedTrack) {
	for {
		pkts, _, err := ft.sender.ReadRTCP()
		if err != nil {
			return
		}
		for _, pkt := range pkts {
			if _, ok := pkt.(*rtcp.PictureLossIndication); ok {
				_ = src.peer.pc.WriteRTCP([]rtcp.Packet{
					&rtcp.PictureLossIndication{MediaSSRC: uint32(src.track.SSRC())},
				})
			}
		}
	}
}

func (p *sfuPeer) uniqueTrackID(base string) string {
	if n, used := p.usedTrackIDs[base]; used {
		p.usedTrackIDs[base] = n + 1
		return fmt.Sprintf("%s#%d", base, n+1)
	}
	p.usedTrackIDs[base] = 1
	return base
}

func (p *sfuPeer) close(reason string) {
	p.mu.Lock()
	if p.closed {
		p.mu.Unlock()
		return
	}
	p.closed = true
	forwarded := make([]*forwardedTrack, 0, len(p.forwarded))
	for _, ft := range p.forwarded {
		forwarded = append(forwarded, ft)
	}
	p.forwarded = map[string]*forwardedTrack{}
	p.published = map[string]*publishedTrack{}
	p.mu.Unlock()

	for _, ft := range forwarded {
		p.room.removeRoute(ft.key, ft)
	}
	_ = p.pc.Close()
}

// ---------------------------------------------------------------------------
// room routing table
// ---------------------------------------------------------------------------

// allowsPublish enforces the broadcast single publisher rule.
func (r *Room) allowsPublish(userID string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if r.Topology != TopologyBroadcast {
		return true
	}
	p, ok := r.participants[userID]
	return ok && p.Role == RolePublisher
}

// attachPublisher registers an inbound stream for every other participant.
func (r *Room) attachPublisher(src *publishedTrack) {
	for _, peer := range r.peerSnapshot() {
		if peer.userID == src.ownerID {
			continue
		}
		peer.addForwarded(src)
	}
}

// detachPublisher removes an inbound stream from every subscriber.
func (r *Room) detachPublisher(src *publishedTrack) {
	for _, peer := range r.peerSnapshot() {
		peer.removeForwarded(src.key)
	}
	r.mu.Lock()
	delete(r.routes, src.key)
	r.mu.Unlock()
}

// attachExistingPublished replays already known streams onto a fresh peer.
func (r *Room) attachExistingPublished() {
	peers := r.peerSnapshot()
	for _, dst := range peers {
		for _, srcPeer := range peers {
			if srcPeer.userID == dst.userID {
				continue
			}
			for _, src := range srcPeer.publishedSnapshot() {
				dst.addForwarded(src)
			}
		}
	}
}

func (r *Room) peerSnapshot() []*sfuPeer {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]*sfuPeer, 0, len(r.peers))
	for _, p := range r.peers {
		out = append(out, p)
	}
	return out
}

func (r *Room) addRoute(route *outRoute) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := route.track.key
	for _, existing := range r.routes[key] {
		if existing.track == route.track {
			return
		}
	}
	r.routes[key] = append(r.routes[key], route)
}

func (r *Room) removeRoute(key string, ft *forwardedTrack) {
	r.mu.Lock()
	defer r.mu.Unlock()
	routes := r.routes[key]
	kept := routes[:0]
	for _, existing := range routes {
		if existing.track != ft {
			kept = append(kept, existing)
		}
	}
	if len(kept) == 0 {
		delete(r.routes, key)
		return
	}
	r.routes[key] = kept
}

// removeRoutesFor drops every leg owned by (or published by) a participant.
func (r *Room) removeRoutesFor(userID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for key, routes := range r.routes {
		kept := routes[:0]
		for _, route := range routes {
			if route.ownerID == userID || route.track.peer.userID == userID {
				continue
			}
			kept = append(kept, route)
		}
		if len(kept) == 0 {
			delete(r.routes, key)
			continue
		}
		r.routes[key] = kept
	}
}

func (r *Room) removeAllRoutes() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.routes = map[string][]*outRoute{}
}

func (r *Room) dropPeerCaches() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.peers = map[string]*sfuPeer{}
	r.trackSources = map[string]map[string]string{}
}

// fanout forwards one RTP packet to every subscriber of the given track.
func (r *Room) fanout(key string, pkt *rtp.Packet) {
	r.mu.RLock()
	routes := r.routes[key]
	if len(routes) == 0 {
		r.mu.RUnlock()
		return
	}
	targets := make([]*forwardedTrack, len(routes))
	for i, route := range routes {
		targets[i] = route.track
	}
	r.mu.RUnlock()
	for _, t := range targets {
		_ = t.track.WriteRTP(pkt)
	}
}

// ---------------------------------------------------------------------------
// track naming helpers (client sends `call.tracks` describing its sources)
// ---------------------------------------------------------------------------

func publishKey(ownerID, trackID string) string {
	return ownerID + "|" + normalizeTrackKey(trackID)
}

func normalizeTrackKey(trackID string) string {
	return strings.TrimSpace(strings.ToLower(trackID))
}

func normalizeTrackSource(source string) string {
	switch strings.TrimSpace(strings.ToLower(source)) {
	case "audio", "mic", "voice":
		return "audio"
	case "screen", "display":
		return "screen"
	case "camera", "cam", "video":
		return "camera"
	default:
		return ""
	}
}

func defaultTrackSource(kind webrtc.RTPCodecType) string {
	if kind == webrtc.RTPCodecTypeAudio {
		return "audio"
	}
	return "camera"
}

// sourceFor resolves the media source label of a publisher track. Publishers
// announce their track -> source mapping with `call.tracks`; without it we
// fall back to the codec kind.
func (r *Room) sourceFor(userID, trackID string, kind webrtc.RTPCodecType) string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	if src, ok := r.trackSources[userID][normalizeTrackKey(trackID)]; ok && src != "" {
		return src
	}
	return defaultTrackSource(kind)
}
