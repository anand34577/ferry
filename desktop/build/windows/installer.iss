; Ferry for Windows — Inno Setup installer.
; Build (after `wails build`):  ISCC.exe /DAppVersion=1.7.1 build\windows\installer.iss
; Output: build\bin\Ferry-setup.exe

#ifndef AppVersion
  #define AppVersion "0.0.0"
#endif
; The Ferry server program is ferry.exe; a distinct name keeps "close the running app" from touching it.
#define AppExe "FerryDesktop.exe"

[Setup]
AppId={{8F3C2A51-6B7D-4E0A-9C1F-3D5B7A2E9F10}
AppName=Ferry
AppVersion={#AppVersion}
AppVerName=Ferry {#AppVersion}
AppPublisher=Ferry
AppPublisherURL=https://github.com/anand34577/ferry
AppSupportURL=https://github.com/anand34577/ferry/issues
VersionInfoVersion={#AppVersion}
VersionInfoDescription=Ferry installer
DefaultDirName={autopf}\Ferry
DefaultGroupName=Ferry
DisableProgramGroupPage=yes
; Administrator rights are needed once, for the firewall rule that lets nearby devices reach Ferry.
PrivilegesRequired=admin
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
MinVersion=10.0
OutputDir=..\bin
OutputBaseFilename=Ferry-setup
SetupIconFile=icon.ico
UninstallDisplayIcon={app}\{#AppExe}
UninstallDisplayName=Ferry
WizardStyle=modern
Compression=lzma2/max
SolidCompression=yes
CloseApplications=no

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "Create a &desktop shortcut"; GroupDescription: "Shortcuts:"
Name: "sendto"; Description: "Add Ferry to Explorer's ""Send to"" menu"; GroupDescription: "Shortcuts:"

[Files]
Source: "..\bin\{#AppExe}"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{autoprograms}\Ferry"; Filename: "{app}\{#AppExe}"
Name: "{autodesktop}\Ferry"; Filename: "{app}\{#AppExe}"; Tasks: desktopicon
Name: "{usersendto}\Ferry"; Filename: "{app}\{#AppExe}"; Tasks: sendto

[Run]
; Let nearby devices reach Ferry on home and work networks (not on public ones).
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Ferry"""; Flags: runhidden waituntilterminated
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall add rule name=""Ferry"" description=""Nearby file transfers (LocalSend compatible)"" dir=in action=allow program=""{app}\{#AppExe}"" enable=yes profile=private,domain"; Flags: runhidden waituntilterminated; StatusMsg: "Allowing Ferry through Windows Firewall…"
; Start as the signed-in user, not elevated.
Filename: "{app}\{#AppExe}"; Description: "Start Ferry"; Flags: nowait postinstall skipifsilent runasoriginaluser

[UninstallRun]
Filename: "{sys}\taskkill.exe"; Parameters: "/F /IM {#AppExe}"; Flags: runhidden waituntilterminated; RunOnceId: "StopFerry"
Filename: "{sys}\netsh.exe"; Parameters: "advfirewall firewall delete rule name=""Ferry"""; Flags: runhidden waituntilterminated; RunOnceId: "RemoveFirewallRule"

[UninstallDelete]
; The app window's browser cache. Settings (%APPDATA%\Ferry) and received files are kept.
Type: filesandordirs; Name: "{localappdata}\Ferry\WebView2"

[Code]
const
  WebView2Key = 'SOFTWARE\Microsoft\EdgeUpdate\Clients\{F3017226-FE2A-4295-8BDF-00C3A9A7E4C5}';
  WebView2Url = 'https://go.microsoft.com/fwlink/p/?LinkId=2124703';

function HasWebView2: Boolean;
var
  V: String;
begin
  Result :=
    (RegQueryStringValue(HKLM32, WebView2Key, 'pv', V) and (V <> '') and (V <> '0.0.0.0')) or
    (RegQueryStringValue(HKLM64, WebView2Key, 'pv', V) and (V <> '') and (V <> '0.0.0.0')) or
    (RegQueryStringValue(HKCU, WebView2Key, 'pv', V) and (V <> '') and (V <> '0.0.0.0'));
end;

// An update replaces the program: stop a running copy first (it lives on in the tray), and install the
// WebView2 runtime the window needs when it is missing (Windows 11 already has it).
function PrepareToInstall(var NeedsRestart: Boolean): String;
var
  Code: Integer;
begin
  Result := '';
  Exec(ExpandConstant('{sys}\taskkill.exe'), '/F /IM {#AppExe}', '', SW_HIDE, ewWaitUntilTerminated, Code);
  if not HasWebView2 then
  begin
    try
      DownloadTemporaryFile(WebView2Url, 'MicrosoftEdgeWebview2Setup.exe', '', nil);
      if not Exec(ExpandConstant('{tmp}\MicrosoftEdgeWebview2Setup.exe'), '/silent /install', '', SW_HIDE, ewWaitUntilTerminated, Code) or (Code <> 0) then
        Result := 'The Microsoft Edge WebView2 runtime could not be installed (error ' + IntToStr(Code) + '). Install it from https://developer.microsoft.com/microsoft-edge/webview2/ and run this setup again.';
    except
      Result := 'Ferry needs the Microsoft Edge WebView2 runtime, and it could not be downloaded. Connect to the internet, or install it from https://developer.microsoft.com/microsoft-edge/webview2/ and run this setup again.';
    end;
  end;
end;

// Remove the "start with Windows" entry the app may have created for this user.
procedure CurUninstallStepChanged(CurUninstallStep: TUninstallStep);
begin
  if CurUninstallStep = usPostUninstall then
    RegDeleteValue(HKCU, 'Software\Microsoft\Windows\CurrentVersion\Run', 'Ferry');
end;
