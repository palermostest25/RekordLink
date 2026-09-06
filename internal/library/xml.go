package library

import (
	"bytes"
	"encoding/xml"
	"errors"
	"fmt"
)

// Library models AlphaTheta's documented rekordbox XML playlist interchange
// format. Values are strings where the specification permits optional fields,
// which lets RekordLink preserve an omitted value without inventing one.
type Library struct {
	XMLName    xml.Name   `xml:"DJ_PLAYLISTS"`
	Version    string     `xml:"Version,attr"`
	Product    Product    `xml:"PRODUCT"`
	Collection Collection `xml:"COLLECTION"`
	Playlists  Playlists  `xml:"PLAYLISTS"`
}

type Product struct {
	Name    string `xml:"Name,attr,omitempty"`
	Version string `xml:"Version,attr,omitempty"`
	Company string `xml:"Company,attr,omitempty"`
}

type Collection struct {
	Entries int     `xml:"Entries,attr"`
	Tracks  []Track `xml:"TRACK"`
}

type Track struct {
	TrackID      string         `xml:"TrackID,attr"`
	Name         string         `xml:"Name,attr,omitempty"`
	Artist       string         `xml:"Artist,attr,omitempty"`
	Composer     string         `xml:"Composer,attr,omitempty"`
	Album        string         `xml:"Album,attr,omitempty"`
	Grouping     string         `xml:"Grouping,attr,omitempty"`
	Genre        string         `xml:"Genre,attr,omitempty"`
	Kind         string         `xml:"Kind,attr,omitempty"`
	Size         string         `xml:"Size,attr,omitempty"`
	TotalTime    string         `xml:"TotalTime,attr,omitempty"`
	DiscNumber   string         `xml:"DiscNumber,attr,omitempty"`
	TrackNumber  string         `xml:"TrackNumber,attr,omitempty"`
	Year         string         `xml:"Year,attr,omitempty"`
	AverageBpm   string         `xml:"AverageBpm,attr,omitempty"`
	DateModified string         `xml:"DateModified,attr,omitempty"`
	DateAdded    string         `xml:"DateAdded,attr,omitempty"`
	BitRate      string         `xml:"BitRate,attr,omitempty"`
	SampleRate   string         `xml:"SampleRate,attr,omitempty"`
	Comments     string         `xml:"Comments,attr,omitempty"`
	PlayCount    string         `xml:"PlayCount,attr,omitempty"`
	LastPlayed   string         `xml:"LastPlayed,attr,omitempty"`
	Rating       string         `xml:"Rating,attr,omitempty"`
	Location     string         `xml:"Location,attr"`
	Remixer      string         `xml:"Remixer,attr,omitempty"`
	Tonality     string         `xml:"Tonality,attr,omitempty"`
	Label        string         `xml:"Label,attr,omitempty"`
	Mix          string         `xml:"Mix,attr,omitempty"`
	Colour       string         `xml:"Colour,attr,omitempty"`
	Tempos       []Tempo        `xml:"TEMPO"`
	Markers      []PositionMark `xml:"POSITION_MARK"`
}

type Tempo struct {
	Inizio  string `xml:"Inizio,attr"`
	Bpm     string `xml:"Bpm,attr"`
	Metro   string `xml:"Metro,attr"`
	Battito string `xml:"Battito,attr"`
}

type PositionMark struct {
	Name  string `xml:"Name,attr,omitempty"`
	Type  string `xml:"Type,attr"`
	Start string `xml:"Start,attr"`
	End   string `xml:"End,attr,omitempty"`
	Num   string `xml:"Num,attr,omitempty"`
}

type Playlists struct {
	Root Node `xml:"NODE"`
}

type Node struct {
	Type    string          `xml:"Type,attr"`
	Name    string          `xml:"Name,attr"`
	Count   int             `xml:"Count,attr,omitempty"`
	Entries int             `xml:"Entries,attr,omitempty"`
	KeyType string          `xml:"KeyType,attr,omitempty"`
	Tracks  []PlaylistTrack `xml:"TRACK"`
	Nodes   []Node          `xml:"NODE"`
}

type PlaylistTrack struct {
	Key string `xml:"Key,attr"`
}

func Parse(data []byte) (*Library, error) {
	dec := xml.NewDecoder(bytes.NewReader(data))
	dec.Strict = true
	var lib Library
	if err := dec.Decode(&lib); err != nil {
		return nil, fmt.Errorf("invalid rekordbox XML: %w", err)
	}
	if lib.XMLName.Local != "DJ_PLAYLISTS" {
		return nil, errors.New("invalid rekordbox XML: root must be DJ_PLAYLISTS")
	}
	if len(lib.Collection.Tracks) > 0 && lib.Playlists.Root.Name == "" {
		return nil, errors.New("invalid rekordbox XML: PLAYLISTS root NODE is missing")
	}
	for _, track := range lib.Collection.Tracks {
		if track.TrackID == "" || track.Location == "" {
			return nil, errors.New("invalid rekordbox XML: every TRACK needs TrackID and Location")
		}
	}
	return &lib, nil
}

func Marshal(lib *Library) ([]byte, error) {
	lib.XMLName = xml.Name{Local: "DJ_PLAYLISTS"}
	lib.Version = "1.0.0"
	lib.Product = Product{Name: "RekordLink", Version: "1", Company: "RekordLink"}
	lib.Collection.Entries = len(lib.Collection.Tracks)
	normalizeNode(&lib.Playlists.Root)
	body, err := xml.MarshalIndent(lib, "", "  ")
	if err != nil {
		return nil, err
	}
	return append([]byte(xml.Header), append(body, '\n')...), nil
}

func normalizeNode(node *Node) {
	if node.Type == "0" {
		node.Count = len(node.Nodes)
		node.Entries = 0
		node.KeyType = ""
		node.Tracks = nil
		for i := range node.Nodes {
			normalizeNode(&node.Nodes[i])
		}
		return
	}
	node.Count = 0
	node.Entries = len(node.Tracks)
	if node.KeyType == "" {
		node.KeyType = "0"
	}
	node.Nodes = nil
}

func PlaylistCount(node Node) int {
	if node.Type == "1" {
		return 1
	}
	total := 0
	for _, child := range node.Nodes {
		total += PlaylistCount(child)
	}
	return total
}

func MarkerCount(lib *Library) int {
	total := 0
	for _, track := range lib.Collection.Tracks {
		total += len(track.Markers)
	}
	return total
}

func TempoCount(lib *Library) int {
	total := 0
	for _, track := range lib.Collection.Tracks {
		total += len(track.Tempos)
	}
	return total
}
