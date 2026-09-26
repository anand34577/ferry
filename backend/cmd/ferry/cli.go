package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"ferry/internal/service"
)

// extractConfigFlag removes "--config FILE" / "--config=FILE" (or a single dash) from args.
func extractConfigFlag(args []string) (rest []string, file string) {
	for i := 0; i < len(args); i++ {
		a := args[i]
		switch {
		case a == "--config" || a == "-config":
			if i+1 < len(args) {
				file = args[i+1]
				i++
			}
		case strings.HasPrefix(a, "--config="), strings.HasPrefix(a, "-config="):
			file = a[strings.Index(a, "=")+1:]
		default:
			rest = append(rest, a)
		}
	}
	return rest, file
}

func serviceCmd(args []string, cfgFile string) error {
	if len(args) == 0 {
		return errors.New("usage: ferry service install|uninstall|start|stop|restart|status")
	}
	switch args[0] {
	case "install":
		fs := flag.NewFlagSet("service install", flag.ExitOnError)
		addr := fs.String("addr", ":8080", "address to listen on (used when creating the configuration file)")
		fs.Parse(args[1:])
		base := envExample
		if cfgFile != "" { // carry over settings from the file next to the program
			if b, err := os.ReadFile(cfgFile); err == nil {
				base = strings.ReplaceAll(string(b), "\r\n", "\n")
			}
		}
		render := func(dataDir, logFile, addr string) string { return envForService(base, dataDir, logFile, addr) }
		p, err := service.Install(service.Options{Addr: *addr, ConfigFor: render})
		if err != nil {
			return err
		}
		fmt.Println("Ferry is installed and running.")
		fmt.Println()
		for _, u := range service.URLs(*addr) {
			fmt.Println("  Open:   ", u)
		}
		fmt.Println("  Config: ", p.Config)
		fmt.Println("  Data:   ", p.DataDir)
		fmt.Println("  Logs:   ", service.LogsHint())
		fmt.Println()
		fmt.Println("The first visit creates the administrator account.")
		fmt.Println("After editing the config file, run: ferry service restart")
		return nil
	case "uninstall":
		p, err := service.Uninstall()
		if err != nil {
			return err
		}
		fmt.Println("The Ferry service was removed. Your data and settings were kept:")
		fmt.Println("  Config: ", p.Config)
		fmt.Println("  Data:   ", p.DataDir)
		return nil
	case "start":
		return service.Start()
	case "stop":
		return service.Stop()
	case "restart":
		return service.Restart()
	case "status":
		return service.Status()
	}
	return fmt.Errorf("unknown service command %q (install, uninstall, start, stop, restart, status)", args[0])
}

func configCmd(args []string, file string) error {
	if len(args) == 1 && args[0] == "example" {
		fmt.Print(envExample)
		return nil
	}
	if len(args) == 1 && args[0] == "path" {
		if file == "" {
			fmt.Println("No configuration file; using FERRY_* environment variables and defaults.")
		} else {
			abs, _ := filepath.Abs(file)
			fmt.Println(abs)
		}
		return nil
	}
	return errors.New("usage: ferry config example|path")
}

// openLogFile appends to path, first moving a file over 10 MB to path.1 (one old file is kept).
func openLogFile(path string) (*os.File, error) {
	if st, err := os.Stat(path); err == nil && st.Size() > 10<<20 {
		os.Rename(path, path+".1")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	return os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o640)
}

// logServiceError records a failure to start when there is no console (Windows service).
func logServiceError(err error) {
	path := os.Getenv("FERRY_LOG_FILE")
	if path == "" {
		path = service.Default().LogFile
	}
	if path == "" {
		return
	}
	if f, e := openLogFile(path); e == nil {
		fmt.Fprintf(f, "%s ERROR ferry could not start: %v\n", time.Now().Format(time.RFC3339), err)
		f.Close()
	}
}

// serviceConfig returns the installed service's config file when no other file was found, so admin
// commands (e.g. "ferry user list") work on the same data as the running service.
func serviceConfig() string {
	p := service.Default().Config
	if p == "" {
		return ""
	}
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// checkNotRoot stops data commands run as root on a Linux service install: files root creates next to
// the database would be unreadable for the service's own "ferry" user.
func checkNotRoot(cfgFile string) error {
	if os.Geteuid() == 0 && cfgFile != "" && cfgFile == serviceConfig() && runtime.GOOS == "linux" {
		return errors.New("run this as the ferry user, e.g.: sudo -u ferry ferry " + strings.Join(os.Args[1:], " "))
	}
	return nil
}
