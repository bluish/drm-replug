# AGENTS.md

`drm-replug` is one Go file, stdlib only. It reads a DRM connector's mode
list with the `DRM_IOCTL_MODE_GETCONNECTOR` ioctl, listens to kernel uevents
over netlink, and replugs a connector by writing `off` then `detect` to its
sysfs `status` file.

## Rules

- Never make the kernel probe through the ioctl: only the DRM master (the
  compositor) can, and a forced probe from here would race with it. Reads
  with `count_modes` only.
- Keep the replug cap per connection (`maxReplugs`): a cable or port that
  really can't do the mode must not blank the screen in a loop.
- The settle delay waits out the hotplug bursts a monitor makes while it
  boots; shortening it brings back the race this tool exists for.
- Stdlib only.

## Testing

- `go vet ./... && go build` and `./drm-replug modes <connector>` (no root)
  compare with `hyprctl monitors all`.
- `sudo ./drm-replug replug <connector>` blanks the screen for a few seconds
  and must bring it back with the full list (check the compositor log for a
  disconnect and a connect).
- The real case (monitor powered on, mode list short) can only be checked by
  turning the monitor off and on; `journalctl -u drm-replug` shows what the
  watcher did.

## Commits

`type(scope): description` in English; body with why, then lowercase
bullets of what changed, then a `Validation:` line with what was actually run.
