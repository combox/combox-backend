package calls

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestParseEnvelopeJoin(t *testing.T) {
	raw := []byte(`{"type":"call.join","chat_id":"chat-1","kind":"group","role":"publisher","device_id":"dev"}`)
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse join: %v", err)
	}
	if env.ChatID != "chat-1" || env.Kind != KindGroup || env.Role != RolePublisher {
		t.Fatalf("unexpected envelope: %+v", env)
	}
}

func TestParseEnvelopeDefaultsKindAllowed(t *testing.T) {
	raw := []byte(`{"type":"call.join","chat_id":"chat-1"}`)
	if _, err := ParseEnvelope(raw); err != nil {
		t.Fatalf("empty kind must be accepted: %v", err)
	}
}

func TestParseEnvelopeRejectsMalformed(t *testing.T) {
	cases := []struct {
		name string
		raw  string
	}{
		{"invalid json", `{"type":`},
		{"missing type", `{"chat_id":"c"}`},
		{"join without chat", `{"type":"call.join","kind":"group"}`},
		{"join bad kind", `{"type":"call.join","chat_id":"c","kind":"webinar"}`},
		{"join bad role", `{"type":"call.join","chat_id":"c","kind":"group","role":"boss"}`},
		{"offer without sdp", `{"type":"call.offer","call_id":"x"}`},
		{"answer without call", `{"type":"call.answer","sdp":"v=0"}`},
		{"candidate without sdp_mid", `{"type":"call.candidate","call_id":"x","candidate":"candidate:1"}`},
		{"relay without target", `{"type":"call.relay","call_id":"x","relay_kind":"offer","sdp":"v=0"}`},
		{"relay unknown kind", `{"type":"call.relay","call_id":"x","target_user_id":"u","relay_kind":"what"}`},
		{"media without state", `{"type":"call.media","call_id":"x"}`},
		{"tracks empty", `{"type":"call.tracks","call_id":"x","tracks":{}}`},
		{"unknown type", `{"type":"call.explode"}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := ParseEnvelope([]byte(tc.raw)); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			} else if !errors.Is(err, ErrInvalidMessage) {
				t.Fatalf("expected ErrInvalidMessage, got %v", err)
			}
		})
	}
}

func TestParseEnvelopeCandidateIndex(t *testing.T) {
	raw := []byte(`{"type":"call.candidate","call_id":"x","candidate":"candidate:1 1 udp 1 127.0.0.1 1000 typ host","sdp_mid":"0","sdp_mline_index":0}`)
	env, err := ParseEnvelope(raw)
	if err != nil {
		t.Fatalf("parse candidate: %v", err)
	}
	if env.SDPMLineIndex == nil || *env.SDPMLineIndex != 0 {
		t.Fatalf("expected mline index 0, got %v", env.SDPMLineIndex)
	}
}

func TestErrorFrameCarriesCode(t *testing.T) {
	frame := errorFrame("req-1", ErrCallFull)
	if frame.Type != MsgError || frame.ID != "req-1" || frame.Code != ErrCallFull.Code {
		t.Fatalf("unexpected error frame: %+v", frame)
	}
	raw, err := json.Marshal(frame)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if len(raw) == 0 {
		t.Fatal("empty frame")
	}
}
