# Deployment Profiles

Deployment profiles continue to come in both "default" and "locked" variants, and both "system" and "user" profiles.

## Profile locations

Profiles are stored in an OS-dependent format and location, just like in Rancher Desktop 1.x.

Both the schema and the name / location is different in RDD profiles.

When the `apiserver` has started, the selected profile is copied into a `profile` config map inside the `rdd-system` namespace.

## Profile selection

Each instance can have their own profile, called `rancher-desktop-$RDD_INSTANCE`. The first profile from this list applies:

1. `SYSTEM_PROFILE/rancher-desktop-$RDD_INSTANCE`
2. `SYSTEM_PROFILE/rancher-desktop-2`
3. `USER_PROFILE/rancher-desktop-$RDD_INSTANCE`
4. no profile applied

The `SYSTEM_PROFILE/rancher-desktop-2` profile applies to all instances that don't have their own system profile to prevent circumvention of a system "locked" profile.

## Profile implementation

The profile has a section for each controller; at minimum one for `rdd` and one for `app`.

It doesn't make sense to have an `app` profile without an `rdd` one because then the user could just create a different VM that would not be subject to the profile.

### `rdd` profile

The `rdd` profile is implemented using RBAC. The service account used by the builtin controller manager always has full access to the control plane, but the service account returned by `rdd service config` may be limited to modifying the `app` object in the default app-namespace by the `rdd` profile.

This means that non-builtin controllers will not work (which is the intention of having a locked `rdd` profile).

### `app` profile

The `app` profile settings are enforced by the `app` admission controller.

Since starting the `app` (e.g. when the control plane starts up) requires setting `spec.status` to `Running`, it means the admission controller will automatically prevent the app from starting if the profile has changed and the current configuration has become invalid.

We probably need a mechanism to just accept all locked fields as-is, to make the app startable again (the only other option is to `reset` or `delete` the app).

