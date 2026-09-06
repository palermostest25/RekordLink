package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/rekordlink/rekordlink/internal/library"
	"github.com/rekordlink/rekordlink/internal/service"
	rlsync "github.com/rekordlink/rekordlink/internal/sync"
	"github.com/rekordlink/rekordlink/internal/ui"
)

var version = "dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "rekordlink: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	if len(os.Args) < 2 {
		usage()
		return errors.New("a command is required")
	}
	switch os.Args[1] {
	case "host":
		return runHost(os.Args[2:])
	case "join":
		return runJoin(os.Args[2:])
	case "relay":
		return runRelay(os.Args[2:])
	case "service":
		return runService(os.Args[2:])
	case "ui":
		return runUI(os.Args[2:])
	case "_service_run":
		return runSavedService(os.Args[2:])
	case "inspect":
		return runInspect(os.Args[2:])
	case "status":
		return runStatus(os.Args[2:])
	case "merge":
		return runMerge(os.Args[2:])
	case "version", "--version", "-v":
		fmt.Println(version)
		return nil
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		usage()
		return fmt.Errorf("unknown command %q", os.Args[1])
	}
}

func runStatus(args []string) error {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	output := fs.String("output", "rekordlink-shared.xml", "merged XML output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	receipt, err := rlsync.VerifyReceipt(*output)
	if err != nil {
		return err
	}
	fmt.Print(rlsync.FormatReceipt(receipt))
	if receipt.LocalAudioMissing > 0 {
		return errors.New("merged library has missing local audio")
	}
	return nil
}

func runService(args []string) error {
	if len(args) == 0 {
		return errors.New("service requires install, start, stop, restart, uninstall, or status")
	}
	switch args[0] {
	case "install":
		if len(args) < 2 {
			return errors.New("service install requires host, join, or relay followed by its options")
		}
		paths, err := service.Install(args[1], args[2:])
		if err != nil {
			return err
		}
		fmt.Printf("background service installed\nengine: %s\nconfig: %s\nservice: %s\nlogs: %s\n", paths.Executable, paths.Config, paths.Unit, paths.Log)
		return nil
	case "uninstall":
		paths, err := service.Uninstall()
		if err != nil {
			return err
		}
		fmt.Printf("background service removed: %s\n", paths.Unit)
		return nil
	case "status":
		paths, err := service.CurrentPaths()
		if err != nil {
			return err
		}
		cfg, err := service.Load(paths.Config)
		if err != nil {
			return fmt.Errorf("service is not configured: %w", err)
		}
		runtimeState, err := service.Runtime()
		if err != nil {
			return err
		}
		fmt.Printf("state: %s\nconfigured command: %s\nengine: %s\nconfig: %s\nservice: %s\nlogs: %s\n", runtimeState.State, cfg.Command, paths.Executable, paths.Config, paths.Unit, paths.Log)
		return nil
	case "start", "stop", "restart":
		if err := service.Control(args[0]); err != nil {
			return err
		}
		fmt.Printf("background service %s requested\n", args[0])
		return nil
	default:
		return fmt.Errorf("unknown service action %q", args[0])
	}
}

func runUI(args []string) error {
	fs := flag.NewFlagSet("ui", flag.ContinueOnError)
	listen := fs.String("listen", "127.0.0.1:9766", "loopback address for the local management UI")
	noBrowser := fs.Bool("no-browser", false, "do not open the default browser")
	exitOnStdinClose := fs.Bool("exit-on-stdin-close", false, "exit when the supervising process closes standard input")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *exitOnStdinClose {
		go func() {
			_, _ = io.Copy(io.Discard, os.Stdin)
			stop()
		}()
	}
	return ui.Run(ctx, ui.Options{Listen: *listen, OpenBrowser: !*noBrowser, Version: version})
}

func runSavedService(args []string) error {
	if len(args) != 1 {
		return errors.New("saved service requires its config path")
	}
	cfg, err := service.Load(args[0])
	if err != nil {
		return err
	}
	if runtime.GOOS == "windows" {
		paths, err := service.CurrentPaths()
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(paths.Log), 0o700); err != nil {
			return err
		}
		logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return err
		}
		defer logFile.Close()
		log.SetOutput(logFile)
	}
	switch cfg.Command {
	case "host":
		return runHostMode(cfg.Args, false)
	case "join":
		return runJoin(cfg.Args)
	case "relay":
		return runRelayMode(cfg.Args, false)
	default:
		return errors.New("saved service command is invalid")
	}
}

