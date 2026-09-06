package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/rekordlink/rekordlink/internal/service"
)

func TestValidateListenRequiresLoopback(t *testing.T) {
	for _, address := range []string{"127.0.0.1:9766", "[::1]:9766", "localhost:9766"} {
		if err := validateListen(address); err != nil {
			t.Fatalf("validate %s: %v", address, err)
		}
	}
	for _, address := range []string{"0.0.0.0:9766", ":9766", "192.168.1.2:9766"} {
		if err := validateListen(address); err == nil {
			t.Fatalf("expected %s to be rejected", address)
		}
	}
}

func TestBuildJoinServiceArgs(t *testing.T) {
	libraryPath, err := filepath.Abs(filepath.Join("..", "library", "testdata", "dj-a.xml"))
	if err != nil {
		t.Fatal(err)
	}
	audioRoot := filepath.Join(t.TempDir(), "Managed Audio")
	output := filepath.Join(t.TempDir(), "shared.xml")
	inviteJSON, err := json.Marshal(map[string]any{
		"endpoint":    "https://127.0.0.1:9777",
		"fingerprint": strings.Repeat("a", 64),
		"pair_code":   "123456789012",
		"room_id":     "test-room",
		"audio":       true,
	})
	if err != nil {
		t.Fatal(err)
	}
	invite := "rekordlink://join/" + base64.RawURLEncoding.EncodeToString(inviteJSON)
	command, args, err := buildServiceArgs(setupRequest{
		Mode:       "join",
		Name:       "DJ B",
		Library:    libraryPath,
		Output:     output,
		AudioRoot:  audioRoot,
		AllowAudio: true,
		Invite:     invite,
		Interval:   "20s",
	})
	if err != nil {
		t.Fatal(err)
	}
	if command != "join" || option(args, "--library") != libraryPath || option(args, "--output") != output {
		t.Fatalf("unexpected command: %s %#v", command, args)
	}
	if option(args, "--audio-root") != audioRoot || !hasOption(args, "--allow-audio-copy") {
		t.Fatalf("audio sync flags missing: %#v", args)
	}
	if option(args, "--invite") != invite || option(args, "--interval") != "20s" {
		t.Fatalf("join flags missing: %#v", args)
	}
	if _, _, err := buildServiceArgs(setupRequest{Mode: "join", Name: "DJ B", Library: libraryPath, Output: output, Invite: invite}); err == nil || !strings.Contains(err.Error(), "must match") {
		t.Fatalf("expected audio-mode mismatch, got %v", err)
	}
}

func TestViewConfigDoesNotExposeInvite(t *testing.T) {
	view := viewConfig(service.Config{Command: "join", Args: []string{
		"--name", "DJ B", "--invite", "rekordlink://join/top-secret", "--library", "/tmp/a.xml",
	}})
	if !view.InviteConfigured {
		t.Fatal("expected invite to be reported as configured")
	}
	if strings.Contains(strings.Join([]string{view.Name, view.Library, view.Output, view.AudioRoot}, " "), "top-secret") {
		t.Fatal("invite leaked through the config view")
	}
}

func TestBuildPublicRelayServiceArgs(t *testing.T) {
	command, args, err := buildServiceArgs(setupRequest{
		Mode:           "relay",
		PublicEndpoint: "https://rekordlink.zeusyboy.com/",
		AllowAudio:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if command != "relay" || option(args, "--public-endpoint") != "https://rekordlink.zeusyboy.com/" {
		t.Fatalf("unexpected public relay command: %s %#v", command, args)
	}
	if _, _, err := buildServiceArgs(setupRequest{Mode: "relay", PublicEndpoint: "https://rekordlink.zeusyboy.com", Advertise: "192.0.2.10"}); err == nil {
		t.Fatal("public endpoint and direct address were both accepted")
	}
}

func TestSecureHandlerRejectsCrossSiteAndInvalidHost(t *testing.T) {
	s := &server{csrf: "expected"}
	next := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) })
	handler := s.secure(next)

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9766/api/control", strings.NewReader("{}"))
	request.RemoteAddr = "127.0.0.1:43210"
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF token returned %d", response.Code)
	}

	request = httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9766/api/control", strings.NewReader("{}"))
	request.RemoteAddr = "127.0.0.1:43210"
	request.Host = "attacker.example:9766"
	request.Header.Set("X-RekordLink-CSRF", "expected")
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusForbidden {
		t.Fatalf("invalid host returned %d", response.Code)
	}
}
