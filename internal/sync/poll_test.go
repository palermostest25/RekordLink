package sync

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/rekordlink/rekordlink/internal/library"
)

func TestPullNormalizesWeakProxyETagForLegacyRelay(t *testing.T) {
	xmlData := pollTestXML(t, "First")
	combinedCalls := 0
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/combined.xml":
			combinedCalls++
			w.Header().Set("ETag", `W/"room-1"`)
			if r.Header.Get("If-None-Match") == `"room-1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_, _ = w.Write(xmlData)
		case "/api/v1/status":
			statusCalls++
			http.Error(w, "status fallback should not be needed", http.StatusInternalServerError)
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := pollTestClient(t, server)
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if client.etag != `"room-1"` {
		t.Fatalf("normalized ETag = %q", client.etag)
	}
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if combinedCalls != 2 || statusCalls != 0 {
		t.Fatalf("combined calls = %d, status calls = %d", combinedCalls, statusCalls)
	}
}

func TestPullFallsBackToRevisionWhenProxyIgnoresConditionalRequest(t *testing.T) {
	firstXML := pollTestXML(t, "First")
	ignoredBody := pollTestXML(t, "Must not replace output")
	combinedCalls := 0
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/combined.xml":
			combinedCalls++
			w.Header().Set("ETag", `W/"room-7"`)
			if combinedCalls == 1 {
				_, _ = w.Write(firstXML)
			} else {
				_, _ = w.Write(ignoredBody)
			}
		case "/api/v1/status":
			statusCalls++
			_ = json.NewEncoder(w).Encode(status{RoomID: "room", Revision: 7})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := pollTestClient(t, server)
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if combinedCalls != 2 {
		t.Fatalf("combined body downloaded %d times, want 2", combinedCalls)
	}
	if statusCalls != 2 {
		t.Fatalf("status fallback calls = %d, want 2", statusCalls)
	}
	output, err := os.ReadFile(client.output)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := library.Parse(output)
	if err != nil {
		t.Fatal(err)
	}
	if got := parsed.Collection.Tracks[0].Name; got != "First" {
		t.Fatalf("unchanged revision replaced output with %q", got)
	}
}

func TestPullFallsBackToRevisionWhenProxyRemovesETag(t *testing.T) {
	xmlData := pollTestXML(t, "No ETag")
	combinedCalls := 0
	statusCalls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/combined.xml":
			combinedCalls++
			_, _ = w.Write(xmlData)
		case "/api/v1/status":
			statusCalls++
			_ = json.NewEncoder(w).Encode(status{RoomID: "room", Revision: 11})
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := pollTestClient(t, server)
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := client.pull(t.Context()); err != nil {
		t.Fatal(err)
	}
	if combinedCalls != 1 || statusCalls != 2 {
		t.Fatalf("combined calls = %d, status calls = %d", combinedCalls, statusCalls)
	}
}

func pollTestClient(t *testing.T, server *httptest.Server) *syncClient {
	t.Helper()
	return &syncClient{
		http:   server.Client(),
		state:  clientState{Endpoint: server.URL, PeerID: "peer", Token: "token", Name: "DJ"},
		output: filepath.Join(t.TempDir(), "rekordlink-shared.xml"),
	}
}

func pollTestXML(t *testing.T, trackName string) []byte {
	t.Helper()
	lib := &library.Library{Version: "1.0.0"}
	lib.Collection.Tracks = []library.Track{{TrackID: "1", Name: trackName, Location: "file://localhost/tmp/rekordlink-poll-test.mp3"}}
	lib.Playlists.Root = library.Node{Type: "0", Name: "ROOT"}
	b, err := library.Marshal(lib)
	if err != nil {
		t.Fatal(err)
	}
	return b
}
