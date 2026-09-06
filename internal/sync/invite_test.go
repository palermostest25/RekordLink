package sync

import (
	"context"
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestInviteRoundTrip(t *testing.T) {
	want := invitation{Endpoint: "https://192.168.1.10:9777", Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", Transport: transportPinnedTLS, PairCode: "123456789012", RoomID: "room", Audio: true}
	encoded, err := encodeInvite(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeInvite(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestPublicTLSInviteRoundTrip(t *testing.T) {
	want := invitation{Endpoint: "https://rekordlink.example.com", Transport: transportPublicTLS, PairCode: "123456789012", RoomID: "room", Audio: true}
	encoded, err := encodeInvite(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeInvite(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestLegacyInviteDefaultsToPinnedTLS(t *testing.T) {
	want := invitation{Endpoint: "https://192.168.1.10:9777", Fingerprint: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", PairCode: "123456789012", RoomID: "room"}
	encoded, err := encodeInvite(want)
	if err != nil {
		t.Fatal(err)
	}
	got, err := decodeInvite(encoded)
	if err != nil {
		t.Fatal(err)
	}
	if got.Transport != transportPinnedTLS {
		t.Fatalf("legacy invite transport = %q", got.Transport)
	}
}

func TestPublicTLSClientUsesSystemVerification(t *testing.T) {
	client := clientForInvite(invitation{Transport: transportPublicTLS})
	transport, ok := client.Transport.(*http.Transport)
	if !ok || transport.TLSClientConfig == nil {
		t.Fatal("public client did not configure HTTPS transport")
	}
	if transport.TLSClientConfig.InsecureSkipVerify {
		t.Fatal("public client disabled system certificate verification")
	}
	if client.CheckRedirect == nil {
		t.Fatal("public client permits redirects")
	}
}

func TestValidatePublicEndpoint(t *testing.T) {
	got, err := ValidatePublicEndpoint("https://rekordlink.zeusyboy.com/")
	if err != nil || got != "https://rekordlink.zeusyboy.com" {
		t.Fatalf("got %q, %v", got, err)
	}
	for _, value := range []string{"http://rekordlink.example.com", "https://user@example.com", "https://example.com/path", "https://example.com?x=1"} {
		if _, err := ValidatePublicEndpoint(value); err == nil {
			t.Fatalf("accepted invalid public endpoint %q", value)
		}
	}
}

func TestPublicRelayWritesSystemTLSInvite(t *testing.T) {
	stateDir := t.TempDir()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := RunRelay(ctx, RelayConfig{
		StateDir:       stateDir,
		ListenAddr:     "127.0.0.1:0",
		PublicEndpoint: "https://rekordlink.zeusyboy.com/",
		Version:        "test",
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "invite.txt"))
	if err != nil {
		t.Fatal(err)
	}
	invite, err := decodeInvite(string(b[:len(b)-1]))
	if err != nil {
		t.Fatal(err)
	}
	if invite.Endpoint != "https://rekordlink.zeusyboy.com" || invite.Transport != transportPublicTLS || invite.Fingerprint != "" {
		t.Fatalf("unexpected public relay invite: %+v", invite)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "tls-key.pem")); !os.IsNotExist(err) {
		t.Fatalf("public relay unexpectedly created a private TLS key: %v", err)
	}
}

func TestRoomRegistrationAndPersistence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "room.json")
	r, err := newRoom(path, "DJ A")
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := r.register("DJ B", r.inviteCode())
	if err != nil {
		t.Fatal(err)
	}
	if !r.authenticate(id, token) {
		t.Fatal("issued credentials do not authenticate")
	}
	reloaded, err := loadRoom(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reloaded.authenticate(id, token) {
		t.Fatal("credentials did not survive reload")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o077 != 0 {
		t.Fatalf("state file permissions are too broad: %o", info.Mode().Perm())
	}
}

func TestRelayAcceptsExactlyTwoDJs(t *testing.T) {
	r, err := newRelayRoom(filepath.Join(t.TempDir(), "room.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"DJ A", "DJ B"} {
		if _, _, err := r.register(name, r.inviteCode()); err != nil {
			t.Fatalf("register %s: %v", name, err)
		}
	}
	if _, _, err := r.register("DJ C", r.inviteCode()); err == nil {
		t.Fatal("third DJ unexpectedly joined a two-DJ room")
	}
}
