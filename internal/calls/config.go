package calls

import (
	"log/slog"
	"time"
)

// Default limits for a single call room.
const (
	defaultMeshLimit         = 2
	defaultMaxParticipants   = 200
	defaultTURNCredentialTTL = 12 * time.Hour
)

// Config controls the calls subsystem behaviour.
type Config struct {
	// Enabled turns the signaling endpoints on.
	Enabled bool

	// STUNURLs and TURNURLs are handed out to clients via the ICE endpoint.
	STUNURLs []string
	TURNURLs []string

	// TURNSharedSecret is the coturn use-auth-secret value used to mint
	// time-limited TURN credentials (REST API style).
	TURNSharedSecret string
	// TURNCredentialTTL bounds the lifetime of a minted credential.
	TURNCredentialTTL time.Duration

	// MeshLimit is the maximum number of participants that may stay in the
	// mesh/p2p topology. Every further participant forces an escalation to SFU.
	MeshLimit int
	// MaxParticipants caps a single call room.
	MaxParticipants int

	// ICEPortMin/ICEPortMax pin the SFU UDP port range (0 = OS ephemeral).
	ICEPortMin uint16
	ICEPortMax uint16

	// AllowLoopback keeps loopback ICE candidates (used by tests / dev).
	AllowLoopback bool

	Logger *slog.Logger
}

func (c Config) withDefaults() Config {
	if c.MeshLimit <= 0 {
		c.MeshLimit = defaultMeshLimit
	}
	if c.MaxParticipants <= 0 {
		c.MaxParticipants = defaultMaxParticipants
	}
	if c.TURNCredentialTTL <= 0 {
		c.TURNCredentialTTL = defaultTURNCredentialTTL
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	return c
}
