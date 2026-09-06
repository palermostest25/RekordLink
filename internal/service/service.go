package service

import (
	"bytes"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

const label = "com.rekordlink.agent"

type Config struct {
	Command string   `json:"command"`
	Args    []string `json:"args"`
}

type Paths struct {
	Config     string
	Executable string
	Unit       string
	Log        string
}

type RuntimeStatus struct {
	Configured bool   `json:"configured"`
	Running    bool   `json:"running"`
	State      string `json:"state"`
}

func Install(command string, args []string) (Paths, error) {
	if command != "host" && command != "join" && command != "relay" {
		return Paths{}, errors.New("service command must be host, join, or relay")
	}
	sourceExecutable, err := os.Executable()
	if err != nil {
		return Paths{}, err
	}
	sourceExecutable, err = filepath.Abs(sourceExecutable)
	if err != nil {
		return Paths{}, err
	}
	paths, err := servicePaths()
	if err != nil {
		return Paths{}, err
	}
	executable := paths.Executable
	if filepath.Clean(sourceExecutable) != filepath.Clean(executable) {
		binary, err := os.ReadFile(sourceExecutable)
		if err != nil {
			return Paths{}, fmt.Errorf("stage background executable: %w", err)
		}
		if err := writeAtomic(executable, binary, 0o700); err != nil {
			return Paths{}, fmt.Errorf("stage background executable: %w", err)
		}
	}
	configData, err := json.MarshalIndent(Config{Command: command, Args: args}, "", "  ")
	if err != nil {
		return Paths{}, err
	}
	if err := writeAtomic(paths.Config, append(configData, '\n'), 0o600); err != nil {
		return Paths{}, err
	}
	if err := os.MkdirAll(filepath.Dir(paths.Log), 0o700); err != nil {
		return Paths{}, err
	}
	if runtime.GOOS == "darwin" || runtime.GOOS == "windows" {
		logFile, err := os.OpenFile(paths.Log, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			return Paths{}, err
		}
		if err := logFile.Chmod(0o600); err != nil {
			logFile.Close()
			return Paths{}, err
		}
		if err := logFile.Close(); err != nil {
			return Paths{}, err
		}
	}
	switch runtime.GOOS {
	case "darwin":
		if err := writeAtomic(paths.Unit, []byte(renderLaunchAgent(executable, paths.Config, paths.Log)), 0o600); err != nil {
			return Paths{}, err
		}
		uid, err := currentUID()
		if err != nil {
			return Paths{}, err
		}
		domain := "gui/" + uid
		// Reload the unit so a changed executable path or configuration takes
		// effect immediately instead of merely waking the old process.
		_ = exec.Command("launchctl", "bootout", domain+"/"+label).Run()
		if output, err := exec.Command("launchctl", "bootstrap", domain, paths.Unit).CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("start launch agent: %v (%s)", err, strings.TrimSpace(string(output)))
		}
	case "linux":
		if err := writeAtomic(paths.Unit, []byte(renderSystemdUnit(executable, paths.Config)), 0o600); err != nil {
			return Paths{}, err
		}
		if output, err := exec.Command("systemctl", "--user", "daemon-reload").CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("reload systemd: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		if output, err := exec.Command("systemctl", "--user", "enable", "rekordlink.service").CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("enable service: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		if output, err := exec.Command("systemctl", "--user", "restart", "rekordlink.service").CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("start service: %v (%s)", err, strings.TrimSpace(string(output)))
		}
	case "windows":
		action := quoteWindows(executable) + " _service_run " + quoteWindows(paths.Config)
		_ = exec.Command("schtasks", "/End", "/TN", "RekordLink").Run()
		if output, err := exec.Command("schtasks", "/Create", "/F", "/SC", "ONLOGON", "/RL", "LIMITED", "/TN", "RekordLink", "/TR", action).CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("create scheduled task: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		if output, err := exec.Command("schtasks", "/Run", "/TN", "RekordLink").CombinedOutput(); err != nil {
			return Paths{}, fmt.Errorf("start scheduled task: %v (%s)", err, strings.TrimSpace(string(output)))
		}
	default:
		return Paths{}, fmt.Errorf("background service installation is unsupported on %s", runtime.GOOS)
	}
	return paths, nil
}

