package library

import (
	"strings"
	"testing"
)

func sharedFixture() *Library {
	return &Library{
		Collection: Collection{Tracks: []Track{
			{TrackID: "1", Name: "Mine", Artist: "A", Location: "file://localhost/mine.mp3"},
			{TrackID: "2", Name: "Other", Artist: "B", Location: "file://localhost/other.mp3"},
		}},
		Playlists: Playlists{Root: Node{
			Type: "0",
			Name: "ROOT",
			Nodes: []Node{{
				Type: "0",
				Name: "Sets",
				Nodes: []Node{
					{Type: "1", Name: "Duo Contributions", Tracks: []PlaylistTrack{{Key: "1"}}},
					{Type: "1", Name: "Personal", Tracks: []PlaylistTrack{{Key: "2"}}},
				},
			}},
		}},
	}
}

func TestPrepareSharedPlaylistsMovesSelectedSourceIntoMarker(t *testing.T) {
	data, err := Marshal(sharedFixture())
	if err != nil {
		t.Fatal(err)
	}
	out, err := PrepareSharedPlaylistsXML(data, []string{"/Sets/Duo Contributions"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Playlists.Root.Nodes) != 2 {
		t.Fatalf("top-level nodes = %d, want 2", len(lib.Playlists.Root.Nodes))
	}
	sets := lib.Playlists.Root.Nodes[0]
	if len(sets.Nodes) != 1 || sets.Nodes[0].Name != "Personal" {
		t.Fatalf("selected source survived in ordinary tree: %+v", sets.Nodes)
	}
	marker := lib.Playlists.Root.Nodes[1]
	if marker.Name != SharedContributionRootName || len(marker.Nodes) != 1 || marker.Nodes[0].Name != "Duo Contributions" {
		t.Fatalf("contribution marker is wrong: %+v", marker)
	}
	if len(lib.Collection.Tracks) != 2 {
		t.Fatal("preparing a contribution removed collection tracks")
	}
}

func TestPrepareSharedPlaylistsAllowsRedundantDescendantSelection(t *testing.T) {
	data, err := Marshal(sharedFixture())
	if err != nil {
		t.Fatal(err)
	}
	out, err := PrepareSharedPlaylistsXML(data, []string{"/Sets", "/Sets/Duo Contributions"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Playlists.Root.Nodes) != 1 || lib.Playlists.Root.Nodes[0].Name != SharedContributionRootName {
		t.Fatalf("folder contribution was not moved once: %+v", lib.Playlists.Root.Nodes)
	}
}

func TestPrepareSharedPlaylistsFailsClosedAndStripsSpoofedMarker(t *testing.T) {
	data, err := Marshal(sharedFixture())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSharedPlaylistsXML(data, []string{"/Renamed"}); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("missing contribution was accepted: %v", err)
	}

	lib := sharedFixture()
	lib.Playlists.Root.Nodes = append(lib.Playlists.Root.Nodes, Node{Type: "0", Name: SharedContributionRootName, Nodes: []Node{{Type: "1", Name: "Spoof", Tracks: []PlaylistTrack{{Key: "2"}}}}})
	data, err = Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	out, err := PrepareSharedPlaylistsXML(data, nil)
	if err != nil {
		t.Fatal(err)
	}
	clean, err := Parse(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range clean.Playlists.Root.Nodes {
		if node.Name == SharedContributionRootName {
			t.Fatal("unconfigured contribution marker survived")
		}
	}
}

func TestPrepareSharedPlaylistsRejectsGeneratedOutput(t *testing.T) {
	lib := sharedFixture()
	lib.Playlists.Root.Nodes = append(lib.Playlists.Root.Nodes, Node{Type: "0", Name: ManagedPlaylistRootName, Nodes: []Node{{Type: "1", Name: SharedPlaylistName, Tracks: []PlaylistTrack{{Key: "1"}}}}})
	data, err := Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PrepareSharedPlaylistsXML(data, []string{"/RekordLink/Duo Library"}); err == nil || !strings.Contains(err.Error(), "generated") {
		t.Fatalf("generated output was accepted as a contribution: %v", err)
	}
}

func TestMergeBuildsOneDuoLibraryFromBothContributors(t *testing.T) {
	a := sharedFixture()
	b := sharedFixture()
	b.Collection.Tracks[0] = Track{TrackID: "9", Name: "Theirs", Artist: "C", Location: "file://localhost/theirs.mp3"}
	b.Playlists.Root.Nodes[0].Nodes[0].Tracks = []PlaylistTrack{{Key: "9"}, {Key: "2"}}

	prepare := func(lib *Library) *Library {
		t.Helper()
		data, err := Marshal(lib)
		if err != nil {
			t.Fatal(err)
		}
		data, err = PrepareSharedPlaylistsXML(data, []string{"/Sets/Duo Contributions"})
		if err != nil {
			t.Fatal(err)
		}
		prepared, err := Parse(data)
		if err != nil {
			t.Fatal(err)
		}
		return prepared
	}
	merged, err := Merge([]Snapshot{
		{PeerID: "a", PeerName: "Sam", Library: prepare(a)},
		{PeerID: "b", PeerName: "Jay", Library: prepare(b)},
	}, "a")
	if err != nil {
		t.Fatal(err)
	}
	root := merged.Playlists.Root.Nodes[0]
	if len(root.Nodes) != 3 || root.Nodes[0].Name != SharedPlaylistName || root.Nodes[0].Type != "1" {
		t.Fatalf("shared playlist missing from generated root: %+v", root.Nodes)
	}
	if len(root.Nodes[0].Tracks) != 3 {
		t.Fatalf("shared union has %d tracks, want 3", len(root.Nodes[0].Tracks))
	}
	for _, peer := range root.Nodes[1:] {
		for _, child := range peer.Nodes {
			if child.Name == "Duo Contributions" || child.Name == SharedContributionRootName {
				t.Fatalf("transport/source contribution leaked into %q", peer.Name)
			}
		}
	}
}
