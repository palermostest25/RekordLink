package library

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"path/filepath"
	"sort"
	"strings"
)

type Snapshot struct {
	PeerID   string
	PeerName string
	Library  *Library
}

type trackVersion struct {
	peerID string
	track  Track
}

// Merge builds a non-destructive union. Each DJ keeps a namespaced copy of
// their playlist tree. Identical tracks are de-duplicated, and the requester's
// Location wins so a matching local file remains playable after import.
func Merge(snapshots []Snapshot, requesterID string) (*Library, error) {
	if len(snapshots) == 0 {
		return nil, errors.New("cannot merge an empty room")
	}
	sort.SliceStable(snapshots, func(i, j int) bool { return snapshots[i].PeerID < snapshots[j].PeerID })

	versions := map[string][]trackVersion{}
	identityByPeerTrack := map[string]string{}
	for _, snapshot := range snapshots {
		if snapshot.Library == nil {
			continue
		}
		for _, track := range snapshot.Library.Collection.Tracks {
			identity := TrackIdentity(track)
			versions[identity] = append(versions[identity], trackVersion{peerID: snapshot.PeerID, track: track})
			identityByPeerTrack[snapshot.PeerID+"\x00"+track.TrackID] = identity
		}
	}

	identities := make([]string, 0, len(versions))
	for identity := range versions {
		identities = append(identities, identity)
	}
	sort.Strings(identities)

	merged := &Library{Version: "1.0.0"}
	merged.Playlists.Root = Node{Type: "0", Name: "ROOT"}
	mergedID := map[string]string{}
	for i, identity := range identities {
		id := itoa(i + 1)
		mergedID[identity] = id
		candidate := versions[identity][len(versions[identity])-1].track
		for _, version := range versions[identity] {
			if version.peerID == requesterID {
				// A local version wins as an atomic record. XML has only one set
				// of cues/grids per collection track; silently overlaying a
				// collaborator's version would be destructive on import.
				candidate = version.track
				break
			}
		}
		candidate.TrackID = id
		merged.Collection.Tracks = append(merged.Collection.Tracks, candidate)
	}

	rekordLinkFolder := Node{Type: "0", Name: ManagedPlaylistRootName}
	sharedTracks := []PlaylistTrack{}
	sharedSeen := map[string]bool{}
	hasSharedContributions := false
	for _, snapshot := range snapshots {
		if snapshot.Library == nil {
			continue
		}
		peerFolder := Node{Type: "0", Name: safeName(snapshot.PeerName)}
		// Defend against snapshots produced by older clients or retained by an
		// older relay. Generated RekordLink trees are derived output, never new
		// source material.
		children, _ := originalPlaylistRoots(snapshot.Library.Playlists.Root.Nodes)
		children, contributions := splitSharedContributionRoots(children)
		hasSharedContributions = hasSharedContributions || len(contributions) > 0
		for _, child := range children {
			peerFolder.Nodes = append(peerFolder.Nodes, remapNode(child, snapshot.PeerID, identityByPeerTrack, mergedID))
		}
		rekordLinkFolder.Nodes = append(rekordLinkFolder.Nodes, peerFolder)
		for _, contribution := range contributions {
			collectPlaylistRefs(contribution, func(ref PlaylistTrack) {
				identity := identityByPeerTrack[snapshot.PeerID+"\x00"+ref.Key]
				id := mergedID[identity]
				if id != "" && !sharedSeen[id] {
					sharedSeen[id] = true
					sharedTracks = append(sharedTracks, PlaylistTrack{Key: id})
				}
			})
		}
	}
	if hasSharedContributions {
		rekordLinkFolder.Nodes = append([]Node{{Type: "1", Name: SharedPlaylistName, KeyType: "0", Tracks: sharedTracks}}, rekordLinkFolder.Nodes...)
	}
	merged.Playlists.Root.Nodes = []Node{rekordLinkFolder}
	return merged, nil
}

func remapNode(node Node, peerID string, identities, mergedIDs map[string]string) Node {
	copyNode := node
	copyNode.Nodes = nil
	copyNode.Tracks = nil
	for _, child := range node.Nodes {
		copyNode.Nodes = append(copyNode.Nodes, remapNode(child, peerID, identities, mergedIDs))
	}
	for _, ref := range node.Tracks {
		identity := identities[peerID+"\x00"+ref.Key]
		if id := mergedIDs[identity]; id != "" {
			copyNode.Tracks = append(copyNode.Tracks, PlaylistTrack{Key: id})
		}
	}
	copyNode.KeyType = "0"
	return copyNode
}

// TrackIdentity intentionally excludes Location because two DJs usually keep
// the same audio at different paths. Size and duration reduce false matches;
// title and artist make the value stable when path names differ.
func TrackIdentity(track Track) string {
	if parsed, err := url.Parse(track.Location); err == nil && parsed.Scheme == "rekordlink" && parsed.Host == "sha256" {
		hash := strings.TrimPrefix(parsed.Path, "/")
		if len(hash) == 64 {
			if decoded, err := hex.DecodeString(hash); err == nil && len(decoded) == sha256.Size {
				return "content:" + strings.ToLower(hash)
			}
		}
	}
	name := normalize(track.Name)
	artist := normalize(track.Artist)
	base := normalize(locationBase(track.Location))
	material := strings.Join([]string{name, artist, track.TotalTime, track.Size, base}, "\x1f")
	if name != "" && artist != "" {
		material = strings.Join([]string{name, artist, track.TotalTime, track.Size}, "\x1f")
	}
	sum := sha256.Sum256([]byte(material))
	return hex.EncodeToString(sum[:])
}

func normalize(s string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(s))), " ")
}

func locationBase(location string) string {
	parsed, err := url.Parse(location)
	if err == nil && parsed.Path != "" {
		return filepath.Base(parsed.Path)
	}
	return filepath.Base(location)
}

func safeName(name string) string {
	name = strings.TrimSpace(strings.ReplaceAll(strings.ReplaceAll(name, "/", "-"), "\\", "-"))
	if name == "" {
		return "DJ"
	}
	return name
}

func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for v > 0 {
		i--
		buf[i] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[i:])
}
