package sync

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/rekordlink/rekordlink/internal/audio"
	"github.com/rekordlink/rekordlink/internal/library"
)

func excludedSource(t *testing.T) (path string, original []byte, privateHash, publicHash string) {
	t.Helper()
	dir := t.TempDir()
	privateFile, publicFile := filepath.Join(dir, "private.mp3"), filepath.Join(dir, "public.mp3")
	if err := os.WriteFile(privateFile, []byte("private audio"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(publicFile, []byte("public audio"), 0600); err != nil {
		t.Fatal(err)
	}
	privateHash, err := audio.HashFile(privateFile)
	if err != nil {
		t.Fatal(err)
	}
	publicHash, err = audio.HashFile(publicFile)
	if err != nil {
		t.Fatal(err)
	}
	lib := &library.Library{Collection: library.Collection{Tracks: []library.Track{
		{TrackID: "1", Name: "PRIVATE TITLE", Location: audio.PathFileURI(privateFile)},
		{TrackID: "2", Name: "Public", Location: audio.PathFileURI(publicFile)},
	}}, Playlists: library.Playlists{Root: library.Node{Type: "0", Name: "ROOT", Nodes: []library.Node{
		{Type: "0", Name: "Jay", Nodes: []library.Node{{Type: "1", Name: "Set", Tracks: []library.PlaylistTrack{{Key: "1"}}}}},
		{Type: "1", Name: "Mine", Tracks: []library.PlaylistTrack{{Key: "1"}, {Key: "2"}}},
	}}}}
	original, err = library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(dir, "source.xml")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	return
}

func TestJoinExcludesBeforeUploadAndLeavesSourceIntact(t *testing.T) {
	for _, withAudio := range []bool{false, true} {
		t.Run(map[bool]string{false: "metadata", true: "audio"}[withAudio], func(t *testing.T) {
			source, original, privateHash, publicHash := excludedSource(t)
			roomState, err := newRelayRoom(filepath.Join(t.TempDir(), "room.json"))
			if err != nil {
				t.Fatal(err)
			}
			id, token, err := roomState.register("Test DJ", roomState.inviteCode())
			if err != nil {
				t.Fatal(err)
			}
			var localAudio, relayAudio *audio.Manager
			if withAudio {
				localAudio, err = audio.New(t.TempDir(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
				relayAudio, err = audio.New(t.TempDir(), t.TempDir())
				if err != nil {
					t.Fatal(err)
				}
			}
			server := &apiServer{room: roomState, audio: relayAudio, registerFails: map[string][]time.Time{}}
			httpServer := httptest.NewServer(server.handler())
			defer httpServer.Close()
			client := &syncClient{http: httpServer.Client(), state: clientState{Endpoint: httpServer.URL, PeerID: id, Token: token, Name: "Test DJ"}, library: source, audio: localAudio, excludePlaylists: []string{"/Jay"}}
			if err := client.pushIfChanged(t.Context(), true); err != nil {
				t.Fatal(err)
			}
			published, _, err := roomState.combined(id)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Contains(published, []byte("PRIVATE TITLE")) || bytes.Contains(published, []byte(`Name="Jay"`)) {
				t.Fatal("private metadata reached relay")
			}
			lib, err := library.Parse(published)
			if err != nil {
				t.Fatal(err)
			}
			if len(lib.Collection.Tracks) != 1 {
				t.Fatalf("published %d tracks", len(lib.Collection.Tracks))
			}
			if withAudio {
				for _, manager := range []*audio.Manager{localAudio, relayAudio} {
					if _, ok := manager.HasBlob(privateHash); ok {
						t.Fatal("excluded audio was cached or uploaded")
					}
					if _, ok := manager.HasBlob(publicHash); !ok {
						t.Fatal("public audio missing")
					}
				}
			}
			actual, err := os.ReadFile(source)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(actual, original) {
				t.Fatal("source library changed")
			}
			// The same boundary also applies to later publications. A renamed
			// excluded folder must not silently share its former private contents.
			renamed := bytes.ReplaceAll(original, []byte(`Name="Jay"`), []byte(`Name="Renamed"`))
			if err := os.WriteFile(source, renamed, 0600); err != nil {
				t.Fatal(err)
			}
			revision := roomState.status().Revision
			if err := client.pushIfChanged(t.Context(), false); err == nil {
				t.Fatal("renamed exclusion did not stop publication")
			}
			if roomState.status().Revision != revision {
				t.Fatal("invalid exclusion modified relay snapshot")
			}
		})
	}
}

func TestHostExcludesBeforeAudioPreparation(t *testing.T) {
	source, _, privateHash, publicHash := excludedSource(t)
	r, err := newRoom(filepath.Join(t.TempDir(), "room.json"), "Host")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := audio.New(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := publishFile(r, r.hostID(), "Host", source, manager, []string{"/Jay"}, nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := manager.HasBlob(privateHash); ok {
		t.Fatal("host cached excluded audio")
	}
	if _, ok := manager.HasBlob(publicHash); !ok {
		t.Fatal("host did not cache public audio")
	}
	data, _, err := r.combined(r.hostID())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(data, []byte("PRIVATE TITLE")) {
		t.Fatal("host shared excluded metadata")
	}
}

func TestExclusionsCanTargetGeneratedTreesBeforeStripping(t *testing.T) {
	_, data, _, _ := excludedSource(t)
	data = bytes.ReplaceAll(data, []byte(`Name="Jay"`), []byte(`Name="RekordLink"`))
	clean, err := preparePublishedSnapshot(data, []string{"/RekordLink/Set"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(clean, []byte("PRIVATE TITLE")) {
		t.Fatal("managed-root stripping bypassed track exclusion")
	}
}

func TestSharedContributionPublishesAsDuoLibrary(t *testing.T) {
	source, original, _, _ := excludedSource(t)
	roomState, err := newRelayRoom(filepath.Join(t.TempDir(), "room.json"))
	if err != nil {
		t.Fatal(err)
	}
	id, token, err := roomState.register("Sam", roomState.inviteCode())
	if err != nil {
		t.Fatal(err)
	}
	server := &apiServer{room: roomState, registerFails: map[string][]time.Time{}}
	httpServer := httptest.NewServer(server.handler())
	defer httpServer.Close()
	client := &syncClient{
		http:            httpServer.Client(),
		state:           clientState{Endpoint: httpServer.URL, PeerID: id, Token: token, Name: "Sam"},
		library:         source,
		sharedPlaylists: []string{"/Mine"},
	}
	if err := client.pushIfChanged(t.Context(), true); err != nil {
		t.Fatal(err)
	}
	data, _, err := roomState.combined(id)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := library.Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	root := lib.Playlists.Root.Nodes[0]
	if len(root.Nodes) < 1 || root.Nodes[0].Name != library.SharedPlaylistName || len(root.Nodes[0].Tracks) != 2 {
		t.Fatalf("generated Duo Library is wrong: %+v", root.Nodes)
	}
	if bytes.Contains(data, []byte(library.SharedContributionRootName)) {
		t.Fatal("internal contribution marker leaked into merged XML")
	}
	actual, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(actual, original) {
		t.Fatal("source library changed")
	}
}

func TestSharedContributionRequiresUpdatedMergeServer(t *testing.T) {
	for _, supported := range []bool{false, true} {
		t.Run(map[bool]string{false: "old", true: "updated"}[supported], func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				if supported {
					_, _ = w.Write([]byte(`{"ok":true,"shared_playlists":true}`))
				} else {
					_, _ = w.Write([]byte(`{"ok":true,"version":"0.4.5"}`))
				}
			}))
			defer server.Close()
			client := &syncClient{http: server.Client(), state: clientState{Endpoint: server.URL}}
			err := client.requireSharedPlaylistSupport(t.Context())
			if supported && err != nil {
				t.Fatalf("updated server rejected: %v", err)
			}
			if !supported && err == nil {
				t.Fatal("old merge server accepted shared contribution")
			}
		})
	}
}
