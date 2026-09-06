package service

import (
	"strings"
	"testing"
)

func TestRenderLaunchAgentEscapesPaths(t *testing.T) {
	got := renderLaunchAgent(`/Applications/A&B/rekordlink`, `/tmp/a<b.json`, `/tmp/a>b.log`)
	for _, want := range []string{`A&amp;B`, `a&lt;b.json`, `a&gt;b.log`, `<key>KeepAlive</key><true/>`} {
		if !strings.Contains(got, want) {
			t.Fatalf("launch agent missing %q: %s", want, got)
		}
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	got := renderSystemdUnit(`/opt/Rekord Link/rekordlink`, `/home/dj/.config/RekordLink/service.json`)
	if !strings.Contains(got, `ExecStart="/opt/Rekord Link/rekordlink" _service_run`) || !strings.Contains(got, `Restart=on-failure`) {
		t.Fatalf("bad unit: %s", got)
	}
}
