package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"os/user"
	"strconv"
)

const unitPath = "/etc/systemd/system/ferry.service"

func Default() Paths {
	return Paths{Binary: "/usr/local/bin/ferry", Config: "/etc/ferry/ferry.env", DataDir: "/var/lib/ferry"}
}

// unit is the systemd service: a dedicated unprivileged user and a read-only view of the system.
func unit(p Paths) string {
	return `[Unit]
Description=Ferry file sharing
After=network-online.target
Wants=network-online.target

[Service]
User=ferry
Group=ferry
Environment=FERRY_CONFIG=` + p.Config + `
ExecStart=` + p.Binary + ` serve
Restart=on-failure
RestartSec=5
# Lets Ferry use ports 80/443 without running as root.
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ProtectHome=true
PrivateTmp=true
ReadWritePaths=` + p.DataDir + `

[Install]
WantedBy=multi-user.target
`
}

func Install(o Options) (Paths, error) {
	p := Default()
	if os.Geteuid() != 0 {
		return p, errors.New("run this with sudo: sudo ferry service install")
	}
	if _, err := exec.LookPath("systemctl"); err != nil {
		return p, errors.New("systemd was not found; start Ferry with your init system using: " + p.Binary + " serve")
	}
	if err := copyBinary(p.Binary); err != nil {
		return p, fmt.Errorf("install binary: %w", err)
	}
	u, err := ensureUser()
	if err != nil {
		return p, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err := os.MkdirAll(p.DataDir, 0o750); err != nil {
		return p, err
	}
	if err := os.Chown(p.DataDir, uid, gid); err != nil {
		return p, err
	}
	if _, err := writeConfig(p, o); err != nil {
		return p, fmt.Errorf("write config: %w", err)
	}
	os.Chown(p.Config, 0, gid) // readable by the service, not by other users (it may hold passwords)
	os.Chmod(p.Config, 0o640)
	if err := os.WriteFile(unitPath, []byte(unit(p)), 0o644); err != nil {
		return p, err
	}
	if err := run("systemctl", "daemon-reload"); err != nil {
		return p, err
	}
	return p, run("systemctl", "enable", "--now", Name)
}

func ensureUser() (*user.User, error) {
	if u, err := user.Lookup("ferry"); err == nil {
		return u, nil
	}
	var err error
	if _, e := exec.LookPath("useradd"); e == nil {
		err = run("useradd", "--system", "--user-group", "--home-dir", "/var/lib/ferry", "--no-create-home", "--shell", "/usr/sbin/nologin", "ferry")
	} else {
		err = run("adduser", "-S", "-D", "-H", "-h", "/var/lib/ferry", "-s", "/sbin/nologin", "ferry")
	}
	if err != nil {
		return nil, fmt.Errorf("create the ferry user: %w", err)
	}
	return user.Lookup("ferry")
}

func Uninstall() (Paths, error) {
	p := Default()
	if os.Geteuid() != 0 {
		return p, errors.New("run this with sudo: sudo ferry service uninstall")
	}
	run("systemctl", "disable", "--now", Name)
	if err := os.Remove(unitPath); err != nil && !os.IsNotExist(err) {
		return p, err
	}
	return p, run("systemctl", "daemon-reload")
}

func Start() error   { return run("systemctl", "start", Name) }
func Stop() error    { return run("systemctl", "stop", Name) }
func Restart() error { return run("systemctl", "restart", Name) }

func Status() error {
	cmd := exec.Command("systemctl", "status", Name, "--no-pager")
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	cmd.Run() // non-zero exit just means "not running"; the output says so
	return nil
}

func LogsHint() string { return "journalctl -u ferry -f" }

// RunAsService reports false: on Linux systemd runs "ferry serve" directly.
func RunAsService(func(ctx context.Context) error) (bool, error) { return false, nil }
