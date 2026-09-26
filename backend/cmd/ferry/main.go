// Command ferry runs the Ferry server and provides admin CLI commands.
package main

import (
	"context"
	"crypto/tls"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"ferry/internal/config"
	"ferry/internal/db"
	"ferry/internal/server"
	"ferry/internal/service"
	"ferry/internal/storage"
)

var version = "dev" // set with -ldflags "-X main.version=v1.0.0"

const usage = `Ferry — self-hosted file sharing

Usage:
  ferry [serve]                             Start the server
  ferry service install [-addr :8080]       Install and start Ferry as a system service
  ferry service uninstall                   Remove the service (keeps your data)
  ferry service start|stop|restart|status   Control the service
  ferry config example                      Print an example configuration file
  ferry config path                         Show which configuration file is used
  ferry user list                           List users
  ferry user create -email E -password P [-name N] [-admin]
  ferry user reset-password -email E -password P
  ferry user set-role -email E -role admin|user
  ferry user disable-2fa -email E           Turn off two-factor sign-in for a user
  ferry cleanup                             Run the cleanup job once
  ferry healthcheck                         Exit 0 if the local server is healthy
  ferry version                             Print the version

Options:
  --config FILE                             Configuration file (default: ferry.env next to the program)

Settings come from the configuration file and FERRY_* environment variables (environment wins).
`

func main() {
	args, cfgFlag := extractConfigFlag(os.Args[1:])
	cfgFile := config.FilePath(cfgFlag)
	if cfgFile == "" {
		cfgFile = serviceConfig()
	}
	if cfgFile != "" {
		if err := config.LoadFile(cfgFile); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	cmd := "serve"
	if len(args) > 0 {
		cmd, args = args[0], args[1:]
	}
	var err error
	if cmd == "user" || cmd == "cleanup" {
		if err := checkNotRoot(cfgFile); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
	}
	switch cmd {
	case "serve":
		var isService bool
		if isService, err = service.RunAsService(serve); !isService {
			ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			err = serve(ctx)
			stop()
		} else if err != nil {
			logServiceError(err)
		}
	case "service":
		err = serviceCmd(args, cfgFile)
	case "config":
		err = configCmd(args, cfgFile)
	case "version", "--version", "-v":
		fmt.Println("ferry", version)
	case "healthcheck":
		err = healthcheck()
	case "cleanup":
		err = withDB(func(ctx context.Context, cfg *config.Config, d *db.DB) error {
			st, err := storage.NewLocal(cfg.StoragePath)
			if err != nil {
				return err
			}
			s, err := server.New(ctx, cfg, d, st, slog.New(slog.NewTextHandler(os.Stderr, nil)), server.NewLogRing(10), version)
			if err != nil {
				return err
			}
			fmt.Println(s.Cleanup(ctx))
			return nil
		})
	case "user":
		err = userCmd(args)
	case "help", "-h", "--help":
		fmt.Print(usage)
	default:
		fmt.Fprint(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		pauseIfDoubleClicked()
		os.Exit(1)
	}
}

func newLogger(cfg *config.Config, ring *server.LogRing) *slog.Logger {
	lvl := slog.LevelInfo
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	w := io.MultiWriter(os.Stderr, ring)
	if cfg.LogFile != "" {
		if f, err := openLogFile(cfg.LogFile); err == nil {
			w = io.MultiWriter(os.Stderr, ring, f)
		} else {
			fmt.Fprintln(os.Stderr, "log file:", err)
		}
	}
	opts := &slog.HandlerOptions{Level: lvl}
	if cfg.LogFormat == "json" {
		return slog.New(slog.NewJSONHandler(w, opts))
	}
	return slog.New(slog.NewTextHandler(w, opts))
}

// serve runs the server until ctx is cancelled.
func serve(ctx context.Context) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ring := server.NewLogRing(300)
	log := newLogger(cfg, ring)

	d, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	st, err := storage.NewLocal(cfg.StoragePath)
	if err != nil {
		return err
	}
	s, err := server.New(ctx, cfg, d, st, log, ring, version)
	if err != nil {
		return err
	}
	go s.RunCleanup(ctx)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 20 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    64 << 10,
		// No Read/WriteTimeout: multi-gigabyte transfers legitimately take hours.
		ErrorLog: slog.NewLogLogger(log.Handler(), slog.LevelDebug),
	}
	errc := make(chan error, 1)
	go func() {
		scheme := "http"
		if cfg.TLSCert != "" {
			scheme = "https"
		}
		log.Info("ferry started", "version", version, "addr", scheme+"://"+displayAddr(cfg.Addr), "db", cfg.DBDriver, "storage", cfg.StoragePath)
		var users int
		d.QueryRow(ctx, `SELECT COUNT(*) FROM users`).Scan(&users)
		if users == 0 {
			log.Info("open Ferry in a browser to create the administrator account", "url", scheme+"://"+displayAddr(cfg.Addr))
		}
		if cfg.TLSCert != "" {
			errc <- srv.ListenAndServeTLS(cfg.TLSCert, cfg.TLSKey)
		} else {
			if cfg.PublicURL == "" {
				log.Info("FERRY_PUBLIC_URL is not set; set it when Ferry is reachable from the internet (needed for password reset by email)")
			} else if strings.HasPrefix(cfg.PublicURL, "http://") {
				log.Warn("FERRY_PUBLIC_URL is not HTTPS — use HTTPS (directly or via a reverse proxy) in production")
			}
			errc <- srv.ListenAndServe()
		}
	}()
	select {
	case err := <-errc:
		if !errors.Is(err, http.ErrServerClosed) {
			return err
		}
	case <-ctx.Done():
		log.Info("shutting down (waiting up to 30s for active requests)")
		sctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		srv.Shutdown(sctx)
	}
	return nil
}

