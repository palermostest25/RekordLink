package ui

import (
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/rekordlink/rekordlink/internal/library"
	"github.com/rekordlink/rekordlink/internal/service"
	rlsync "github.com/rekordlink/rekordlink/internal/sync"
)

// RunNativeCommand is the JSON bridge used by the native macOS application.
// It deliberately uses standard input/output so the UI does not need a local
// HTTP server and the bridge remains private to the bundled executable.
func RunNativeCommand(action string, input io.Reader, output io.Writer, version string) error {
	encode := func(value any) error { return json.NewEncoder(output).Encode(value) }
	decode := func(value any) error {
		decoder := json.NewDecoder(io.LimitReader(input, maxRequestBytes+1))
		decoder.DisallowUnknownFields()
		return decoder.Decode(value)
	}

	switch action {
	case "state":
		state, err := nativeState(version)
		if err != nil {
			return err
		}
		return encode(state)
	case "logs":
		logs, err := service.ReadLogs(128 << 10)
		if err != nil {
			return err
		}
		return encode(map[string]string{"logs": logs})
	case "invite":
		invite, err := nativeInvite()
		if err != nil {
			return err
		}
		return encode(map[string]string{"invite": invite})
	case "playlists":
		var request struct {
			Library string `json:"library"`
		}
		if err := decode(&request); err != nil {
			return err
		}
		playlists, err := nativePlaylists(request.Library)
		if err != nil {
			return err
		}
		return encode(map[string]any{"playlists": playlists})
	case "setup":
		var request setupRequest
		if err := decode(&request); err != nil {
			return err
		}
		command, args, err := buildServiceArgs(request)
		if err != nil {
			return err
		}
		if command == "join" && strings.TrimSpace(request.Invite) == "" {
			paths, _ := service.CurrentPaths()
			if old, loadErr := service.Load(paths.Config); loadErr == nil && old.Command == "join" {
				if invite := option(old.Args, "--invite"); invite != "" {
					args = append(args, "--invite", invite)
				}
			}
			if option(args, "--invite") == "" {
				return errors.New("a join invite is required")
			}
			inviteAudio, inviteErr := rlsync.InviteAudio(option(args, "--invite"))
			if inviteErr != nil || inviteAudio != request.AllowAudio {
				return errors.New("audio synchronization must match the saved host or relay invite")
			}
		}
		if _, err := service.Install(command, args); err != nil {
			return err
		}
		return encode(map[string]string{"message": "Configuration saved and background sync started."})
	case "control":
		var request struct {
			Action string `json:"action"`
		}
		if err := decode(&request); err != nil {
			return err
		}
		if request.Action == "remove" {
			if _, err := service.Uninstall(); err != nil {
				return err
			}
		} else if err := service.Control(request.Action); err != nil {
			return err
		}
		return encode(map[string]string{"message": "Background sync " + request.Action + " completed."})
	case "repair":
		paths, err := service.CurrentPaths()
		if err != nil {
			return err
		}
		cfg, err := service.Load(paths.Config)
		if errors.Is(err, os.ErrNotExist) {
			return encode(map[string]bool{"repaired": false})
		}
		if err != nil {
			return err
		}
		if _, err := service.Install(cfg.Command, cfg.Args); err != nil {
			return err
		}
		return encode(map[string]bool{"repaired": true})
	default:
		return errors.New("unknown native UI action")
	}
}

func nativeState(version string) (stateResponse, error) {
	runtimeState, err := service.Runtime()
	if err != nil {
		return stateResponse{}, err
	}
	response := stateResponse{Version: version, Service: runtimeState, Receipt: receiptView{State: "WAITING", Message: "Waiting for the first complete synchronization."}}
	paths, pathErr := service.CurrentPaths()
	if pathErr == nil {
		if cfg, loadErr := service.Load(paths.Config); loadErr == nil {
			view := viewConfig(cfg)
			response.Config = &view
			if view.Output != "" {
				response.Receipt = loadReceipt(view.Output)
			}
		}
	}
	return response, nil
}

func nativeInvite() (string, error) {
	paths, err := service.CurrentPaths()
	if err != nil {
		return "", err
	}
	cfg, err := service.Load(paths.Config)
	if err != nil {
		return "", errors.New("the background service is not configured")
	}
	if cfg.Command != "host" && cfg.Command != "relay" {
		return "", errors.New("only a host or relay creates an invite")
	}
	stateDir := option(cfg.Args, "--state")
	if stateDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			return "", err
		}
		stateDir = filepath.Join(configDir, "RekordLink", cfg.Command)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "invite.txt"))
	if err != nil {
		return "", errors.New("invite is not ready yet; start sync and try again")
	}
	invite := strings.TrimSpace(string(b))
	if len(invite) > 16<<10 || rlsync.ValidateInvite(invite) != nil {
		return "", errors.New("saved invite is invalid")
	}
	return invite, nil
}

func nativePlaylists(path string) ([]library.PlaylistPath, error) {
	absolute, err := absolutePath(path)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(absolute)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if err != nil {
		return nil, err
	}
	if len(data) > 128<<20 {
		return nil, errors.New("library XML exceeds the 128 MiB safety limit")
	}
	lib, err := library.Parse(data)
	if err != nil {
		return nil, err
	}
	return library.PlaylistPaths(lib), nil
}