func Uninstall() (Paths, error) {
	paths, err := servicePaths()
	if err != nil {
		return Paths{}, err
	}
	switch runtime.GOOS {
	case "darwin":
		if uid, uidErr := currentUID(); uidErr == nil {
			_ = exec.Command("launchctl", "bootout", "gui/"+uid+"/"+label).Run()
		}
	case "linux":
		_ = exec.Command("systemctl", "--user", "disable", "--now", "rekordlink.service").Run()
	case "windows":
		_ = exec.Command("schtasks", "/Delete", "/F", "/TN", "RekordLink").Run()
	}
	for _, path := range []string{paths.Unit, paths.Config, paths.Executable} {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return paths, err
		}
	}
	if runtime.GOOS == "linux" {
		_ = exec.Command("systemctl", "--user", "daemon-reload").Run()
	}
	return paths, nil
}

func Load(path string) (Config, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	var cfg Config
	if err := json.Unmarshal(b, &cfg); err != nil {
		return Config{}, err
	}
	if cfg.Command != "host" && cfg.Command != "join" && cfg.Command != "relay" {
		return Config{}, errors.New("service config contains an invalid command")
	}
	return cfg, nil
}

func CurrentPaths() (Paths, error) { return servicePaths() }

// Runtime reports whether the per-user synchronization service is configured
// and currently active. A missing service is a normal state, not an error.
func Runtime() (RuntimeStatus, error) {
	paths, err := servicePaths()
	if err != nil {
		return RuntimeStatus{}, err
	}
	status := RuntimeStatus{State: "not configured"}
	if _, err := os.Stat(paths.Config); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return status, nil
		}
		return status, err
	}
	status.Configured = true
	status.State = "stopped"

	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		uid, err := currentUID()
		if err != nil {
			return status, err
		}
		command = exec.Command("launchctl", "print", "gui/"+uid+"/"+label)
	case "linux":
		command = exec.Command("systemctl", "--user", "is-active", "rekordlink.service")
	case "windows":
		command = exec.Command("powershell", "-NoProfile", "-NonInteractive", "-Command", "(Get-ScheduledTask -TaskName 'RekordLink' -ErrorAction Stop).State")
	default:
		status.State = "unknown"
		return status, nil
	}
	output, commandErr := command.CombinedOutput()
	if commandErr != nil {
		return status, nil
	}
	stateText := strings.ToLower(strings.TrimSpace(string(output)))
	switch runtime.GOOS {
	case "darwin":
		status.Running = strings.Contains(stateText, "state = running")
	case "linux":
		status.Running = stateText == "active"
	case "windows":
		status.Running = stateText == "running"
	}
	if status.Running {
		status.State = "running"
	}
	return status, nil
}

// Control starts, stops, or restarts the already-configured per-user service.
// Stop deliberately leaves the private configuration and unit on disk.
func Control(action string) error {
	if action != "start" && action != "stop" && action != "restart" {
		return errors.New("service action must be start, stop, or restart")
	}
	paths, err := servicePaths()
	if err != nil {
		return err
	}
	if _, err := os.Stat(paths.Config); err != nil {
		return fmt.Errorf("service is not configured: %w", err)
	}

	run := func(command *exec.Cmd, description string) error {
		output, err := command.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%s: %v (%s)", description, err, strings.TrimSpace(string(output)))
		}
		return nil
	}

	switch runtime.GOOS {
	case "darwin":
		uid, err := currentUID()
		if err != nil {
			return err
		}
		domain := "gui/" + uid
		serviceName := domain + "/" + label
		if action == "stop" {
			return run(exec.Command("launchctl", "bootout", serviceName), "stop launch agent")
		}
		if action == "restart" {
			if err := exec.Command("launchctl", "print", serviceName).Run(); err == nil {
				return run(exec.Command("launchctl", "kickstart", "-k", serviceName), "restart launch agent")
			}
		}
		if err := exec.Command("launchctl", "print", serviceName).Run(); err == nil {
			return run(exec.Command("launchctl", "kickstart", serviceName), "start launch agent")
		}
		return run(exec.Command("launchctl", "bootstrap", domain, paths.Unit), "start launch agent")
	case "linux":
		return run(exec.Command("systemctl", "--user", action, "rekordlink.service"), action+" systemd service")
	case "windows":
		switch action {
		case "start":
			return run(exec.Command("schtasks", "/Run", "/TN", "RekordLink"), "start scheduled task")
		case "stop":
			return run(exec.Command("schtasks", "/End", "/TN", "RekordLink"), "stop scheduled task")
		case "restart":
			_ = exec.Command("schtasks", "/End", "/TN", "RekordLink").Run()
			return run(exec.Command("schtasks", "/Run", "/TN", "RekordLink"), "restart scheduled task")
		}
	}
	return fmt.Errorf("service control is unsupported on %s", runtime.GOOS)
}

