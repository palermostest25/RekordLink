package library

import (
	"fmt"
	"strings"
)

// PlaylistPath identifies a node relative to the XML playlist root. Path uses
// JSON Pointer escaping (~0 for ~, ~1 for /) so names containing / stay distinct
// from nested folders. Labels are for display only, never matching.
type PlaylistPath struct {
	Path   string `json:"path"`
	Label  string `json:"label"`
	Folder bool   `json:"folder"`
}

func playlistPath(parent, name string) string {
	return parent + "/" + strings.NewReplacer("~", "~0", "/", "~1").Replace(name)
}

func PlaylistPaths(lib *Library) []PlaylistPath {
	paths := []PlaylistPath{}
	var visit func([]Node, string, string)
	visit = func(nodes []Node, parent, label string) {
		for _, node := range nodes {
			path := playlistPath(parent, node.Name)
			name := node.Name
			if label != "" {
				name = label + " › " + name
			}
			paths = append(paths, PlaylistPath{Path: path, Label: name, Folder: node.Type == "0"})
			visit(node.Nodes, path, name)
		}
	}
	visit(lib.Playlists.Root.Nodes, "", "")
	return paths
}

// ExcludePlaylistsXML is an outbound filter, applied before audio preparation.
// Exclusion wins over inclusion elsewhere: remove excluded track IDs, duplicate
// records pointing at the same Location, and all remaining references to them.
// Unfiled tracks remain shared. A missing configured path fails closed, so a
// renamed folder cannot silently become public. Call before stripping managed
// playlist roots, which may themselves contain a configured exclusion.
func ExcludePlaylistsXML(data []byte, paths []string) ([]byte, error) {
	if len(paths) == 0 {
		return data, nil
	}
	lib, err := Parse(data)
	if err != nil {
		return nil, err
	}
	available := map[string]bool{}
	for _, node := range PlaylistPaths(lib) {
		available[node.Path] = true
	}
	excluded := map[string]bool{}
	for _, path := range paths {
		if !available[path] {
			return nil, fmt.Errorf("excluded folder/playlist %q is missing from the XML export; update the export or review exclusions before sharing", path)
		}
		excluded[path] = true
	}
	blocked := map[string]bool{}
	var collect func(Node)
	collect = func(node Node) {
		for _, ref := range node.Tracks {
			blocked[ref.Key] = true
		}
		for _, child := range node.Nodes {
			collect(child)
		}
	}
	var prune func([]Node, string) []Node
	prune = func(nodes []Node, parent string) []Node {
		kept := make([]Node, 0, len(nodes))
		for _, node := range nodes {
			path := playlistPath(parent, node.Name)
			if excluded[path] {
				collect(node)
				continue
			}
			node.Nodes = prune(node.Nodes, path)
			kept = append(kept, node)
		}
		return kept
	}
	lib.Playlists.Root.Nodes = prune(lib.Playlists.Root.Nodes, "")
	locations := map[string]bool{}
	for _, track := range lib.Collection.Tracks {
		if blocked[track.TrackID] {
			locations[track.Location] = true
		}
	}
	tracks := make([]Track, 0, len(lib.Collection.Tracks))
	for _, track := range lib.Collection.Tracks {
		if blocked[track.TrackID] || locations[track.Location] {
			blocked[track.TrackID] = true
			continue
		}
		tracks = append(tracks, track)
	}
	lib.Collection.Tracks = tracks
	var pruneRefs func(*Node)
	pruneRefs = func(node *Node) {
		refs := make([]PlaylistTrack, 0, len(node.Tracks))
		for _, ref := range node.Tracks {
			if !blocked[ref.Key] {
				refs = append(refs, ref)
			}
		}
		node.Tracks = refs
		for i := range node.Nodes {
			pruneRefs(&node.Nodes[i])
		}
	}
	pruneRefs(&lib.Playlists.Root)
	return Marshal(lib)
}
