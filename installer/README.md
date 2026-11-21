# Windows installer

`muxshift.iss` is the per-user Inno Setup script. It installs the files from an
already built release directory under `%LocalAppData%\Programs\MuxShift`,
creates Start Menu shortcuts and an optional desktop shortcut, and registers an
uninstall entry.

Build the release first, then the setup:

```powershell
scripts\release.ps1
scripts\build-installer.ps1
```

`build-installer.ps1` needs Inno Setup 6 (`ISCC.exe`); pass `-Iscc` when it is
not in one of the standard install directories. The output is
`dist\muxshift-<version>-windows-amd64-setup.exe`.

The installer does not download or replace an existing copy; it writes only the
release directory it is given. Silent installation for a check:

```powershell
dist\muxshift-0.0.3-windows-amd64-setup.exe /VERYSILENT /NORESTART /DIR=C:\Temp\MuxShift-Test
```
