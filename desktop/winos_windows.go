package main

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// seal encrypts a secret for the current Windows user (DPAPI); other users and other computers can't read it.
func seal(plain string) (string, error) {
	if plain == "" {
		return "", nil
	}
	in := windows.DataBlob{Size: uint32(len(plain)), Data: unsafe.SliceData([]byte(plain))}
	var out windows.DataBlob
	if err := windows.CryptProtectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", err
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return base64.StdEncoding.EncodeToString(unsafe.Slice(out.Data, out.Size)), nil
}

func unseal(enc string) (string, error) {
	if enc == "" {
		return "", errors.New("not signed in")
	}
	b, err := base64.StdEncoding.DecodeString(enc)
	if err != nil || len(b) == 0 {
		return "", errors.New("saved sign-in is damaged")
	}
	in := windows.DataBlob{Size: uint32(len(b)), Data: &b[0]}
	var out windows.DataBlob
	if err := windows.CryptUnprotectData(&in, nil, nil, 0, nil, windows.CRYPTPROTECT_UI_FORBIDDEN, &out); err != nil {
		return "", errors.New("saved sign-in can't be read on this Windows account — please sign in again")
	}
	defer windows.LocalFree(windows.Handle(unsafe.Pointer(out.Data)))
	return string(unsafe.Slice(out.Data, out.Size)), nil
}

const runKey = `Software\Microsoft\Windows\CurrentVersion\Run`

// setAutostart adds or removes Ferry from the programs Windows starts at sign-in (per user, no admin rights).
func setAutostart(on bool) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKey, registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if !on {
		err := k.DeleteValue("Ferry")
		if errors.Is(err, registry.ErrNotExist) || errors.Is(err, syscall.ERROR_FILE_NOT_FOUND) {
			return nil
		}
		return err
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	return k.SetStringValue("Ferry", `"`+exe+`" --background`)
}

// revealInExplorer opens Explorer with the file selected (or the folder itself).
func revealInExplorer(path string) error {
	if st, err := os.Stat(path); err == nil && st.IsDir() {
		return exec.Command("explorer.exe", path).Start()
	}
	cmd := exec.Command("explorer.exe")
	cmd.SysProcAttr = &syscall.SysProcAttr{CmdLine: `explorer.exe /select,"` + path + `"`}
	return cmd.Start()
}

// openPath opens a file with its default program.
func openPath(path string) error {
	return windows.ShellExecute(0, windows.StringToUTF16Ptr("open"), windows.StringToUTF16Ptr(path), nil, nil, windows.SW_SHOWNORMAL)
}
