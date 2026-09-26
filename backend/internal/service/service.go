// Package service installs and controls Ferry as a system service (systemd, launchd or Windows Service).
package service

import (
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const Name = "ferry"

// Options for Install.
type Options struct {
	Addr      string                                     // listen address written to a new config file
	ConfigFor func(dataDir, logFile, addr string) string // renders the initial config file
}

// Paths used by the installed service on this platform.
type Paths struct {
	Binary, Config, DataDir, LogFile string
}

// copyBinary installs the running executable at dst (unless it already is dst).
func copyBinary(dst string) error {
	src, err := os.Executable()
	if err != nil {
		return err
	}
	if src, err = filepath.EvalSymlinks(src); err != nil {
		return err
	}
	if abs, _ := filepath.Abs(dst); strings.EqualFold(abs, src) {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	tmp := dst + ".new"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// writeConfig creates the config file unless it exists (an existing file is never overwritten).
func writeConfig(p Paths, o Options) (created bool, err error) {
	if _, err := os.Stat(p.Config); err == nil {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(p.Config), 0o755); err != nil {
		return false, err
	}
	return true, os.WriteFile(p.Config, []byte(o.ConfigFor(p.DataDir, p.LogFile, o.Addr)), 0o640)
}

func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %v\n%s", name, strings.Join(args, " "), err, strings.TrimSpace(string(out)))
	}
	return nil
}

// URLs lists addresses where the web app can be opened, for the post-install message.
func URLs(addr string) []string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	if host != "" && host != "0.0.0.0" && host != "::" {
		return []string{"http://" + net.JoinHostPort(host, port)}
	}
	urls := []string{"http://localhost:" + port}
	ifaces, _ := net.InterfaceAddrs()
	for _, a := range ifaces {
		if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() && !n.IP.IsLinkLocalUnicast() {
			urls = append(urls, "http://"+net.JoinHostPort(n.IP.String(), port))
		}
	}
	return urls
}
