package sync

import (
	"testing"

	"github.com/rekordlink/rekordlink/internal/library"
)

func TestCombinedUsesWeakETagComparisonAndStablePeerOrder(t *testing.T) {
	aXML := pollTestXML(t, "A")
	bXML := pollTestXML(t, "B")
	r := &room{diskRoom: diskRoom{
		RoomID:   "room",
		PairCode: "123456789012",
		Revision: 4,
		Peers: map[string]*peer{
			"b": {ID: "b", Name: "Zed", XML: bXML},
			"a": {ID: "a", Name: "Alice", XML: aXML},
		},
	}}

	b, etag, notModified, err := r.combinedIfChanged("a", "")
	if err != nil {
		t.Fatal(err)
	}
	if notModified || etag != `"room-4"` {
		t.Fatalf("notModified=%v etag=%q", notModified, etag)
	}
	merged, err := library.Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(merged.Playlists.Root.Nodes) != 1 || len(merged.Playlists.Root.Nodes[0].Nodes) != 2 {
		t.Fatalf("unexpected merged playlist tree: %#v", merged.Playlists.Root.Nodes)
	}
	peers := merged.Playlists.Root.Nodes[0].Nodes
	if peers[0].Name != "Alice" || peers[1].Name != "Zed" {
		t.Fatalf("peer order = %q, %q", peers[0].Name, peers[1].Name)
	}

	b, returnedETag, notModified, err := r.combinedIfChanged("a", `"old", W/"room-4"`)
	if err != nil {
		t.Fatal(err)
	}
	if !notModified || b != nil || returnedETag != etag {
		t.Fatalf("weak validator was not honored: notModified=%v etag=%q body=%d", notModified, returnedETag, len(b))
	}
}
