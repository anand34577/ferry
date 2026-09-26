# Install the program

Ferry is a single program file with the web app built in. There is nothing else to install.

## Download

From the [latest release](https://github.com/anand34577/ferry/releases/latest), download the file for your system:

| System | File |
|---|---|
| Windows (most PCs) | `ferry_<version>_windows_amd64.zip` |
| Windows on ARM | `ferry_<version>_windows_arm64.zip` |
| macOS (Apple silicon: M1 and newer) | `ferry_<version>_darwin_arm64.tar.gz` |
| macOS (Intel) | `ferry_<version>_darwin_amd64.tar.gz` |
| Linux (most PCs and servers) | `ferry_<version>_linux_amd64.tar.gz` |
| Linux on 64-bit ARM (Raspberry Pi 4/5, ARM servers) | `ferry_<version>_linux_arm64.tar.gz` |
| Linux on 32-bit ARM (older Raspberry Pi) | `ferry_<version>_linux_armv7.tar.gz` |

Each download contains `ferry` (or `ferry.exe`), a settings file `ferry.env`, and the license.
To check a download, compare it with `SHA256SUMS.txt` on the same page (`sha256sum -c SHA256SUMS.txt`).

Unpack it to a folder of your choice:

- **Windows:** right-click the ZIP → **Extract All**.
- **macOS / Linux:** `tar -xzf ferry_*.tar.gz && cd ferry_*/`

## Try it out

**Windows:** double-click `ferry.exe`. A window opens and shows the address. Keep it open while you use Ferry; close it to stop.
If Windows SmartScreen appears, choose **More info → Run anyway**. When Windows Firewall asks, allow access on **private networks** so phones and other computers can connect.

**macOS / Linux:** in the unpacked folder, run:

```bash
./ferry
```

Press `Ctrl+C` to stop.
On macOS, if the system blocks the program because it was downloaded from the internet, run `xattr -d com.apple.quarantine ferry` once.

Then open **http://localhost:8080** and create the administrator account. Data is stored in a `data` folder next to the program.

## Run as a system service

A service starts automatically when the computer starts, runs in the background, and restarts if something goes wrong. Ferry installs itself as one with a single command.

### Linux

```bash
sudo ./ferry service install
```

This works on any Linux with systemd (Ubuntu, Debian, Fedora, Raspberry Pi OS and most others). It:

- copies the program to `/usr/local/bin/ferry`,
- creates a system user `ferry` that the service runs as,
- stores data in `/var/lib/ferry` and settings in `/etc/ferry/ferry.env`,
- starts Ferry now and at every boot.

### macOS

```bash
sudo ./ferry service install
```

Ferry runs in the background as your user account and starts at boot. Data: `/usr/local/var/ferry`, settings: `/usr/local/etc/ferry/ferry.env`, log: `/usr/local/var/log/ferry.log`.

### Windows

1. Open the Start menu, type **Terminal** (or **PowerShell**), right-click it and choose **Run as administrator**.
2. Go to the unpacked folder and install:

   ```powershell
   cd "$HOME\Downloads\ferry_<version>_windows_amd64"
   .\ferry.exe service install
   ```

Ferry is installed to `C:\Program Files\Ferry` and runs as a Windows service (**Ferry** in the Services app) under a low-privilege system account. It starts with Windows and restarts automatically after a failure. Data and settings are in `C:\ProgramData\Ferry`; the log is `C:\ProgramData\Ferry\ferry.log`. Access from other devices is allowed on private and domain networks.

### After installing

The command prints the addresses where Ferry can be opened. Open one in a browser and create the administrator account.

If you edited `ferry.env` next to the program before installing, those settings are carried over. To listen on another port, install with `service install -addr :9000`.

## Manage the service

On Linux and macOS, put `sudo` in front of each command. On Windows, use an Administrator terminal.

| Task | Command |
|---|---|
| Status | `ferry service status` |
| Stop / start | `ferry service stop` · `ferry service start` |
| Restart (after changing settings) | `ferry service restart` |
| Remove the service (keeps data and settings) | `ferry service uninstall` |

**Logs**

| System | Command |
|---|---|
| Linux | `journalctl -u ferry -f` |
| macOS | `tail -f /usr/local/var/log/ferry.log` |
| Windows | `Get-Content "C:\ProgramData\Ferry\ferry.log" -Wait -Tail 50` |

## Change settings

Edit the settings file, then restart Ferry.

| How Ferry runs | Settings file |
|---|---|
| By hand | `ferry.env` next to the program |
| Service on Linux | `/etc/ferry/ferry.env` |
| Service on macOS | `/usr/local/etc/ferry/ferry.env` |
| Service on Windows | `C:\ProgramData\Ferry\ferry.env` |

Remove the `#` in front of a setting to use it. `ferry config path` shows which file is in use. All settings: [Configuration](configuration.md).

## Admin commands

The `ferry` command also manages users from a terminal — useful if you are locked out.

| System | Example |
|---|---|
| Linux service | `sudo -u ferry ferry user list` |
| macOS service | `ferry user list` |
| Windows service | `& "C:\Program Files\Ferry\ferry.exe" user list` (Administrator terminal) |
| Run by hand | `./ferry user list` in the program folder |

All commands: [Administration → Command line](administration.md#command-line).

## Update

1. Download and unpack the new version.
2. Run the install command again from the new folder (`sudo ./ferry service install`, or `.\ferry.exe service install` on Windows). It replaces the program and keeps your data and settings.

If you run Ferry by hand, stop it and replace the program file. Database updates are applied automatically on start. Make a [backup](administration.md#backup-and-restore) first.

## Uninstall

1. `ferry service uninstall` (with `sudo`, or in an Administrator terminal on Windows).
2. To also delete all files and settings, remove the data and settings folders listed above, and the program (`/usr/local/bin/ferry` or `C:\Program Files\Ferry`). On Linux, `sudo userdel ferry` removes the service user.

## Next steps

- [Put Ferry online with HTTPS](https.md)
- [Backup and restore](administration.md#backup-and-restore)
