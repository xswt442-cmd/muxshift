; MuxShift's Windows installer. The release build script passes the version and
; the directory holding muxshift.exe, README.md, VERSION.txt and the other
; release files; the resulting setup installs per user, creates a Start Menu
; shortcut, offers a desktop shortcut, and registers an uninstall entry.

#ifndef MyAppVersion
  #define MyAppVersion "0.0.0"
#endif
#ifndef MySourceDir
  #define MySourceDir "..\dist\muxshift-" + MyAppVersion + "-windows-amd64"
#endif
#ifndef MyOutputDir
  #define MyOutputDir "..\dist"
#endif

#define MyAppName "MuxShift"
#define MyAppExeName "muxshift.exe"
#define MyAppPublisher "MuxShift contributors"
#define MyAppURL "https://github.com/xswt442-cmd/muxshift"

; The window reaches a website address only through the extension that ships
; beside it. A release directory built before the extension was packaged would
; install a window whose About page points at nothing, so refuse to build it
; rather than hand out a setup that cannot do what the README says.
#if !FileExists(AddBackslash(MySourceDir) + "extension\manifest.json")
  #error MySourceDir carries no extension\manifest.json; build the release directory with scripts\release.ps1 first
#endif

[Setup]
AppId={{15A6423E-2ACC-4222-839A-EE78D68D7D21}
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppVerName={#MyAppName} {#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
AppSupportURL={#MyAppURL}
AppUpdatesURL={#MyAppURL}/releases
DefaultDirName={localappdata}\Programs\MuxShift
DefaultGroupName=MuxShift
DisableProgramGroupPage=yes
PrivilegesRequired=lowest
OutputDir={#MyOutputDir}
OutputBaseFilename=muxshift-{#MyAppVersion}-windows-amd64-setup
SetupIconFile=..\desktop\internal\win\icon.ico
UninstallDisplayIcon={app}\{#MyAppExeName}
Compression=lzma2/ultra64
SolidCompression=yes
WizardStyle=modern
ArchitecturesAllowed=x64compatible
ArchitecturesInstallIn64BitMode=x64compatible
LicenseFile=..\LICENSE
VersionInfoVersion={#MyAppVersion}
VersionInfoProductName={#MyAppName}
VersionInfoDescription={#MyAppName} installer
CloseApplications=yes

[Languages]
Name: "english"; MessagesFile: "compiler:Default.isl"

[Tasks]
Name: "desktopicon"; Description: "{cm:CreateDesktopIcon}"; GroupDescription: "{cm:AdditionalIcons}"; Flags: unchecked

[Files]
Source: "{#MySourceDir}\*"; DestDir: "{app}"; Flags: ignoreversion recursesubdirs createallsubdirs

[Icons]
Name: "{autoprograms}\MuxShift"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"
Name: "{autoprograms}\Uninstall MuxShift"; Filename: "{uninstallexe}"
Name: "{autodesktop}\MuxShift"; Filename: "{app}\{#MyAppExeName}"; WorkingDir: "{app}"; Tasks: desktopicon

[Run]
Filename: "{app}\{#MyAppExeName}"; Description: "{cm:LaunchProgram,MuxShift}"; Flags: nowait postinstall skipifsilent
