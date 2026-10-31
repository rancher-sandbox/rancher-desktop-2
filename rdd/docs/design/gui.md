# Rancher Desktop 2.0 GUI

## Startup sequence

The GUI will avoid interacting with the host machine, outside of calling `rdd`
to get its configuration, including both relevant paths and how to connect to
the control plane.  It may also need to interact with RDX extensions.  The GUI
also starts `rdd steve`, which serves the Cluster Dashboard.  Having the control
plane start steve instead would need a process supervisor and a status field for
steve's port.  It would not help a GUI on another host either, because steve
binds 127.0.0.1, so that GUI would still need a proxy to reach it.

1.  Run `rdd service paths` to determine paths (where to store the logs,
    GUI-specific data, etc.).

2.  Run `rdd ctl get app` to determine whether first-run is needed.
    This will make sure the control plane is running and accepting requests before returning.

3.  Run `rdd service config`.
    This is used to configure the GUI to be able to communicate to the control plane.

4.  Create and setup a watch on the `app`.
    This will also make sure that the control plane will not be stopped due to the idle-timeout.

## Stopped App Instance

The GUI can remain running even when the app is in stopped state, so long as the control plane stays running.
For example this will be possible purely from the GUI:

1. Stop the app
2. Create a snapshot
3. Change settings
4. Restart the app

This saves one VM restart cycle because there is not a forced start after taking the snapshot.

There will be settings to control control plane lifetime independent of the GUI:
- Start the Rancher Desktop service on login.
  - This implies that the Rancher Desktop service will persist when the GUI is closed.
- Start the container engine when Rancher Desktop service starts.
- Start the Rancher Desktop GUI on login.

## Stopped control plane

The GUI assumes that the control plane does *not* restart for the duration of its lifetime.
If the control plane needs to restart for any reason, the app must quit.
