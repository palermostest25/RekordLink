package ui

import (
	"context"
	"crypto/rand"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/rekordlink/rekordlink/internal/library"
	"github.com/rekordlink/rekordlink/internal/service"
	rlsync "github.com/rekordlink/rekordlink/internal/sync"
)

const maxRequestBytes = 64 << 10

//go:embed index.html
var indexHTML string

type Options struct {
	Listen      string
	OpenBrowser bool
	Version     string
}

type setupRequest struct {
	ExcludePlaylists []string `json:"exclude_playlists"`
	Mode             string   `json:"mode"`
	Name             string   `json:"name"`
	Library          string   `json:"library"`
	Output           string   `json:"output"`
	AudioRoot        string   `json:"audio_root"`
	AllowAudio       bool     `json:"allow_audio"`
	Invite           string   `json:"invite"`
	Listen           string   `json:"listen"`
	Advertise        string   `json:"advertise"`
	PublicEndpoint   string   `json:"public_endpoint"`
	Interval         string   `json:"interval"`
}

type configView struct {
	ExcludePlaylists []string `json:"exclude_playlists"`
	Mode             string   `json:"mode"`
	Name             string   `json:"name,omitempty"`
	Library          string   `json:"library,omitempty"`
	Output           string   `json:"output,omitempty"`
	AudioRoot        string   `json:"audio_root,omitempty"`
	AllowAudio       bool     `json:"allow_audio"`
	InviteConfigured bool     `json:"invite_configured"`
	Listen           string   `json:"listen,omitempty"`
	Advertise        string   `json:"advertise,omitempty"`
	PublicEndpoint   string   `json:"public_endpoint,omitempty"`
	Interval         string   `json:"interval,omitempty"`
}

type receiptView struct {
	State       string `json:"state"`
	Message     string `json:"message,omitempty"`
	Generated   string `json:"generated,omitempty"`
	Tracks      int    `json:"tracks"`
	Playlists   int    `json:"playlists"`
	AudioReady  int    `json:"audio_ready"`
	AudioTotal  int    `json:"audio_total"`
	RoomVersion string `json:"room_revision,omitempty"`
}

type stateResponse struct {
	Version string                `json:"version"`
	Service service.RuntimeStatus `json:"service"`
	Config  *configView           `json:"config,omitempty"`
	Receipt receiptView           `json:"receipt"`
}

type server struct {
	csrf    string
	version string
	host    string
}

func Run(ctx context.Context, opts Options) error {
	if opts.Listen == "" {
		opts.Listen = "127.0.0.1:9766"
	}
	if err := validateListen(opts.Listen); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", opts.Listen)
	if err != nil {
		return err
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		listener.Close()
		return err
	}
	s := &server{csrf: hex.EncodeToString(tokenBytes), version: opts.Version, host: listener.Addr().String()}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", s.index)
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("GET /api/logs", s.logs)
	mux.HandleFunc("GET /api/invite", s.invite)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/playlists", s.playlists)
	mux.HandleFunc("POST /api/control", s.control)
	mux.HandleFunc("POST /api/pick", s.pick)

	httpServer := &http.Server{
		Handler:           s.secure(mux),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       10 * time.Second,
		WriteTimeout:      15 * time.Second,
		IdleTimeout:       45 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
	}()
	url := "http://" + listener.Addr().String()
	log.Printf("management UI available at %s", url)
	if opts.OpenBrowser {
		if err := openURL(url); err != nil {
			log.Printf("open browser: %v", err)
		}
	}
	err = httpServer.Serve(listener)
	if errors.Is(err, http.ErrServerClosed) {
		return nil
	}
	return err
}

func validateListen(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid UI listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return errors.New("the management UI must listen on a loopback address")
	}
	return nil
}

