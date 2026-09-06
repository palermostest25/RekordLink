package sync

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/rekordlink/rekordlink/internal/library"
)

type peer struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	TokenHash string    `json:"token_hash,omitempty"`
	UpdatedAt time.Time `json:"updated_at"`
	XML       []byte    `json:"xml,omitempty"`
}

type diskRoom struct {
	RoomID   string           `json:"room_id"`
	PairCode string           `json:"pair_code"`
	Revision uint64           `json:"revision"`
	Peers    map[string]*peer `json:"peers"`
}

type room struct {
	mu       sync.RWMutex
	path     string
	diskRoom diskRoom
}

type status struct {
	RoomID   string       `json:"room_id"`
	Revision uint64       `json:"revision"`
	Peers    []peerStatus `json:"peers"`
}

type peerStatus struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	UpdatedAt time.Time `json:"updated_at"`
	Tracks    int       `json:"tracks"`
	Playlists int       `json:"playlists"`
}

func newRoom(path, hostName string) (*room, error) {
	if existing, err := loadRoom(path); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	hostID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	pairCode, err := numericCode(12)
	if err != nil {
		return nil, err
	}
	r := &room{path: path, diskRoom: diskRoom{
		RoomID:   hostID,
		PairCode: pairCode,
		Peers: map[string]*peer{
			hostID: {ID: hostID, Name: hostName},
		},
	}}
	return r, r.saveLocked()
}

func newRelayRoom(path string) (*room, error) {
	if existing, err := loadRoom(path); err == nil {
		return existing, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}
	roomID, err := randomHex(16)
	if err != nil {
		return nil, err
	}
	pairCode, err := numericCode(12)
	if err != nil {
		return nil, err
	}
	r := &room{path: path, diskRoom: diskRoom{RoomID: roomID, PairCode: pairCode, Peers: make(map[string]*peer)}}
	return r, r.saveLocked()
}

func loadRoom(path string) (*room, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var data diskRoom
	if err := json.Unmarshal(b, &data); err != nil {
		return nil, fmt.Errorf("read room state: %w", err)
	}
	if data.RoomID == "" || data.PairCode == "" || data.Peers == nil {
		return nil, errors.New("room state is incomplete")
	}
	return &room{path: path, diskRoom: data}, nil
}

func (r *room) register(name, pairCode string) (string, string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if subtleEqual(pairCode, r.diskRoom.PairCode) == false {
		return "", "", errors.New("pairing code is invalid")
	}
	for _, p := range r.diskRoom.Peers {
		if p.TokenHash != "" && p.Name == name {
			return "", "", errors.New("this DJ is already paired; reuse the saved join state")
		}
	}
	if len(r.diskRoom.Peers) >= 2 {
		return "", "", errors.New("this v1 room already has two DJs")
	}
	id, err := randomHex(16)
	if err != nil {
		return "", "", err
	}
	token, err := randomHex(32)
	if err != nil {
		return "", "", err
	}
	r.diskRoom.Peers[id] = &peer{ID: id, Name: name, TokenHash: hashString(token)}
	r.diskRoom.Revision++
	if err := r.saveLocked(); err != nil {
		delete(r.diskRoom.Peers, id)
		return "", "", err
	}
	return id, token, nil
}

func (r *room) authenticate(peerID, token string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p := r.diskRoom.Peers[peerID]
	return p != nil && p.TokenHash != "" && subtleEqual(p.TokenHash, hashString(token))
}

func (r *room) update(peerID, name string, data []byte) error {
	if _, err := library.Parse(data); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	p := r.diskRoom.Peers[peerID]
	if p == nil {
		return errors.New("unknown peer")
	}
	if name != "" {
		p.Name = name
	}
	p.XML = append([]byte(nil), data...)
	p.UpdatedAt = time.Now().UTC()
	r.diskRoom.Revision++
	return r.saveLocked()
}

func (r *room) combined(requesterID string) ([]byte, string, error) {
	b, etag, _, err := r.combinedIfChanged(requesterID, "")
	return b, etag, err
}

func (r *room) combinedIfChanged(requesterID, validator string) ([]byte, string, bool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	etag := fmt.Sprintf("\"%s-%d\"", r.diskRoom.RoomID, r.diskRoom.Revision)
	hasSnapshot := false
	for _, p := range r.diskRoom.Peers {
		if len(p.XML) > 0 {
			hasSnapshot = true
			break
		}
	}
	if !hasSnapshot {
		return nil, "", false, errors.New("no peer has published a library yet")
	}
	if weakETagMatches(validator, etag) {
		return nil, etag, true, nil
	}
	snapshots := make([]library.Snapshot, 0, len(r.diskRoom.Peers))
	for _, p := range r.diskRoom.Peers {
		if len(p.XML) == 0 {
			continue
		}
		lib, err := library.Parse(p.XML)
		if err != nil {
			return nil, "", false, err
		}
		snapshots = append(snapshots, library.Snapshot{PeerID: p.ID, PeerName: p.Name, Library: lib})
	}
	sort.Slice(snapshots, func(i, j int) bool { return snapshots[i].PeerID < snapshots[j].PeerID })
	merged, err := library.Merge(snapshots, requesterID)
	if err != nil {
		return nil, "", false, err
	}
	data, err := library.Marshal(merged)
	return data, etag, false, err
}

func (r *room) status() status {
	r.mu.RLock()
	defer r.mu.RUnlock()
	s := status{RoomID: r.diskRoom.RoomID, Revision: r.diskRoom.Revision}
	for _, p := range r.diskRoom.Peers {
		ps := peerStatus{ID: p.ID, Name: p.Name, UpdatedAt: p.UpdatedAt}
		if lib, err := library.Parse(p.XML); err == nil {
			ps.Tracks = len(lib.Collection.Tracks)
			ps.Playlists = library.PlaylistCount(lib.Playlists.Root)
		}
		s.Peers = append(s.Peers, ps)
	}
	sort.Slice(s.Peers, func(i, j int) bool { return s.Peers[i].ID < s.Peers[j].ID })
	return s
}

func weakETagMatches(header, current string) bool {
	current = normalizeETag(current)
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || (candidate != "" && normalizeETag(candidate) == current) {
			return true
		}
	}
	return false
}

func normalizeETag(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && strings.EqualFold(value[:2], "W/") {
		value = strings.TrimSpace(value[2:])
	}
	return value
}

func (r *room) hostID() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	for id, p := range r.diskRoom.Peers {
		if p.TokenHash == "" {
			return id
		}
	}
	return r.diskRoom.RoomID
}

func (r *room) inviteCode() string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.diskRoom.PairCode
}

func (r *room) saveLocked() error {
	b, err := json.MarshalIndent(r.diskRoom, "", "  ")
	if err != nil {
		return err
	}
	return WriteAtomic(r.path, append(b, '\n'), 0o600)
}

func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rekordlink-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func randomHex(bytes int) (string, error) {
	b := make([]byte, bytes)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func numericCode(length int) (string, error) {
	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	for i := range b {
		b[i] = '0' + b[i]%10
	}
	return string(b), nil
}

func hashString(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

func subtleEqual(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var different byte
	for i := range len(a) {
		different |= a[i] ^ b[i]
	}
	return different == 0
}
