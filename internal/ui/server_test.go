package ui

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/rekordlink/rekordlink/internal/library"
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

func TestExclusionsPersistThroughServiceConfig(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.xml")
	data, err := library.Marshal(&library.Library{Playlists: library.Playlists{Root: library.Node{Type: "0", Name: "ROOT", Nodes: []library.Node{
		{Type: "0", Name: "Jay"}, {Type: "1", Name: "Private"},
	}}}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"host", "join"} {
		request := setupRequest{Mode: mode, Name: "DJ", Library: path, ExcludePlaylists: []string{"/Jay", "/Private"}}
		command, args, err := buildServiceArgs(request)
		if err != nil {
			t.Fatal(err)
		}
		view := viewConfig(service.Config{Command: command, Args: args})
		if !reflect.DeepEqual(view.ExcludePlaylists, request.ExcludePlaylists) {
			t.Fatalf("saved exclusions lost: %v", view.ExcludePlaylists)
		}
		request.ExcludePlaylists = []string{"/Not present"}
		if _, _, err := buildServiceArgs(request); err == nil {
			t.Fatal("saved an exclusion that would not apply")
		}
		request.ExcludePlaylists = nil
		_, args, err = buildServiceArgs(request)
		if err != nil {
			t.Fatal(err)
		}
		if len(options(args, "--exclude-playlist")) != 0 {
			t.Fatal("clearing exclusions did not restore share-all")
		}
	}
	if got := options([]string{"--exclude-playlist=/Jay", "--exclude-playlist", "/Private"}, "--exclude-playlist"); !reflect.DeepEqual(got, []string{"/Jay", "/Private"}) {
		t.Fatalf("CLI equals form lost: %v", got)
	}
}

func TestPlaylistPickerListsExactPathsAndRequiresCSRF(t *testing.T) {
	path, err := filepath.Abs(filepath.Join("..", "library", "testdata", "dj-a.xml"))
	if err != nil {
		t.Fatal(err)
	}
	body, err := json.Marshal(map[string]string{"library": path})
	if err != nil {
		t.Fatal(err)
	}
	s := &server{csrf: "expected"}
	handler := s.secure(http.HandlerFunc(s.playlists))
	for _, valid := range []bool{false, true} {
		request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:9766/api/playlists", strings.NewReader(string(body)))
		request.RemoteAddr = "127.0.0.1:43210"
		if valid {
			request.Header.Set("X-RekordLink-CSRF", "expected")
		}
		response := httptest.NewRecorder()
		handler.ServeHTTP(response, request)
		if !valid {
			if response.Code != http.StatusForbidden {
				t.Fatal("picker accepted missing CSRF")
			}
			continue
		}
		if response.Code != http.StatusOK {
			t.Fatalf("picker: %d %s", response.Code, response.Body.String())
		}
		var result struct {
			Playlists []library.PlaylistPath `json:"playlists"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, p := range result.Playlists {
			if _, err := library.ExcludePlaylistsXML(data, []string{p.Path}); err != nil {
				t.Fatalf("picker returned unusable path %q: %v", p.Path, err)
			}
		}
		if len(result.Playlists) == 0 {
			t.Fatal("picker returned no playlists")
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