func (s *server) secure(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		remoteHost, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil || !net.ParseIP(remoteHost).IsLoopback() {
			http.Error(w, "loopback access only", http.StatusForbidden)
			return
		}
		host, _, err := net.SplitHostPort(r.Host)
		if err != nil || (!strings.EqualFold(host, "localhost") && (net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback())) {
			http.Error(w, "invalid host", http.StatusForbidden)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; script-src 'unsafe-inline'; connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		if r.Method != http.MethodGet && r.Header.Get("X-RekordLink-CSRF") != s.csrf {
			http.Error(w, "invalid request token", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *server) index(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	page := strings.ReplaceAll(indexHTML, "{{CSRF}}", html.EscapeString(s.csrf))
	_, _ = io.WriteString(w, page)
}

func (s *server) state(w http.ResponseWriter, _ *http.Request) {
	runtimeState, err := service.Runtime()
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	response := stateResponse{Version: s.version, Service: runtimeState, Receipt: receiptView{State: "WAITING", Message: "Waiting for the first complete synchronization."}}
	paths, pathErr := service.CurrentPaths()
	if pathErr == nil {
		if cfg, err := service.Load(paths.Config); err == nil {
			view := viewConfig(cfg)
			response.Config = &view
			if view.Output != "" {
				response.Receipt = loadReceipt(view.Output)
			}
		}
	}
	writeJSON(w, response, http.StatusOK)
}

func (s *server) logs(w http.ResponseWriter, _ *http.Request) {
	logs, err := service.ReadLogs(128 << 10)
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"logs": logs}, http.StatusOK)
}

func (s *server) invite(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("X-RekordLink-CSRF") != s.csrf {
		writeError(w, errors.New("invalid request token"), http.StatusForbidden)
		return
	}
	paths, err := service.CurrentPaths()
	if err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	cfg, err := service.Load(paths.Config)
	if err != nil {
		writeError(w, errors.New("the background service is not configured"), http.StatusNotFound)
		return
	}
	if cfg.Command != "host" && cfg.Command != "relay" {
		writeError(w, errors.New("only a host or relay creates an invite"), http.StatusBadRequest)
		return
	}
	stateDir := option(cfg.Args, "--state")
	if stateDir == "" {
		configDir, err := os.UserConfigDir()
		if err != nil {
			writeError(w, err, http.StatusInternalServerError)
			return
		}
		stateDir = filepath.Join(configDir, "RekordLink", cfg.Command)
	}
	b, err := os.ReadFile(filepath.Join(stateDir, "invite.txt"))
	if err != nil {
		writeError(w, errors.New("invite is not ready yet; start the service and try again"), http.StatusNotFound)
		return
	}
	invite := strings.TrimSpace(string(b))
	if len(invite) > 16<<10 || rlsync.ValidateInvite(invite) != nil {
		writeError(w, errors.New("saved invite is invalid"), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"invite": invite}, http.StatusOK)
}

func (s *server) setup(w http.ResponseWriter, r *http.Request) {
	var request setupRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	command, args, err := buildServiceArgs(request)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if command == "join" && strings.TrimSpace(request.Invite) == "" {
		paths, _ := service.CurrentPaths()
		if old, loadErr := service.Load(paths.Config); loadErr == nil && old.Command == "join" {
			if invite := option(old.Args, "--invite"); invite != "" {
				args = append(args, "--invite", invite)
			}
		}
		if option(args, "--invite") == "" {
			writeError(w, errors.New("a join invite is required"), http.StatusBadRequest)
			return
		}
		inviteAudio, inviteErr := rlsync.InviteAudio(option(args, "--invite"))
		if inviteErr != nil || inviteAudio != request.AllowAudio {
			writeError(w, errors.New("audio synchronization must match the saved host or relay invite"), http.StatusBadRequest)
			return
		}
	}
	if _, err := service.Install(command, args); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"message": "Configuration saved and background sync started."}, http.StatusOK)
}

// playlists reads the selected export without installing or changing a service.
func (s *server) playlists(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Library string `json:"library"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	path, err := absolutePath(request.Library)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	f, err := os.Open(path)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (128<<20)+1))
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if len(data) > 128<<20 {
		writeError(w, errors.New("library XML exceeds the 128 MiB safety limit"), http.StatusBadRequest)
		return
	}
	lib, err := library.Parse(data)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]any{"playlists": library.PlaylistPaths(lib)}, http.StatusOK)
}

func (s *server) control(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Action string `json:"action"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	if request.Action == "remove" {
		if _, err := service.Uninstall(); err != nil {
			writeError(w, err, http.StatusInternalServerError)
			return
		}
		writeJSON(w, map[string]string{"message": "Background service removed. Synced music and XML were left in place."}, http.StatusOK)
		return
	}
	if err := service.Control(request.Action); err != nil {
		writeError(w, err, http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]string{"message": "Service " + request.Action + " request completed."}, http.StatusOK)
}

func (s *server) pick(w http.ResponseWriter, r *http.Request) {
	var request struct {
		Kind string `json:"kind"`
	}
	if err := decodeJSON(w, r, &request); err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	path, err := pickPath(request.Kind)
	if err != nil {
		writeError(w, err, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]string{"path": path}, http.StatusOK)
}

