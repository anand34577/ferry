package main

import (
	"runtime"
	"sync"

	"github.com/energye/systray"
	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// The tray icon keeps Ferry receiving while its window is closed (like LocalSend and cloud-sync apps).
var tray struct {
	sync.Mutex
	receiving *systray.MenuItem
}

func startTray(a *App, icon []byte) {
	go func() {
		runtime.LockOSThread() // the tray's hidden window and its message loop must stay on one thread
		systray.Run(func() {
			systray.SetIcon(icon)
			systray.SetTooltip("Ferry")
			systray.SetOnClick(func(systray.IMenu) { a.showWindow() })
			systray.SetOnDClick(func(systray.IMenu) { a.showWindow() })
			systray.SetOnRClick(func(m systray.IMenu) { m.ShowMenu() })
			systray.AddMenuItem("Open Ferry", "").Click(a.showWindow)
			recv := systray.AddMenuItemCheckbox("Receive files", "Let nearby devices send to this PC", a.settings.get().Receiving)
			recv.Click(func() {
				on := !a.settings.get().Receiving
				a.SaveSettings(SettingsPatch{Receiving: &on})
			})
			systray.AddMenuItem("Open received files", "").Click(func() { a.OpenDownloads() })
			systray.AddSeparator()
			systray.AddMenuItem("Quit Ferry", "Stop sending and receiving").Click(a.Quit)
			tray.Lock()
			tray.receiving = recv
			tray.Unlock()
			updateTray(a)
		}, nil)
	}()
}

// updateTray mirrors the receiving state in the tray and tells the UI settings changed (e.g. from the tray).
func updateTray(a *App) {
	s := a.settings.get()
	tray.Lock()
	if tray.receiving != nil {
		if s.Receiving {
			tray.receiving.Check()
			systray.SetTooltip("Ferry — receiving as " + s.Alias)
		} else {
			tray.receiving.Uncheck()
			systray.SetTooltip("Ferry — not receiving")
		}
	}
	tray.Unlock()
	if a.ctx != nil {
		wruntime.EventsEmit(a.ctx, "state", a.GetState())
	}
}
