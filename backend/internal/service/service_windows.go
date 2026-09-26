package service

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"golang.org/x/sys/windows/svc"
	"golang.org/x/sys/windows/svc/mgr"
)

const winName = "Ferry"

func dir(env, def string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return def
}

func Default() Paths {
	data := filepath.Join(dir("ProgramData", `C:\ProgramData`), "Ferry")
	return Paths{
		Binary:  filepath.Join(dir("ProgramFiles", `C:\Program Files`), "Ferry", "ferry.exe"),
		Config:  filepath.Join(data, "ferry.env"),
		DataDir: filepath.Join(data, "data"),
		LogFile: filepath.Join(data, "ferry.log"),
	}
}

func connect() (*mgr.Mgr, error) {
	m, err := mgr.Connect()
	if err != nil {
		return nil, errors.New("open an Administrator terminal (right-click → Run as administrator) and run the command again")
	}
	return m, nil
}

func Install(o Options) (Paths, error) {
	p := Default()
	m, err := connect()
	if err != nil {
		return p, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(winName)
	exists := err == nil
	if exists {
		defer s.Close()
		stopAndWait(s) // the binary can't be replaced while it runs
	}
	if err := copyBinary(p.Binary); err != nil {
		return p, fmt.Errorf("install binary: %w", err)
	}
	root := filepath.Dir(p.Config)
	if err := os.MkdirAll(p.DataDir, 0o750); err != nil {
		return p, err
	}
	if _, err := writeConfig(p, o); err != nil {
		return p, fmt.Errorf("write config: %w", err)
	}
	// The service runs as the low-privilege LocalService account (SID S-1-5-19); let it write its own folder.
	if err := run("icacls", root, "/grant", "*S-1-5-19:(OI)(CI)M", "/T", "/Q"); err != nil {
		return p, err
	}
	cfg := mgr.Config{
		DisplayName:      "Ferry",
		Description:      "Ferry file sharing server",
		StartType:        mgr.StartAutomatic,
		ServiceStartName: `NT AUTHORITY\LocalService`,
	}
	if exists {
		cur, err := s.Config()
		if err != nil {
			return p, err
		}
		cur.BinaryPathName = fmt.Sprintf(`"%s" serve --config "%s"`, p.Binary, p.Config)
		cur.DisplayName, cur.Description, cur.StartType, cur.ServiceStartName = cfg.DisplayName, cfg.Description, cfg.StartType, cfg.ServiceStartName
		if err := s.UpdateConfig(cur); err != nil {
			return p, err
		}
	} else {
		if s, err = m.CreateService(winName, p.Binary, cfg, "serve", "--config", p.Config); err != nil {
			return p, err
		}
		defer s.Close()
	}
	s.SetRecoveryActions([]mgr.RecoveryAction{
		{Type: mgr.ServiceRestart, Delay: 5 * time.Second},
		{Type: mgr.ServiceRestart, Delay: 30 * time.Second},
		{Type: mgr.ServiceRestart, Delay: time.Minute},
	}, 86400)
	// Allow other devices on home/work networks to connect (not on networks marked Public).
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=Ferry").Run()
	exec.Command("netsh", "advfirewall", "firewall", "add", "rule", "name=Ferry", "dir=in", "action=allow",
		"program="+p.Binary, "enable=yes", "profile=private,domain").Run()
	return p, s.Start()
}

func stopAndWait(s *mgr.Service) {
	st, err := s.Control(svc.Stop)
	for i := 0; err == nil && st.State != svc.Stopped && i < 60; i++ {
		time.Sleep(500 * time.Millisecond)
		st, err = s.Query()
	}
}

func Uninstall() (Paths, error) {
	p := Default()
	m, err := connect()
	if err != nil {
		return p, err
	}
	defer m.Disconnect()
	s, err := m.OpenService(winName)
	if err != nil {
		return p, errors.New("the Ferry service is not installed")
	}
	defer s.Close()
	stopAndWait(s)
	exec.Command("netsh", "advfirewall", "firewall", "delete", "rule", "name=Ferry").Run()
	return p, s.Delete()
}

func withService(fn func(*mgr.Service) error) error {
	m, err := connect()
	if err != nil {
		return err
	}
	defer m.Disconnect()
	s, err := m.OpenService(winName)
	if err != nil {
		return errors.New("the Ferry service is not installed (run: ferry service install)")
	}
	defer s.Close()
	return fn(s)
}

func Start() error { return withService(func(s *mgr.Service) error { return s.Start() }) }
func Stop() error {
	return withService(func(s *mgr.Service) error { stopAndWait(s); return nil })
}
func Restart() error {
	return withService(func(s *mgr.Service) error { stopAndWait(s); return s.Start() })
}

func Status() error {
	return withService(func(s *mgr.Service) error {
		st, err := s.Query()
		if err != nil {
			return err
		}
		names := map[svc.State]string{svc.Stopped: "stopped", svc.StartPending: "starting", svc.StopPending: "stopping", svc.Running: "running"}
		state := names[st.State]
		if state == "" {
			state = fmt.Sprint(st.State)
		}
		fmt.Println("Ferry service:", state)
		return nil
	})
}

func LogsHint() string { return `Get-Content "` + Default().LogFile + `" -Wait -Tail 50` }

// RunAsService runs fn under the Windows Service Control Manager when started as a service.
func RunAsService(fn func(ctx context.Context) error) (bool, error) {
	if ok, err := svc.IsWindowsService(); err != nil || !ok {
		return false, err
	}
	return true, svc.Run(winName, &handler{fn})
}

type handler struct {
	run func(ctx context.Context) error
}

func (h *handler) Execute(_ []string, req <-chan svc.ChangeRequest, status chan<- svc.Status) (bool, uint32) {
	status <- svc.Status{State: svc.StartPending}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- h.run(ctx) }()
	status <- svc.Status{State: svc.Running, Accepts: svc.AcceptStop | svc.AcceptShutdown}
	for {
		select {
		case err := <-done:
			if err != nil {
				return true, 1 // non-zero exit: recovery actions restart the service
			}
			return false, 0
		case c := <-req:
			switch c.Cmd {
			case svc.Interrogate:
				status <- c.CurrentStatus
			case svc.Stop, svc.Shutdown:
				status <- svc.Status{State: svc.StopPending, WaitHint: 35000}
				cancel()
				<-done
				return false, 0
			}
		}
	}
}
