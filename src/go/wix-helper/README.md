# wix-helper

This is a Windows Installer custom action, used to detect whether WSL is
installed (to prevent the application from being installed in that case).  It
can also update WSL (via `wsl --update`).
