param (
    [switch] $SkipTools,
    [switch] $SkipWSL
)

$InformationPreference = 'Continue'

Write-Information 'Installing components required for Rancher Desktop development...'

# Start separate jobs for things we want to install (in subprocesses).

if (!$SkipTools) {
    Start-Job -Name 'Install Tools' -ErrorAction Stop -ScriptBlock {
        Write-Information 'Installing Tools...'

        Invoke-WebRequest -UseBasicParsing -Uri 'https://get.scoop.sh' `
            | Invoke-Expression
        scoop install 7zip git go mingw nvm unzip
        # Install and use latest node 18* version
        nvm install 18
        nvm use $(nvm list | Select-String '[18\.[0-9.]+]' | Select-Object -First 1 | ForEach-Object { $_.Matches.Value })
        # Install the yarn package manager
        npm install --global yarn
    }
}

# Wait for all jobs to finish.
Get-Job | Receive-Job -Wait -ErrorAction Stop
# Show that all jobs are done
Get-Job
Write-Information 'Rancher Desktop development environment setup complete.'

if (! (Get-Command wsl -ErrorAction SilentlyContinue) -and !$SkipWSL) {
    Write-Information 'installing wsl.... This will require a restart'

    $targetDir = (Join-Path ([System.IO.Path]::GetTempPath()) rdinstall)
    New-Item -ItemType Directory -Force -Path $targetDir

    $files = ("install-wsl.ps1", "restart-helpers.ps1", "sudo-install-wsl.ps1", "uninstall-wsl.ps1")
    foreach ($file in $files) {
        $url = "https://raw.githubusercontent.com/rancher-sandbox/rancher-desktop/main/scripts/windows/$file"
        $outFile = (Join-Path $targetDir $file)
        Invoke-WebRequest -UseBasicParsing -Uri $url -OutFile $outFile
    }

    $sudoPath = (Join-Path $targetDir sudo-install-wsl.ps1)
    & $sudoPath -Step "EnableWSL-01"
}