func runRelay(args []string) error {
	return runRelayMode(args, true)
}

func runRelayMode(args []string, showInvite bool) error {
	fs := flag.NewFlagSet("relay", flag.ContinueOnError)
	state := fs.String("state", defaultStateDir("relay"), "private relay state directory")
	listen := fs.String("listen", ":9777", "relay origin listen address (HTTP only in public-endpoint mode)")
	advertise := fs.String("advertise", "", "host name or IP advertised to both DJs")
	publicEndpoint := fs.String("public-endpoint", "", "public HTTPS URL terminated by a trusted reverse proxy")
	metadataOnly := fs.Bool("metadata-only", false, "disable audio storage and transfer")
	showInviteFlag := fs.Bool("show-invite", showInvite, "print the private pairing invite in process output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rlsync.RunRelay(ctx, rlsync.RelayConfig{
		StateDir:       *state,
		ListenAddr:     *listen,
		Advertise:      *advertise,
		PublicEndpoint: *publicEndpoint,
		Audio:          !*metadataOnly,
		Version:        version,
		ShowInvite:     *showInviteFlag,
	})
}

func runHost(args []string) error {
	return runHostMode(args, true)
}

func runHostMode(args []string, showInvite bool) error {
	fs := flag.NewFlagSet("host", flag.ContinueOnError)
	libraryPath := fs.String("library", "", "path to a rekordbox XML export")
	name := fs.String("name", "", "DJ display name")
	output := fs.String("output", "", "merged XML output path")
	state := fs.String("state", defaultStateDir("host"), "private state directory")
	listen := fs.String("listen", ":9777", "HTTPS listen address")
	advertise := fs.String("advertise", "", "host or IP advertised to the other DJ")
	interval := fs.Duration("interval", rlsync.DefaultInterval, "file scan/sync interval")
	audioRoot := fs.String("audio-root", "", "managed directory for synchronized audio")
	allowAudio := fs.Bool("allow-audio-copy", false, "confirm both DJs may copy referenced local audio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *libraryPath == "" || *name == "" {
		return errors.New("host requires --library and --name")
	}
	if *output == "" {
		*output = defaultOutput(*libraryPath)
	}
	if samePath(*libraryPath, *output) {
		return errors.New("--output must not overwrite the source --library XML")
	}
	if (*audioRoot == "") != !*allowAudio {
		return errors.New("audio sync requires both --audio-root and --allow-audio-copy")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rlsync.RunHost(ctx, rlsync.HostConfig{
		LibraryPath: *libraryPath,
		Name:        *name,
		OutputPath:  *output,
		StateDir:    *state,
		ListenAddr:  *listen,
		Advertise:   *advertise,
		Interval:    *interval,
		Version:     version,
		AudioRoot:   *audioRoot,
		ShowInvite:  showInvite,
	})
}

func runJoin(args []string) error {
	fs := flag.NewFlagSet("join", flag.ContinueOnError)
	invite := fs.String("invite", "", "rekordlink:// invite printed by the host")
	libraryPath := fs.String("library", "", "path to a rekordbox XML export")
	name := fs.String("name", "", "DJ display name")
	output := fs.String("output", "", "merged XML output path")
	state := fs.String("state", defaultStateDir("join"), "private state directory")
	interval := fs.Duration("interval", rlsync.DefaultInterval, "file scan/sync interval")
	audioRoot := fs.String("audio-root", "", "managed directory for synchronized audio")
	allowAudio := fs.Bool("allow-audio-copy", false, "confirm both DJs may copy referenced local audio")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *invite == "" || *libraryPath == "" || *name == "" {
		return errors.New("join requires --invite, --library, and --name")
	}
	if *output == "" {
		*output = defaultOutput(*libraryPath)
	}
	if samePath(*libraryPath, *output) {
		return errors.New("--output must not overwrite the source --library XML")
	}
	if (*audioRoot == "") != !*allowAudio {
		return errors.New("audio sync requires both --audio-root and --allow-audio-copy")
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	return rlsync.RunJoin(ctx, rlsync.JoinConfig{
		Invite:      *invite,
		LibraryPath: *libraryPath,
		Name:        *name,
		OutputPath:  *output,
		StateDir:    *state,
		Interval:    *interval,
		Version:     version,
		AudioRoot:   *audioRoot,
	})
}

func runInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	path := fs.String("library", "", "path to a rekordbox XML export")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *path == "" {
		return errors.New("inspect requires --library")
	}
	b, err := os.ReadFile(*path)
	if err != nil {
		return err
	}
	lib, err := library.Parse(b)
	if err != nil {
		return err
	}
	fmt.Printf("tracks: %d\nplaylists: %d\ncue/loop markers: %d\nbeatgrid markers: %d\n", len(lib.Collection.Tracks), library.PlaylistCount(lib.Playlists.Root), library.MarkerCount(lib), library.TempoCount(lib))
	return nil
}

type namedLibrary struct {
	name string
	path string
}

type libraryFlags []namedLibrary

func (f *libraryFlags) String() string { return "NAME=PATH" }
func (f *libraryFlags) Set(v string) error {
	name, path, ok := strings.Cut(v, "=")
	if !ok || strings.TrimSpace(name) == "" || strings.TrimSpace(path) == "" {
		return errors.New("library must be NAME=PATH")
	}
	*f = append(*f, namedLibrary{name: strings.TrimSpace(name), path: strings.TrimSpace(path)})
	return nil
}

func runMerge(args []string) error {
	fs := flag.NewFlagSet("merge", flag.ContinueOnError)
	var inputs libraryFlags
	fs.Var(&inputs, "library", "input as NAME=PATH (repeat twice or more)")
	output := fs.String("output", "rekordlink-shared.xml", "merged XML output path")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if len(inputs) < 2 {
		return errors.New("merge requires at least two --library NAME=PATH values")
	}
	snapshots := make([]library.Snapshot, 0, len(inputs))
	for i, input := range inputs {
		data, err := os.ReadFile(input.path)
		if err != nil {
			return fmt.Errorf("read %s: %w", input.path, err)
		}
		lib, err := library.Parse(data)
		if err != nil {
			return fmt.Errorf("parse %s: %w", input.path, err)
		}
		snapshots = append(snapshots, library.Snapshot{PeerID: fmt.Sprintf("peer-%d", i+1), PeerName: input.name, Library: lib})
	}
	merged, err := library.Merge(snapshots, snapshots[0].PeerID)
	if err != nil {
		return err
	}
	data, err := library.Marshal(merged)
	if err != nil {
		return err
	}
	return rlsync.WriteAtomic(*output, data, 0o600)
}

func defaultStateDir(role string) string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return filepath.Join(".rekordlink", role)
	}
	return filepath.Join(dir, "RekordLink", role)
}

func defaultOutput(input string) string {
	return filepath.Join(filepath.Dir(input), "rekordlink-shared.xml")
}

func samePath(a, b string) bool {
	absA, errA := filepath.Abs(a)
	absB, errB := filepath.Abs(b)
	return errA == nil && errB == nil && filepath.Clean(absA) == filepath.Clean(absB)
}

func usage() {
	fmt.Print(`RekordLink - secure two-DJ rekordbox XML library linking

Usage:
  rekordlink host    --name "DJ A" --library /path/rekordbox.xml
  rekordlink join    --name "DJ B" --library /path/rekordbox.xml --invite 'rekordlink://...'
	rekordlink relay   --public-endpoint https://sync.example.com --listen :9777
  rekordlink ui      Open the cross-platform management dashboard
  rekordlink service install host|join|relay <options...>
  rekordlink service start|stop|restart|status|uninstall
  rekordlink inspect --library /path/rekordbox.xml
  rekordlink status  --output /path/rekordlink-shared.xml
  rekordlink merge   --library "DJ A=/a.xml" --library "DJ B=/b.xml" --output shared.xml
  rekordlink version

Both DJs configure rekordlink-shared.xml as rekordbox's imported Bridge library.
Run "rekordlink <command> -h" for command options.
`)
}
