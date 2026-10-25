package service

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderLaunchAgentEscapesPaths(t *testing.T) {
	got := renderLaunchAgent(`/Applications/A&B/rekordlink`, `/tmp/a<b.json`, `/tmp/a>b.log`)
	for _, want := range []string{`A&amp;B`, `a&lt;b.json`, `a&gt;b.log`, `<key>SuccessfulExit</key><false/>`, `<key>ThrottleInterval</key><integer>300</integer>`} {
		if !strings.Contains(got, want) {
			t.Fatalf("launch agent missing %q: %s", want, got)
		}
	}
}

func TestBundledAppExecutable(t *testing.T) {
	root := t.TempDir()
	app := root + "/RekordLink.app"
	engine := app + "/Contents/Resources/rekordlink"
	wrapper := app + "/Contents/MacOS/RekordLinkMenu"
	if err := os.MkdirAll(filepath.Dir(wrapper), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(wrapper, []byte("app"), 0o700); err != nil {
		t.Fatal(err)
	}
	if got := bundledAppExecutable(engine); got != wrapper {
		t.Fatalf("got %q, want %q", got, wrapper)
	}
	if got := bundledAppExecutable(root + "/rekordlink"); got != "" {
		t.Fatalf("non-bundled executable returned %q", got)
	}
}

func TestRenderSystemdUnit(t *testing.T) {
	got := renderSystemdUnit(`/opt/Rekord Link/rekordlink`, `/home/dj/.config/RekordLink/service.json`)
	if !strings.Contains(got, `ExecStart="/opt/Rekord Link/rekordlink" _service_run`) || !strings.Contains(got, `Restart=on-failure`) {
		t.Fatalf("bad unit: %s", got)
	}
}
