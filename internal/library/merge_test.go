package library

import (
	"os"
	"strings"
	"testing"
)

func fixture(t *testing.T, path string) *Library {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	return lib
}

func TestParseAndMarshal(t *testing.T) {
	lib := fixture(t, "testdata/dj-a.xml")
	if got := MarkerCount(lib); got != 1 {
		t.Fatalf("markers = %d, want 1", got)
	}
	b, err := Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "<?xml version=\"1.0\" encoding=\"UTF-8\"?>") {
		t.Fatalf("missing XML declaration: %s", b[:60])
	}
	if _, err := Parse(b); err != nil {
		t.Fatalf("round trip: %v", err)
	}
}

func TestMergeDeduplicatesAndUsesRequesterVersion(t *testing.T) {
	a := fixture(t, "testdata/dj-a.xml")
	b := fixture(t, "testdata/dj-b.xml")
	merged, err := Merge([]Snapshot{
		{PeerID: "a", PeerName: "DJ A", Library: a},
		{PeerID: "b", PeerName: "DJ B", Library: b},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := len(merged.Collection.Tracks); got != 3 {
		t.Fatalf("tracks = %d, want 3", got)
	}
	if got := PlaylistCount(merged.Playlists.Root); got != 2 {
		t.Fatalf("playlists = %d, want 2", got)
	}
	var shared *Track
	for i := range merged.Collection.Tracks {
		if merged.Collection.Tracks[i].Name == "First Track" {
			shared = &merged.Collection.Tracks[i]
		}
	}
	if shared == nil {
		t.Fatal("shared track missing")
	}
	if !strings.Contains(shared.Location, "/Users/a/") {
		t.Fatalf("requester path did not win: %s", shared.Location)
	}
	if len(shared.Markers) != 1 || shared.Markers[0].Name != "DROP" {
		t.Fatalf("requester cues did not win: %+v", shared.Markers)
	}
	out, err := Marshal(merged)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Parse(out); err != nil {
		t.Fatalf("merged XML invalid: %v", err)
	}
}

func TestTrackIdentityIgnoresDifferentPaths(t *testing.T) {
	a := Track{Name: "Song", Artist: "Artist", Size: "123", TotalTime: "200", Location: "file://localhost/a/song.mp3"}
	b := a
	b.Location = "file://localhost/b/song.mp3"
	if TrackIdentity(a) != TrackIdentity(b) {
		t.Fatal("same metadata at different paths should match")
	}
}

func TestTrackIdentityPrefersContentHash(t *testing.T) {
	hash := strings.Repeat("a", 64)
	a := Track{Name: "Different Tag A", Artist: "One", Location: "rekordlink://sha256/" + hash + "?name=a.mp3"}
	b := Track{Name: "Different Tag B", Artist: "Two", Location: "rekordlink://sha256/" + hash + "?name=b.wav"}
	if TrackIdentity(a) != TrackIdentity(b) {
		t.Fatal("same audio hash should match despite tag differences")
	}
}

func TestStripManagedPlaylistRoots(t *testing.T) {
	lib := &Library{Version: "1.0.0"}
	lib.Playlists.Root = Node{Type: "0", Name: "ROOT", Nodes: []Node{
		{Type: "0", Name: "My Music", Nodes: []Node{{Type: "1", Name: "Set"}}},
		{Type: "0", Name: "RekordLink", Nodes: []Node{{Type: "0", Name: "DJ B"}}},
		{Type: "0", Name: "rekordlink (2)", Nodes: []Node{{Type: "0", Name: "DJ B"}}},
		{Type: "0", Name: "RekordLink Ideas", Nodes: []Node{{Type: "1", Name: "Keep Me"}}},
	}}

	if got := StripManagedPlaylistRoots(lib); got != 2 {
		t.Fatalf("removed roots = %d, want 2", got)
	}
	if got := len(lib.Playlists.Root.Nodes); got != 2 {
		t.Fatalf("remaining roots = %d, want 2", got)
	}
	if lib.Playlists.Root.Nodes[0].Name != "My Music" || lib.Playlists.Root.Nodes[1].Name != "RekordLink Ideas" {
		t.Fatalf("wrong roots survived: %+v", lib.Playlists.Root.Nodes)
	}
}

func TestMergeDoesNotRepublishItsGeneratedPlaylistTree(t *testing.T) {
	a := fixture(t, "testdata/dj-a.xml")
	b := fixture(t, "testdata/dj-b.xml")
	first, err := Merge([]Snapshot{
		{PeerID: "a", PeerName: "DJ A", Library: a},
		{PeerID: "b", PeerName: "DJ B", Library: b},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}

	// Model both DJs importing the generated RekordLink folder into the next
	// rekordbox Auto Export while retaining their own original playlists.
	a = fixture(t, "testdata/dj-a.xml")
	b = fixture(t, "testdata/dj-b.xml")
	a.Playlists.Root.Nodes = append(a.Playlists.Root.Nodes, first.Playlists.Root.Nodes[0])
	b.Playlists.Root.Nodes = append(b.Playlists.Root.Nodes, first.Playlists.Root.Nodes[0])
	second, err := Merge([]Snapshot{
		{PeerID: "a", PeerName: "DJ A", Library: a},
		{PeerID: "b", PeerName: "DJ B", Library: b},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	if got := PlaylistCount(second.Playlists.Root); got != 2 {
		t.Fatalf("playlists after feedback = %d, want 2", got)
	}
	for _, peer := range second.Playlists.Root.Nodes[0].Nodes {
		for _, child := range peer.Nodes {
			if isManagedPlaylistRootName(child.Name) {
				t.Fatalf("generated tree was nested under %q", peer.Name)
			}
		}
	}
}