func displayAddr(a string) string {
	if strings.HasPrefix(a, ":") {
		return "localhost" + a
	}
	return a
}

func healthcheck() error {
	addr := os.Getenv("FERRY_ADDR")
	if addr == "" {
		addr = ":8080"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	scheme := "http"
	client := &http.Client{Timeout: 5 * time.Second}
	if os.Getenv("FERRY_TLS_CERT") != "" {
		scheme = "https"
		client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}} // local check; cert is for the public hostname
	}
	resp, err := client.Get(scheme + "://" + net.JoinHostPort(host, port) + "/readyz")
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("not ready: %s", resp.Status)
	}
	return nil
}

func withDB(fn func(ctx context.Context, cfg *config.Config, d *db.DB) error) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx := context.Background()
	d, err := db.Open(cfg.DBDriver, cfg.DBDSN)
	if err != nil {
		return err
	}
	defer d.Close()
	if err := d.Migrate(ctx); err != nil {
		return err
	}
	return fn(ctx, cfg, d)
}

func userCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("missing user subcommand (list, create, reset-password, set-role, disable-2fa)")
	}
	sub := args[0]
	fs := flag.NewFlagSet("user "+sub, flag.ExitOnError)
	email := fs.String("email", "", "email address")
	password := fs.String("password", os.Getenv("FERRY_PASSWORD"), "password (or FERRY_PASSWORD env)")
	name := fs.String("name", "", "display name")
	admin := fs.Bool("admin", false, "create as administrator")
	role := fs.String("role", "", "admin or user")
	fs.Parse(args[1:])
	return withDB(func(ctx context.Context, cfg *config.Config, d *db.DB) error {
		switch sub {
		case "list":
			rows, err := d.Query(ctx, `SELECT email, name, role, disabled FROM users ORDER BY created_at`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var e, n, r string
				var dis int
				if err := rows.Scan(&e, &n, &r, &dis); err != nil {
					return err
				}
				state := ""
				if dis == 1 {
					state = " (disabled)"
				}
				fmt.Printf("%-32s %-24s %s%s\n", e, n, r, state)
			}
			return rows.Err()
		case "create":
			r := "user"
			if *admin {
				r = "admin"
			}
			u, err := server.CreateUser(ctx, d, *email, *name, *password, r)
			if err != nil {
				return err
			}
			fmt.Printf("created %s (%s)\n", u.Email, u.Role)
			return nil
		case "reset-password":
			var id string
			if err := d.QueryRow(ctx, `SELECT id FROM users WHERE email = ?`, strings.ToLower(*email)).Scan(&id); err != nil {
				return fmt.Errorf("no user with email %q", *email)
			}
			if err := server.SetPassword(ctx, d, id, *password, ""); err != nil {
				return err
			}
			fmt.Println("password updated; all sessions signed out")
			return nil
		case "set-role":
			if *role != "admin" && *role != "user" {
				return errors.New("-role must be admin or user")
			}
			res, err := d.Exec(ctx, `UPDATE users SET role = ? WHERE email = ?`, *role, strings.ToLower(*email))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("no user with email %q", *email)
			}
			fmt.Println("role updated")
			return nil
		case "disable-2fa":
			res, err := d.Exec(ctx, `UPDATE users SET totp_enabled = 0, totp_secret = '', totp_last_step = 0 WHERE email = ?`, strings.ToLower(*email))
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return fmt.Errorf("no user with email %q", *email)
			}
			fmt.Println("two-factor authentication turned off")
			return nil
		}
		return fmt.Errorf("unknown user subcommand %q", sub)
	})
}