func buildServiceArgs(request setupRequest) (string, []string, error) {
	request.Mode = strings.ToLower(strings.TrimSpace(request.Mode))
	if request.Mode != "host" && request.Mode != "join" && request.Mode != "relay" {
		return "", nil, errors.New("choose host, join, or relay mode")
	}
	listen := strings.TrimSpace(request.Listen)
	if listen == "" {
		listen = ":9777"
	}
	if request.Mode == "relay" {
		args := []string{"--listen", listen}
		if publicEndpoint := strings.TrimSpace(request.PublicEndpoint); publicEndpoint != "" {
			if _, err := rlsync.ValidatePublicEndpoint(publicEndpoint); err != nil {
				return "", nil, err
			}
			args = append(args, "--public-endpoint", publicEndpoint)
		}
		if advertise := strings.TrimSpace(request.Advertise); advertise != "" {
			args = append(args, "--advertise", advertise)
		}
		if option(args, "--public-endpoint") != "" && option(args, "--advertise") != "" {
			return "", nil, errors.New("public endpoint and advertised direct address cannot both be set")
		}
		if !request.AllowAudio {
			args = append(args, "--metadata-only")
		}
		return request.Mode, args, nil
	}

	name := strings.TrimSpace(request.Name)
	if name == "" {
		return "", nil, errors.New("DJ name is required")
	}
	libraryPath, err := absolutePath(request.Library)
	if err != nil {
		return "", nil, fmt.Errorf("library XML: %w", err)
	}
	data, err := os.ReadFile(libraryPath)
	if err != nil {
		return "", nil, fmt.Errorf("read library XML: %w", err)
	}
	if len(data) > 128<<20 {
		return "", nil, errors.New("library XML exceeds the 128 MiB safety limit")
	}
	if _, err := library.Parse(data); err != nil {
		return "", nil, fmt.Errorf("validate library XML: %w", err)
	}
	if _, err := library.ExcludePlaylistsXML(data, request.ExcludePlaylists); err != nil {
		return "", nil, err
	}
	output := strings.TrimSpace(request.Output)
	if output == "" {
		output = filepath.Join(filepath.Dir(libraryPath), "rekordlink-shared.xml")
	}
	output, err = absolutePath(output)
	if err != nil {
		return "", nil, fmt.Errorf("output XML: %w", err)
	}
	if filepath.Clean(libraryPath) == filepath.Clean(output) {
		return "", nil, errors.New("output XML must not overwrite the source library XML")
	}
	args := []string{"--name", name, "--library", libraryPath, "--output", output}
	for _, path := range request.ExcludePlaylists {
		args = append(args, "--exclude-playlist", path)
	}
	if request.AllowAudio {
		audioRoot, err := absolutePath(request.AudioRoot)
		if err != nil {
			return "", nil, fmt.Errorf("audio folder: %w", err)
		}
		if err := os.MkdirAll(audioRoot, 0o700); err != nil {
			return "", nil, fmt.Errorf("create audio folder: %w", err)
		}
		args = append(args, "--audio-root", audioRoot, "--allow-audio-copy")
	}
	if interval := strings.TrimSpace(request.Interval); interval != "" {
		if _, err := time.ParseDuration(interval); err != nil {
			return "", nil, errors.New("sync interval must look like 15s, 1m, or 5m")
		}
		args = append(args, "--interval", interval)
	}
	if request.Mode == "host" {
		args = append(args, "--listen", listen)
		if advertise := strings.TrimSpace(request.Advertise); advertise != "" {
			args = append(args, "--advertise", advertise)
		}
	} else if invite := strings.TrimSpace(request.Invite); invite != "" {
		inviteAudio, err := rlsync.InviteAudio(invite)
		if err != nil {
			return "", nil, err
		}
		if inviteAudio != request.AllowAudio {
			return "", nil, errors.New("audio synchronization must match the host or relay invite")
		}
		args = append(args, "--invite", invite)
	}
	return request.Mode, args, nil
}

func absolutePath(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("path is required")
	}
	return filepath.Abs(value)
}

