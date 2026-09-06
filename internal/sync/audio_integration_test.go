package sync

import (
	"bytes"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rekordlink/rekordlink/internal/audio"
	"github.com/rekordlink/rekordlink/internal/library"
)

func TestEndToEndAudioAndLibrarySync(t *testing.T) {
	temp := t.TempDir()
	roomState, err := newRoom(filepath.Join(temp, "room.json"), "DJ A")
	if err != nil {
		t.Fatal(err)
	}
	hostAudio, err := audio.New(filepath.Join(temp, "host-managed"), filepath.Join(temp, "server-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	hostSource := filepath.Join(temp, "host-source.mp3")
	if err := os.WriteFile(hostSource, bytes.Repeat([]byte{'h'}, int(transferChunkBytes)+123), 0o600); err != nil {
		t.Fatal(err)
	}
	hostXML := oneTrackXML(t, "1", "Host Track", hostSource, "Host Set")
	preparedHost, err := hostAudio.Prepare(hostXML)
	if err != nil {
		t.Fatal(err)
	}
	if err := roomState.update(roomState.hostID(), "DJ A", preparedHost.XML); err != nil {
		t.Fatal(err)
	}

	server := &apiServer{room: roomState, version: "test", audio: hostAudio, registerFails: make(map[string][]time.Time)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("localhost sockets unavailable: %v", err)
	}
	tlsServer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: server.handler()}}
	tlsServer.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	tlsServer.StartTLS()
	defer tlsServer.Close()
	fingerprint := certFingerprint(tlsServer.Certificate().Raw)

	guestID, token, err := roomState.register("DJ B", roomState.inviteCode())
	if err != nil {
		t.Fatal(err)
	}
	guestSource := filepath.Join(temp, "guest-source.flac")
	if err := os.WriteFile(guestSource, bytes.Repeat([]byte{'g'}, int(transferChunkBytes)+321), 0o600); err != nil {
		t.Fatal(err)
	}
	guestLibraryPath := filepath.Join(temp, "guest.xml")
	if err := os.WriteFile(guestLibraryPath, oneTrackXML(t, "2", "Guest Track", guestSource, "Guest Set"), 0o600); err != nil {
		t.Fatal(err)
	}
	guestAudio, err := audio.New(filepath.Join(temp, "guest-managed"), filepath.Join(temp, "guest-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	guestOutput := filepath.Join(temp, "guest-shared.xml")
	client := &syncClient{
		http:    pinnedClient(fingerprint),
		state:   clientState{Endpoint: tlsServer.URL, PeerID: guestID, Token: token, Name: "DJ B"},
		library: guestLibraryPath,
		output:  guestOutput,
		audio:   guestAudio,
	}
	if err := client.pushIfChanged(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}

	mergedBytes, err := os.ReadFile(guestOutput)
	if err != nil {
		t.Fatal(err)
	}
	merged, err := library.Parse(mergedBytes)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Collection.Tracks) != 2 || library.PlaylistCount(merged.Playlists.Root) != 2 {
		t.Fatalf("unexpected merge: %d tracks, %d playlists", len(merged.Collection.Tracks), library.PlaylistCount(merged.Playlists.Root))
	}
	for _, track := range merged.Collection.Tracks {
		path, ok := audio.FileURIPath(track.Location)
		if !ok {
			t.Fatalf("track was not materialized: %s", track.Location)
		}
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("materialized audio missing: %v", err)
		}
	}
}

