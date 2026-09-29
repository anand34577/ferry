// Ferry for Windows: send files to nearby devices directly (LocalSend compatible, no internet needed),
// to your own devices anywhere through your Ferry server, or to anyone with a link.
package main

import (
	"embed"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"

	"ferrydesktop/internal/ferry"
)

//go:embed all:frontend/dist
var assets embed.FS

//go:embed build/windows/icon.ico
var trayIcon []byte

var version = "dev" // set with -ldflags "-X main.version=v1.4.0"

func main() {
	base, err := os.UserConfigDir()
	if err != nil {
		base = os.TempDir()
	}
	dataDir := filepath.Join(base, "Ferry")
	os.MkdirAll(dataDir, 0o700)
	log := newLogger(filepath.Join(dataDir, "ferry.log"))
	ferry.UserAgent = "windows/" + strings.TrimPrefix(version, "v") + " api=1"

	background := false
	var files []string
	for _, a := range os.Args[1:] {
		if a == "--background" {
			background = true
		} else if !strings.HasPrefix(a, "-") {
			files = append(files, a)
		}
	}
	app := NewApp(version, dataDir, log)
	app.pending = files

	local, _ := os.UserCacheDir()
	err = wails.Run(&options.App{
		Title:     "Ferry",
		Width:     1120,
		Height:    760,
		MinWidth:  880,
		MinHeight: 600,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		BackgroundColour:         &options.RGBA{R: 14, G: 15, B: 17, A: 255},
		StartHidden:              background,
		OnStartup:                app.startup,
		OnShutdown:               app.shutdown,
		OnBeforeClose:            app.beforeClose,
		Bind:                     []interface{}{app},
		EnableDefaultContextMenu: false,
		DragAndDrop:              &options.DragAndDrop{EnableFileDrop: true, DisableWebViewDrop: true},
		SingleInstanceLock: &options.SingleInstanceLock{
			UniqueId: "dev.ferry.desktop",
			OnSecondInstanceLaunch: func(d options.SecondInstanceData) {
				var paths []string
				for _, a := range d.Args {
					if a == "--background" || strings.HasPrefix(a, "-") {
						continue
					}
					if !filepath.IsAbs(a) {
						a = filepath.Join(d.WorkingDirectory, a)
					}
					paths = append(paths, a)
				}
				app.showWindow()
				app.handOver(paths)
			},
		},
		Windows: &windows.Options{
			Theme:               windows.SystemDefault,
			WebviewUserDataPath: filepath.Join(local, "Ferry", "WebView2"),
			DisablePinchZoom:    true,
		},
	})
	if err != nil {
		log.Error("ferry stopped", "err", err)
		fmt.Fprintln(os.Stderr, "Error:", err)
		os.Exit(1)
	}
}

// newLogger writes to ferry.log in the data folder (the previous log is kept as ferry.log.1 above 5 MB).
func newLogger(path string) *slog.Logger {
	if st, err := os.Stat(path); err == nil && st.Size() > 5<<20 {
		os.Rename(path, path+".1")
	}
	var w io.Writer = os.Stderr
	if f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600); err == nil {
		w = f
	}
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: slog.LevelInfo}))
}
