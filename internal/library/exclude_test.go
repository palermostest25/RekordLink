package library

import (
	"bytes"
	"reflect"
	"strings"
	"testing"
)

func exclusionFixture(t *testing.T) []byte {
	t.Helper()
	lib := &Library{Collection: Collection{Tracks: []Track{
		{TrackID: "1", Name: "Private", Location: "file://localhost/private.mp3"},
		{TrackID: "2", Name: "Public", Location: "file://localhost/public.mp3"},
		{TrackID: "3", Name: "Unfiled", Location: "file://localhost/unfiled.mp3"},
		{TrackID: "4", Name: "Same file, different record", Location: "file://localhost/private.mp3"},
	}}, Playlists: Playlists{Root: Node{Type: "0", Name: "ROOT", Nodes: []Node{
		{Type: "0", Name: "Jay", Nodes: []Node{{Type: "0", Name: "Sets", Nodes: []Node{{Type: "1", Name: "Private", Tracks: []PlaylistTrack{{Key: "1"}}}}}}},
		{Type: "1", Name: "Mine", Tracks: []PlaylistTrack{{Key: "1"}, {Key: "2"}, {Key: "4"}}},
		{Type: "0", Name: "Other", Nodes: []Node{{Type: "1", Name: "Private", Tracks: []PlaylistTrack{{Key: "2"}}}}},
	}}}}
	data, err := Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestExclusionWinsAcrossPlaylistsAndKeepsUnfiledTracks(t *testing.T) {
	input := exclusionFixture(t)
	original := bytes.Clone(input)
	for _, path := range []string{"/Jay", "/Jay/Sets", "/Jay/Sets/Private"} {
		t.Run(path, func(t *testing.T) {
			data, err := ExcludePlaylistsXML(input, []string{path})
			if err != nil {
				t.Fatal(err)
			}
			lib, err := Parse(data)
			if err != nil {
				t.Fatal(err)
			}
			if lib.Collection.Entries != 2 || lib.Collection.Tracks[0].TrackID != "2" || lib.Collection.Tracks[1].TrackID != "3" {
				t.Fatalf("unexpected tracks: %+v", lib.Collection)
			}
			for _, node := range PlaylistPaths(lib) {
				if node.Path == path {
					t.Fatalf("excluded node survived: %s", path)
				}
			}
			if strings.Contains(string(data), `Key="1"`) || strings.Contains(string(data), `Key="4"`) {
				t.Fatal("private references survived in another playlist")
			}
			if !strings.Contains(string(data), `Name="Other"`) {
				t.Fatal("different folder with same leaf name was excluded")
			}
		})
	}
	if !bytes.Equal(input, original) {
		t.Fatal("mutated source XML")
	}
}

func TestExclusionsDefaultAndFailClosed(t *testing.T) {
	input := exclusionFixture(t)
	data, err := ExcludePlaylistsXML(input, nil)
	if err != nil || !bytes.Equal(data, input) {
		t.Fatalf("no exclusions changed input: %v", err)
	}
	for _, paths := range [][]string{{"/Jya"}, {"/jay"}, {"Jay"}, {""}, {"/Jay", "/Missing"}} {
		if data, err := ExcludePlaylistsXML(input, paths); err == nil || data != nil {
			t.Fatalf("unmatched exclusion shared data: %v", paths)
		}
	}
	data, err = ExcludePlaylistsXML(input, []string{"/Jay", "/Jay/Sets/Private", "/Other/Private"})
	if err != nil {
		t.Fatal(err)
	}
	lib, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(lib.Collection.Tracks) != 1 || lib.Collection.Tracks[0].TrackID != "3" {
		t.Fatal("multiple exclusions did not leave only the unfiled track")
	}
}

func TestExclusionPathsEscapeFolderNames(t *testing.T) {
	lib := &Library{Playlists: Playlists{Root: Node{Type: "0", Name: "ROOT", Nodes: []Node{
		{Type: "0", Name: "A/B", Nodes: []Node{{Type: "1", Name: "~Private"}}},
		{Type: "0", Name: "A", Nodes: []Node{{Type: "1", Name: "B"}}},
	}}}}
	paths := PlaylistPaths(lib)
	got := []string{}
	for _, p := range paths {
		got = append(got, p.Path)
	}
	if !reflect.DeepEqual(got, []string{"/A~1B", "/A~1B/~0Private", "/A", "/A/B"}) {
		t.Fatalf("paths: %v", got)
	}
	data, err := Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	data, err = ExcludePlaylistsXML(data, []string{"/A~1B"})
	if err != nil {
		t.Fatal(err)
	}
	filtered, err := Parse(data)
	if err != nil {
		t.Fatal(err)
	}
	if len(filtered.Playlists.Root.Nodes) != 1 || filtered.Playlists.Root.Nodes[0].Name != "A" {
		t.Fatal("excluded a different nested path")
	}
}
