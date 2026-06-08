# Rancher Desktop 2.0 GUI

## Startup sequence

The GUI will only run an external process once to get the kube config for the rdd control plane[^RDX].

[^RDX]: The only exception is running external processes for RDX extensions.

1.  Run `rdd service config`.
    This will create the rdd instance (with default settings) if it doesn't exist yet.
    It will also make sure the control plane is running and accepting requests before returning.

2.  Setup a watch on `diagnostics` in the `rdd-system` namespace.
    This will also make sure that the control plane will not be stopped due to the idle-timeout.

3.  Fetch the app-namespace value from the config map in `rdd-system`.

4.  Setup a watch on the `app` in the app-namespace.

5.  If there is no app instance, then display the first-run dialog.
    Create the new instance with the specified settings (in "Running" state).
    Then setup the watch on the app again; this time it must succeed.

## Stopped App Instance

The GUI can remain running even when the app is in stopped state, so we need a UI mechanism to start/stop the app instance. For example this will be possible purely from the GUI:

1. Stop the app
2. Create a snapshot
3. Change settings
4. Restart the app

This saves one VM restart cycle because there is not a forced start after taking the snapshot.

There will be 3 auto-start app settings (and one auto-stop one):

1. Start Rancher Desktop service on login
2. Start Rancher Desktop GUI on login
3. Start Rancher Desktop service when the GUI is launched (redundant when #1 is set)
4. Stop Rancher Desktop service when the GUI is closed
