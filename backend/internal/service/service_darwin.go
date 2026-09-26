package service

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
)

const (
	label     = "dev.ferry.server"
	plistPath = "/Library/LaunchDaemons/" + label + ".plist"
	logPath   = "/usr/local/var/log/ferry.log"
)

func Default() Paths {
	// launchd writes the log (StandardErrorPath), so FERRY_LOG_FILE stays unset.
	return Paths{Binary: "/usr/local/bin/ferry", Config: "/usr/local/etc/ferry/ferry.env", DataDir: "/usr/local/var/ferry"}
}

func plist(p Paths, userName string) string {
	x := html.EscapeString
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
  <key>Label</key><string>` + label + `</string>
  <key>ProgramArguments</key><array><string>` + x(p.Binary) + `</string><string>serve</string></array>
  <key>EnvironmentVariables</key><dict><key>FERRY_CONFIG</key><string>` + x(p.Config) + `</string></dict>
  <key>UserName</key><string>` + x(userName) + `</string>
  <key>RunAtLoad</key><true/>
  <key>KeepAlive</key><dict><key>SuccessfulExit</key><false/></dict>
  <key>StandardOutPath</key><string>` + logPath + `</string>
  <key>StandardErrorPath</key><string>` + logPath + `</string>
</dict>
</plist>
`
}

func Install(o Options) (Paths, error) {
	p := Default()
	if os.Geteuid() != 0 {
		return p, errors.New("run this with sudo: sudo ferry service install")
	}
	// Run as the person who installed it rather than as root.
	name := os.Getenv("SUDO_USER")
	if name == "" || name == "root" {
		return p, errors.New("run this with sudo from your normal user account (Ferry should not run as root)")
	}
	u, err := user.Lookup(name)
	if err != nil {
		return p, err
	}
	uid, _ := strconv.Atoi(u.Uid)
	gid, _ := strconv.Atoi(u.Gid)
	if err := copyBinary(p.Binary); err != nil {
		return p, fmt.Errorf("install binary: %w", err)
	}
	for _, d := range []string{p.DataDir, filepath.Dir(logPath)} {
		if err := os.MkdirAll(d, 0o750); err != nil {
			return p, err
		}
	}
	os.Chown(p.DataDir, uid, gid)
	if f, err := os.OpenFile(logPath, os.O_CREATE|os.O_APPEND, 0o640); err == nil {
		f.Close()
		os.Chown(logPath, uid, gid)
	}
	if _, err := writeConfig(p, o); err != nil {
		return p, fmt.Errorf("write config: %w", err)
	}
	os.Chown(p.Config, uid, gid)
	os.Chmod(p.Config, 0o600)
	exec.Command("launchctl", "bootout", "system/"+label).Run() // reinstall: unload the old definition first
	if err := os.WriteFile(plistPath, []byte(plist(p, name)), 0o644); err != nil {
		return p, err
	}
	return p, run("launchctl", "bootstrap", "system", plistPath)
}

func Uninstall() (Paths, error) {
	p := Default()
	if os.Geteuid() != 0 {
		return p, errors.New("run this with sudo: sudo ferry service uninstall")
	}
	exec.Command("launchctl", "bootout", "system/"+label).Run()
	if err := os.Remove(plistPath); err != nil && !os.IsNotExist(err) {
		return p, err
	}
	return p, nil
}

func Start() error   { return run("launchctl", "bootstrap", "system", plistPath) }
func Stop() error    { return run("launchctl", "bootout", "system/"+label) }
func Restart() error { return run("launchctl", "kickstart", "-k", "system/"+label) }

func Status() error {
	cmd := exec.Command("launchctl", "print", "system/"+label)
	cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
	if cmd.Run() != nil {
		fmt.Println("Ferry is not running.")
	}
	return nil
}

func LogsHint() string { return "tail -f " + logPath }

// RunAsService reports false: launchd runs "ferry serve" directly.
func RunAsService(func(ctx context.Context) error) (bool, error) { return false, nil }