func TestResumableSnapshotSpansChunks(t *testing.T) {
	temp := t.TempDir()
	roomState, err := newRelayRoom(filepath.Join(temp, "room.json"))
	if err != nil {
		t.Fatal(err)
	}
	server := &apiServer{room: roomState, version: "test", registerFails: make(map[string][]time.Time)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("localhost sockets unavailable: %v", err)
	}
	tlsServer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: server.handler()}}
	tlsServer.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	tlsServer.StartTLS()
	defer tlsServer.Close()

	peerID, token, err := roomState.register("DJ A", roomState.inviteCode())
	if err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{
		TrackID:  "1",
		Name:     "Large metadata",
		Location: audio.PathFileURI(filepath.Join(temp, "metadata-only-test.mp3")),
		Comments: string(bytes.Repeat([]byte{'x'}, int(transferChunkBytes)+257)),
	}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT"}
	data, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	client := &syncClient{
		http:  pinnedClient(certFingerprint(tlsServer.Certificate().Raw)),
		state: clientState{Endpoint: tlsServer.URL, PeerID: peerID, Token: token, Name: "DJ A"},
	}
	if err := client.publishSnapshot(t.Context(), data); err != nil {
		t.Fatal(err)
	}
	if got := roomState.status().Peers[0].Tracks; got != 1 {
		t.Fatalf("published track count = %d", got)
	}
}

func TestRelayTwoClientsConverge(t *testing.T) {
	temp := t.TempDir()
	roomState, err := newRelayRoom(filepath.Join(temp, "room.json"))
	if err != nil {
		t.Fatal(err)
	}
	relayAudio, err := audio.New(filepath.Join(temp, "relay-managed"), filepath.Join(temp, "relay-blobs"))
	if err != nil {
		t.Fatal(err)
	}
	server := &apiServer{room: roomState, version: "test", audio: relayAudio, registerFails: make(map[string][]time.Time)}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Skipf("localhost sockets unavailable: %v", err)
	}
	tlsServer := &httptest.Server{Listener: listener, Config: &http.Server{Handler: server.handler()}}
	tlsServer.TLS = &tls.Config{MinVersion: tls.VersionTLS13}
	tlsServer.StartTLS()
	defer tlsServer.Close()
	fingerprint := certFingerprint(tlsServer.Certificate().Raw)

	clients := make([]*syncClient, 0, 2)
	for i, name := range []string{"DJ A", "DJ B"} {
		id, token, err := roomState.register(name, roomState.inviteCode())
		if err != nil {
			t.Fatal(err)
		}
		source := writeAudio(t, filepath.Join(temp, name+".mp3"), name+" audio")
		libraryPath := filepath.Join(temp, name+".xml")
		if err := os.WriteFile(libraryPath, oneTrackXML(t, string(rune('1'+i)), name+" Track", source, name+" Set"), 0o600); err != nil {
			t.Fatal(err)
		}
		manager, err := audio.New(filepath.Join(temp, name+"-managed"), filepath.Join(temp, name+"-blobs"))
		if err != nil {
			t.Fatal(err)
		}
		clients = append(clients, &syncClient{
			http: pinnedClient(fingerprint), state: clientState{Endpoint: tlsServer.URL, PeerID: id, Token: token, Name: name},
			library: libraryPath, output: filepath.Join(temp, name+"-shared.xml"), audio: manager,
		})
	}
	for _, client := range clients {
		if err := client.pushIfChanged(t.Context(), true); err != nil {
			t.Fatal(err)
		}
	}
	for _, client := range clients {
		if err := client.pull(t.Context()); err != nil {
			t.Fatal(err)
		}
		data, err := os.ReadFile(client.output)
		if err != nil {
			t.Fatal(err)
		}
		lib, err := library.Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		if len(lib.Collection.Tracks) != 2 || library.PlaylistCount(lib.Playlists.Root) != 2 {
			t.Fatalf("relay did not converge: %d tracks, %d playlists", len(lib.Collection.Tracks), library.PlaylistCount(lib.Playlists.Root))
		}
	}
}

func writeAudio(t *testing.T, path, content string) string {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func oneTrackXML(t *testing.T, id, name, path, playlist string) []byte {
	t.Helper()
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{TrackID: id, Name: name, Artist: "Test", Location: audio.PathFileURI(path)}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT", Nodes: []library.Node{{Type: "1", Name: playlist, Tracks: []library.PlaylistTrack{{Key: id}}}}}
	b, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