func viewConfig(cfg service.Config) configView {
	view := configView{
		ExcludePlaylists: options(cfg.Args, "--exclude-playlist"),
		Mode:             cfg.Command,
		Name:             option(cfg.Args, "--name"),
		Library:          option(cfg.Args, "--library"),
		Output:           option(cfg.Args, "--output"),
		AudioRoot:        option(cfg.Args, "--audio-root"),
		AllowAudio:       hasOption(cfg.Args, "--allow-audio-copy") || (cfg.Command == "relay" && !hasOption(cfg.Args, "--metadata-only")),
		InviteConfigured: option(cfg.Args, "--invite") != "",
		Listen:           option(cfg.Args, "--listen"),
		Advertise:        option(cfg.Args, "--advertise"),
		PublicEndpoint:   option(cfg.Args, "--public-endpoint"),
		Interval:         option(cfg.Args, "--interval"),
	}
	if view.Output == "" && view.Library != "" {
		view.Output = filepath.Join(filepath.Dir(view.Library), "rekordlink-shared.xml")
	}
	return view
}

func option(args []string, name string) string {
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(args[i], name+"=") {
			return strings.TrimPrefix(args[i], name+"=")
		}
	}
	return ""
}

func options(args []string, name string) []string {
	values := []string{}
	for i := 0; i < len(args); i++ {
		if args[i] == name && i+1 < len(args) {
			values = append(values, args[i+1])
			i++
		} else if strings.HasPrefix(args[i], name+"=") {
			values = append(values, strings.TrimPrefix(args[i], name+"="))
		}
	}
	return values
}

func hasOption(args []string, name string) bool {
	for _, arg := range args {
		if arg == name || strings.HasPrefix(arg, name+"=") {
			return true
		}
	}
	return false
}

func loadReceipt(output string) receiptView {
	receipt, err := rlsync.VerifyReceipt(output)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return receiptView{State: "WAITING", Message: "Waiting for the first complete synchronization."}
		}
		return receiptView{State: "ATTENTION", Message: err.Error()}
	}
	state := "READY"
	message := "Merged library and synchronized audio are ready on this computer."
	if receipt.LocalAudioMissing > 0 {
		state = "INCOMPLETE"
		message = "One or more synchronized audio files are missing. The service will retry."
	}
	return receiptView{
		State:       state,
		Message:     message,
		Generated:   receipt.GeneratedAt.Format(time.RFC3339),
		Tracks:      receipt.Tracks,
		Playlists:   receipt.Playlists,
		AudioReady:  receipt.LocalAudioPresent,
		AudioTotal:  receipt.LocalAudioReferences,
		RoomVersion: receipt.RoomRevision,
	}
}

func decodeJSON(w http.ResponseWriter, r *http.Request, destination any) error {
	r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return fmt.Errorf("invalid request: %w", err)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, value any, status int) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}

func writeError(w http.ResponseWriter, err error, status int) {
	writeJSON(w, map[string]string{"error": err.Error()}, status)
}

func openURL(url string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", url)
	case "linux":
		command = exec.Command("xdg-open", url)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		return fmt.Errorf("opening a browser is unsupported on %s", runtime.GOOS)
	}
	return command.Start()
}

func pickPath(kind string) (string, error) {
	if kind != "file" && kind != "directory" {
		return "", errors.New("picker kind must be file or directory")
	}
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		script := `POSIX path of (choose file with prompt "Choose rekordbox XML export")`
		if kind == "directory" {
			script = `POSIX path of (choose folder with prompt "Choose synchronized audio folder")`
		}
		command = exec.Command("osascript", "-e", script)
	case "linux":
		args := []string{"--file-selection", "--title=Choose rekordbox XML export"}
		if kind == "directory" {
			args = append(args, "--directory")
			args[1] = "--title=Choose synchronized audio folder"
		}
		command = exec.Command("zenity", args...)
	case "windows":
		if kind == "file" {
			command = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `Add-Type -AssemblyName PresentationFramework; $d=New-Object Microsoft.Win32.OpenFileDialog; $d.Filter='XML files (*.xml)|*.xml|All files (*.*)|*.*'; if($d.ShowDialog()){ $d.FileName }`)
		} else {
			command = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", `Add-Type -AssemblyName System.Windows.Forms; $d=New-Object System.Windows.Forms.FolderBrowserDialog; if($d.ShowDialog() -eq 'OK'){ $d.SelectedPath }`)
		}
	default:
		return "", fmt.Errorf("native file picking is unsupported on %s", runtime.GOOS)
	}
	output, err := command.Output()
	if err != nil {
		return "", errors.New("file selection was cancelled or unavailable")
	}
	path := strings.TrimSpace(string(output))
	if path == "" {
		return "", errors.New("no path selected")
	}
	return path, nil
}
