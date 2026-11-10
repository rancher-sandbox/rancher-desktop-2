# Rancher Desktop

[![Ask DeepWiki](https://deepwiki.com/badge.svg)](https://deepwiki.com/rancher-sandbox/rancher-desktop)

Rancher Desktop is an open-source project that brings Kubernetes and
container management to the desktop. It runs on Windows, macOS and
Linux. This README pertains to the development of Rancher Desktop.
For user-oriented information about Rancher Desktop, please see [rancherdesktop.io][home].
For user-oriented documentation, please see [docs.rancherdesktop.io][docs].

[home]: https://rancherdesktop.io
[docs]: https://docs.rancherdesktop.io


## Overview

Rancher Desktop is an Electron application that is mainly written in TypeScript.
It bundles a variety of other technologies in order to provide one cohesive application.
It includes a command line tool, `rdctl`, which is written in Go.
Most developer activities, such as running a development build, building/packaging
Rancher Desktop, running unit tests, and running end-to-end tests, are done through
`yarn` scripts. Some exceptions exist.


## Setup

### Windows

1. Install [Windows Subsystem for Linux (WSL)] on your machine. Skip this step, if WSL is already installed.
2. Open a PowerShell prompt (hit Windows Key + `X` and open `Windows PowerShell`).
3. Install [Scoop] via `iwr -useb get.scoop.sh | iex`.
4. Install 7zip, git, go, mingw, nvm, and unzip via `scoop install 7zip git go mingw nvm unzip`.
   Check node version with `nvm list`. If node v26 is not installed or set as the current version, then install using `nvm install 26` and set as current using `nvm use 26.xx.xx`.
5. Install Yarn as described under [Yarn](#yarn).
6. Configure `git` to work with linux- and macos-originated files:
   ```
   git config --global --replace-all core.autocrlf false
   git config --global --replace-all core.eol lf
   ```
If `make lint-go` fails mysteriously, the line endings may be wrong.

You can now clone the repository and run `yarn`.

[Scoop]: https://scoop.sh/
[Windows Subsystem for Linux (WSL)]: https://docs.microsoft.com/en-us/windows/wsl/install

### macOS

Install `nvm` to get Node.js and npm:

See https://github.com/nvm-sh/nvm#installing-and-updating and run the `curl` or `wget`
command to install nvm.

Note that this script adds code dealing with `nvm` to a profile file
(like `~/.bash_profile`). To add access to `nvm` to a current shell session,
you'll need to `source` that file.

Currently we build Rancher Desktop with Node 26. To install it, run:

```
nvm install 26
```

Next, install Yarn as described under [Yarn](#yarn).

You'll also need to run `brew install go` if you haven't installed go.

Then you can install dependencies with:
```
yarn
```

### Linux

Ensure you have the following installed:

- [Node.js][Node.js] v26.

- Yarn, as described under [Yarn](#yarn).

- Go 1.22 or later.

- A C compiler such as `gcc`, which cgo needs to build the `rdd` daemon.

Then you can install dependencies with:

```
yarn
```

You can then run Rancher Desktop as described below. It may fail on the first run -
if this happens, try doing a factory reset and re-running, which has been known
to solve this issue.

[Node.js]: https://nodejs.org/

### Yarn

We use Corepack to manage Yarn. Node 26 no longer includes Corepack, so
install it with npm and then enable Yarn:

```
npm install --global corepack
corepack enable yarn
```

If you previously installed Yarn 1 with `npm install --global yarn`,
remove it first with `npm uninstall --global yarn`. Otherwise installing
Corepack fails with an `EEXIST` error.


## Running

Once you have your dependencies installed you can run a development version
of Rancher Desktop with:

```
yarn dev
```

The Cluster Dashboard needs a Node build of its own, which `yarn install`
skips. To include it in a development build or a package, run
`make --directory=dashboard`, then rebuild rdd with
`make --directory=rdd build-rdd`.


## Tests

To run the unit tests:

```
yarn test
```

To run the integration tests:

```
yarn test:e2e
```


## Building

Rancher can be built from source on Windows, macOS or Linux.
Cross-compilation is currently not supported. To run a build do:

```
yarn build
yarn package
```

The build output goes to `dist/`.

### Debugging builds with the Chrome remote debugger

The Chrome remote debugger allows you to debug Electron apps using Chrome Developer Tools. You can use it to access log messages that might output to the developer console of the renderer process. This is especially helpful for getting additional debug information in production builds of Rancher Desktop.

#### Starting Rancher Desktop with Remote Debugging Enabled

To enable remote debugging, start Rancher Desktop with the `--remote-debugging-port` argument.

On Linux, start Rancher Desktop with the following command:

``` bash
rancher-desktop --remote-debugging-port="8315" --remote-allow-origins=http://localhost:8315
```

On macOS, start Rancher Desktop with the following command:

```
/Applications/Rancher\ Desktop.app/Contents/MacOS/Rancher\ Desktop --remote-debugging-port="8315" --remote-allow-origins=http://localhost:8315
```

On Windows, start Rancher Desktop with the following command:

``` powershell
cd 'C:\Program Files\Rancher Desktop\'
& '.\Rancher Desktop.exe' --remote-debugging-port="8315" --remote-allow-origins=http://localhost:8315
```

After Rancher Desktop starts, open Chrome and navigate to `http://localhost:8315/`. Select the available target to start remote debugging Rancher Desktop.

![image](https://github.com/rak-phillip/rancher-desktop/assets/835961/4f5fcb33-d381-4900-a836-685eab3af441)

![image](https://github.com/rak-phillip/rancher-desktop/assets/835961/91b4ee63-7093-4377-b8b3-f2f4a57a16a7)

#### Remote Debugging an Extension

To remote debug an extension, follow the same process as remote debugging a build. However, you will need to load an extension before navigating to `http://localhost:8315/`. Both Rancher Desktop and the loaded extension should be listed as available targets.

![image](https://github.com/rak-phillip/rancher-desktop/assets/835961/71bb7eec-38e5-4744-a547-ebb36048918a)

![image](https://github.com/rak-phillip/rancher-desktop/assets/835961/f4aad3e1-dabc-473e-9404-05609216cd03)

### Debugging dev env with GoLand

The following steps have been tested with GoLand on Linux but might work for other
JetBrains IDEs in a similar way.

1. Install the Node.js plugin (via `File > Settings > Plugins`)

   ![image](https://github.com/s0nea/rancher-desktop/assets/8761082/f9574abb-06d9-4132-a14b-c3d445e87f7d)

2. Go to the "Run/Debug Configurations" dialog (via `Run > Edit Configurations...`)
3. Add a new Node.js configuration with the following settings:
   - Name: a name for the debug configuration, e.g. `rancher desktop`
   - Node interpreter: choose your installed node interpreter, e.g. `/usr/bin/node`
   - Node parameters: `scripts/ts-wrapper.js scripts/dev.ts`
   - Working directory: choose the working directory of your project, e.g.
     `~/src/rancher-desktop`

   ![image](https://github.com/s0nea/rancher-desktop/assets/8761082/41686095-04ba-4d9e-bac1-b5587d146381)

4. Save the configuration
5. You can now set a breakpoint and click "Debug 'rancher desktop'" to start debugging

   ![image](https://github.com/s0nea/rancher-desktop/assets/8761082/87ea45f4-0a4d-4a52-9f3b-866c45e3fe2a)


## Development Builds

### Windows and macOS

Each commit triggers a GitHub Actions run that results in application bundles
(`.exe`s and `.dmg`s) being uploaded as artifacts. This can be useful if you
want to test the latest build of Rancher Desktop as built by the build system.
You can download these artifacts from the Summary page of completed `package`
actions.


### Linux

Similar to Windows and macOS, Linux builds of Rancher Desktop are made from each
commit. However on Linux, only part of the process is done by GitHub Actions.
The final part of it is done by [Open Build Service][OBS].

There are two channels of the Rancher Desktop repositories: `dev` and `stable`.
`stable` is the channel that most users use. It is the one that users are
instructed to add in the official [documentation][docs], and the one that contains
builds that are created from official releases. `dev` is the channel that we are
interested in here: it contains builds created from the latest commit made on
the `main` branch, and on any branches that match the format `release-*`. To
learn how to install the development repositories, see below.

When using the `dev` repositories, it is important to understand the format of
the versions of Rancher Desktop available from the `dev` repositories.
The versions are in the format:

```
<priority>.<branch>.<commit_time>.<commit>
```

where:

`priority` is a meaningless number that exists to give versions built from the `main`
branch priority over versions built from the `release-*` branches when updating.

`branch` is the branch name; dashes are removed due to constraints imposed by
package formats.

`commit_time` is the UNIX timestamp of the commit used to make the build.

`commit` is the shortened hash of the commit used to make the build.

[docs]: https://docs.rancherdesktop.io
[OBS]: https://build.opensuse.org/


#### `.deb` Development Repository

You can add the repo with the following steps:

```
curl -s https://download.opensuse.org/repositories/isv:/Rancher:/dev/deb/Release.key | gpg --dearmor | sudo dd status=none of=/usr/share/keyrings/isv-rancher-dev-archive-keyring.gpg
echo 'deb [signed-by=/usr/share/keyrings/isv-rancher-dev-archive-keyring.gpg] https://download.opensuse.org/repositories/isv:/Rancher:/dev/deb/ ./' | sudo dd status=none of=/etc/apt/sources.list.d/isv-rancher-dev.list
sudo apt update
```

You can see available versions with:

```
apt list -a rancher-desktop
```

Once you find the version you want to install you can install it with:

```
sudo apt install rancher-desktop=<version>
```

This works even if you already have a version of Rancher Desktop installed.


#### `.rpm` Development Repository

You can add the repo with:

```
sudo zypper addrepo https://download.opensuse.org/repositories/isv:/Rancher:/dev/rpm/isv:Rancher:dev.repo
sudo zypper refresh
```

You can see available versions with:

```
zypper search -s rancher-desktop
```

Finally, install the version you want with:

```
zypper install --oldpackage rancher-desktop=<version>
```

This works even if you already have a version of Rancher Desktop installed.


#### Development AppImages

There are no repositories for AppImages, but you can access the [latest development AppImage builds].

[latest development AppImage builds]: https://download.opensuse.org/repositories/isv:/Rancher:/dev/AppImage/

## Contributing

Please see [the document about contributing](CONTRIBUTING.md).


## Further Reading

Please see the [docs](docs/development/) directory for further developer documentation.
