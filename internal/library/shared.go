package library

import (
	"fmt"
	"strings"
)

// SharedContributionRootName is an internal transport marker. Updated merge
// servers consume it and never expose it in the generated rekordbox tree.
const SharedContributionRootName = "RekordLink Shared Contributions"

// SharedPlaylistName is the single generated playlist containing the union of
// both DJs' selected contribution playlists.
const SharedPlaylistName = "Duo Library"

// PrepareSharedPlaylistsXML moves selected local folders/playlists into an
// internal contribution marker. Moving rather than copying keeps the generated
// output focused: contributors retain their editable source playlist locally,
// while both DJs receive one combined Duo Library playlist.
//
// A missing path fails closed. Generated RekordLink trees cannot be selected,
// since they are imported output rather than an editable source of truth.
func PrepareSharedPlaylistsXML(data []byte, paths []string) ([]byte, error) {
	lib, err := Parse(data)
	if err != nil {
		return nil, err
	}
	selected := make(map[string]bool, len(paths))
	for _, path := range paths {
		selected[path] = true
	}
	// Selecting a folder already includes every descendant. Treat an explicitly
	// selected child as redundant instead of failing after its parent is moved.
	for path := range selected {
		for parent := range selected {
			if path != parent && strings.HasPrefix(path, parent+"/") {
				delete(selected, path)
				break
			}
		}
	}
	found := make(map[string]bool, len(selected))
	contributions := make([]Node, 0, len(selected))
	removedMarker := false
	invalid := ""

	var prune func([]Node, string, bool) []Node
	prune = func(nodes []Node, parent string, insideManaged bool) []Node {
		kept := make([]Node, 0, len(nodes))
		for _, node := range nodes {
			path := playlistPath(parent, node.Name)
			marker := parent == "" && node.Type == "0" && strings.EqualFold(strings.TrimSpace(node.Name), SharedContributionRootName)
			managed := insideManaged || (parent == "" && node.Type == "0" && isManagedPlaylistRootName(node.Name))
			if selected[path] {
				found[path] = true
				if marker || managed {
					invalid = path
				} else {
					contributions = append(contributions, node)
				}
				continue
			}
			if marker {
				removedMarker = true
				continue
			}
			node.Nodes = prune(node.Nodes, path, managed)
			kept = append(kept, node)
		}
		return kept
	}
	lib.Playlists.Root.Nodes = prune(lib.Playlists.Root.Nodes, "", false)
	if invalid != "" {
		return nil, fmt.Errorf("shared contribution %q is inside generated RekordLink output; choose an original editable playlist", invalid)
	}
	for path := range selected {
		if !found[path] {
			return nil, fmt.Errorf("shared contribution folder/playlist %q is missing from the XML export; update the export or review shared-library settings", path)
		}
	}
	if len(contributions) == 0 && !removedMarker {
		return data, nil
	}
	if len(contributions) > 0 {
		lib.Playlists.Root.Nodes = append(lib.Playlists.Root.Nodes, Node{
			Type:  "0",
			Name:  SharedContributionRootName,
			Nodes: contributions,
		})
	}
	return Marshal(lib)
}

func splitSharedContributionRoots(nodes []Node) (ordinary, contributions []Node) {
	ordinary = make([]Node, 0, len(nodes))
	for _, node := range nodes {
		if node.Type == "0" && strings.EqualFold(strings.TrimSpace(node.Name), SharedContributionRootName) {
			contributions = append(contributions, node.Nodes...)
			continue
		}
		ordinary = append(ordinary, node)
	}
	return ordinary, contributions
}

func collectPlaylistRefs(node Node, visit func(PlaylistTrack)) {
	for _, ref := range node.Tracks {
		visit(ref)
	}
	for _, child := range node.Nodes {
		collectPlaylistRefs(child, visit)
	}
}
