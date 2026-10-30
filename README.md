# drm-replug

Replug a display in software when it comes back from power-on with its best
modes missing, instead of pulling the cable out and in again.

It only uses the kernel's DRM interfaces, so it works with any driver and
compositor. It was written for an HDMI 2.1 monitor on an NVIDIA card under
Hyprland, where the problem below shows up.

## The problem

Turn the monitor off and on again, and sometimes it comes back at a lower refresh
rate, with the high ones missing from the mode list. Only unplugging and replugging the cable fixes it.

What happens:

1. While the monitor boots, it raises and drops HDMI hotplug a few times
   within a few seconds.
2. The driver probes the monitor during that window, before it is ready, and
   builds a mode list without anything that needs HDMI 2.1 (FRL) bandwidth:
   no high refresh rates at high resolutions.
3. The compositor reads the mode list only when a display connects.
   [aquamarine](https://github.com/hyprwm/aquamarine) (Hyprland's backend)
   rescans connectors on every hotplug event but keeps the modes of a display
   that stayed connected. The short list sticks until the next real
   disconnect.

Disabling and re-enabling the output in the compositor doesn't help: the
modeset reuses the same short list, and the configured mode is rejected
(`atomic drm request: failed to commit: Invalid argument`).

## The fix

Every DRM connector has a `status` file in sysfs. Writing `off` forces the
connector disconnected; writing `detect` hands it back to the driver, which
probes the monitor again. With the monitor already up, that probe gets the
full list. Both writes fire a hotplug event, so the compositor sees an
ordinary unplug and replug: the screen goes dark for a few seconds and comes
back at full rate. The monitor's power is never touched.

```sh
echo off    | sudo tee /sys/class/drm/card1-HDMI-A-1/status
sleep 2
echo detect | sudo tee /sys/class/drm/card1-HDMI-A-1/status
```

`drm-replug watch` does that on its own: after each burst of hotplug events
settles (10 s), it checks whether the connector offers the mode you expect and
replugs it if not, at most three times per connection.

## Alternative: keep the connector forced on

If the monitor drops hotplug whenever it switches to another input (a KVM
setup), forcing the connector on may be the better fix: the compositor never
sees the display leave, and the mode list built at boot stays. A udev rule
does it at boot:

```
ACTION=="add", SUBSYSTEM=="drm", ENV{DEVTYPE}=="drm_connector", KERNEL=="card*-HDMI-A-1", ATTR{status}="on"
```

`echo detect` to the `status` file undoes it. The trade-off: the display is
never probed again until reboot, so a different monitor on that port keeps the
old modes.

## Install

```sh
sh install.sh                     # builds with Go, installs with sudo
sudoedit /etc/drm-replug.conf     # CONNECTOR and MODE; examples/ has one
sudo systemctl enable --now drm-replug
journalctl -u drm-replug -f
```

Needs Go to build; the binary has no dependencies. It listens to kernel
uevents itself (netlink), so it doesn't need udev. Find the connector name
with `ls /sys/class/drm`, or `hyprctl monitors`.

## Commands

```
drm-replug modes CONNECTOR            the modes the kernel offers (no root)
drm-replug check CONNECTOR MODE       exit 0 if MODE (e.g. 3840x2160@144) is offered
drm-replug replug CONNECTOR           disconnect and reconnect it in the driver (root)
drm-replug watch CONNECTOR MODE       after each hotplug, replug while MODE is missing (root)
```

`modes` lists what the kernel has right now, with refresh rates (the sysfs
`modes` file shows sizes only). It never makes the kernel probe again: only
the compositor, as DRM master, can.

## Tested on

- NVIDIA open kernel module 615.78.08, Linux 7.2
- Hyprland 0.56 (aquamarine 0.15)
- an HDMI 2.1 monitor ([`examples/hdmi-2.1-1440p-280hz.conf`](examples/hdmi-2.1-1440p-280hz.conf))

## License

MIT