// ReadLogs returns a bounded tail suitable for the local management UI.
func ReadLogs(maxBytes int64) (string, error) {
	if maxBytes <= 0 || maxBytes > 1<<20 {
		maxBytes = 128 << 10
	}
	paths, err := servicePaths()
	if err != nil {
		return "", err
	}
	if runtime.GOOS == "linux" {
		output, err := exec.Command("journalctl", "--user", "-u", "rekordlink", "-n", "200", "--no-pager").CombinedOutput()
		if err != nil {
			return "", fmt.Errorf("read journal: %v (%s)", err, strings.TrimSpace(string(output)))
		}
		if int64(len(output)) > maxBytes {
			output = output[len(output)-int(maxBytes):]
		}
		return string(output), nil
	}
	f, err := os.Open(paths.Log)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return "No service log has been written yet.\n", nil
		}
		return "", err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return "", err
	}
	start := info.Size() - maxBytes
	if start < 0 {
		start = 0
	}
	if _, err := f.Seek(start, 0); err != nil {
		return "", err
	}
	b, err := io.ReadAll(io.LimitReader(f, maxBytes))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func servicePaths() (Paths, error) {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, err
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return Paths{}, err
	}
	executableName := "rekordlink"
	if runtime.GOOS == "windows" {
		executableName += ".exe"
	}
	paths := Paths{
		Config:     filepath.Join(configDir, "RekordLink", "service.json"),
		Executable: filepath.Join(configDir, "RekordLink", "bin", executableName),
	}
	switch runtime.GOOS {
	case "darwin":
		paths.Unit = filepath.Join(home, "Library", "LaunchAgents", label+".plist")
		paths.Log = filepath.Join(home, "Library", "Logs", "RekordLink", "agent.log")
	case "linux":
		paths.Unit = filepath.Join(configDir, "systemd", "user", "rekordlink.service")
		paths.Log = "journalctl --user -u rekordlink"
	case "windows":
		paths.Unit = "Windows Task Scheduler: RekordLink"
		paths.Log = filepath.Join(configDir, "RekordLink", "agent.log")
	default:
		paths.Unit = filepath.Join(configDir, "RekordLink", "service")
	}
	return paths, nil
}

func renderLaunchAgent(executable, config, logPath string) string {
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0"><dict>
<key>Label</key><string>` + xmlEscape(label) + `</string>
<key>ProgramArguments</key><array><string>` + xmlEscape(executable) + `</string><string>_service_run</string><string>` + xmlEscape(config) + `</string></array>
<key>RunAtLoad</key><true/><key>KeepAlive</key><true/><key>ThrottleInterval</key><integer>10</integer>
<key>ProcessType</key><string>Background</string>
<key>StandardOutPath</key><string>` + xmlEscape(logPath) + `</string>
<key>StandardErrorPath</key><string>` + xmlEscape(logPath) + `</string>
</dict></plist>
`
}

func renderSystemdUnit(executable, config string) string {
	return `[Unit]
Description=RekordLink background synchronization agent
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
ExecStart=` + quoteSystemd(executable) + ` _service_run ` + quoteSystemd(config) + `
Restart=on-failure
RestartSec=10

[Install]
WantedBy=default.target
`
}

func xmlEscape(value string) string {
	var b bytes.Buffer
	_ = xml.EscapeText(&b, []byte(value))
	return b.String()
}

func quoteSystemd(value string) string {
	return `"` + strings.ReplaceAll(strings.ReplaceAll(value, `\`, `\\`), `"`, `\"`) + `"`
}

func quoteWindows(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `\"`) + `"`
}

func currentUID() (string, error) {
	output, err := exec.Command("id", "-u").Output()
	if err != nil {
		return "", err
	}
	uid := strings.TrimSpace(string(output))
	if _, err := strconv.Atoi(uid); err != nil {
		return "", errors.New("could not determine numeric user ID")
	}
	return uid, nil
}

func writeAtomic(path string, data []byte, mode os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".rekordlink-service-*")
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
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path); err != nil {
		if runtime.GOOS != "windows" {
			return err
		}
		// Windows cannot atomically replace an existing file with os.Rename.
		// The temporary file is complete and closed before this narrow fallback.
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			return err
		}
		return os.Rename(tmpName, path)
	}
	return nil
}
