package sync

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/url"
	"strings"
)

type invitation struct {
	Endpoint    string `json:"endpoint"`
	Fingerprint string `json:"fingerprint,omitempty"`
	Transport   string `json:"transport,omitempty"`
	PairCode    string `json:"pair_code"`
	RoomID      string `json:"room_id"`
	Audio       bool   `json:"audio"`
}

const (
	transportPinnedTLS = "pinned-tls"
	transportPublicTLS = "public-tls"
)

func encodeInvite(inv invitation) (string, error) {
	b, err := json.Marshal(inv)
	if err != nil {
		return "", err
	}
	return "rekordlink://join/" + base64.RawURLEncoding.EncodeToString(b), nil
}

func decodeInvite(value string) (invitation, error) {
	const prefix = "rekordlink://join/"
	if !strings.HasPrefix(value, prefix) {
		return invitation{}, errors.New("invite must start with rekordlink://join/")
	}
	b, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, prefix))
	if err != nil {
		return invitation{}, errors.New("invite is malformed")
	}
	var inv invitation
	if err := json.Unmarshal(b, &inv); err != nil {
		return invitation{}, errors.New("invite is malformed")
	}
	parsed, err := url.Parse(inv.Endpoint)
	if err != nil || parsed.Scheme != "https" || parsed.Host == "" {
		return invitation{}, errors.New("invite endpoint is invalid")
	}
	if inv.PairCode == "" || inv.RoomID == "" {
		return invitation{}, errors.New("invite is incomplete")
	}
	// Invitations created before public relay support omitted Transport and
	// always used a pinned, self-signed relay certificate.
	if inv.Transport == "" {
		inv.Transport = transportPinnedTLS
	}
	switch inv.Transport {
	case transportPinnedTLS:
		if len(inv.Fingerprint) != 64 {
			return invitation{}, errors.New("invite is incomplete")
		}
	case transportPublicTLS:
		if inv.Fingerprint != "" {
			return invitation{}, errors.New("public TLS invite must not contain a certificate pin")
		}
	default:
		return invitation{}, errors.New("invite uses an unsupported transport")
	}
	return inv, nil
}

// ValidateInvite checks an invite without exposing its embedded pairing secret.
// It is used by the local management UI before installing a background client.
func ValidateInvite(value string) error {
	_, err := decodeInvite(value)
	return err
}

// InviteAudio reports whether the room advertised by a valid invite transfers
// audio. The pairing code and other invite contents remain internal.
func InviteAudio(value string) (bool, error) {
	invite, err := decodeInvite(value)
	if err != nil {
		return false, err
	}
	return invite.Audio, nil
}
