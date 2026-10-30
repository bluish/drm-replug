// drm-replug replugs a display in software when it comes back with modes missing.
package main

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const usage = `drm-replug: replug a display in software when it comes back with modes missing.

Usage:
  drm-replug modes CONNECTOR            the modes the kernel offers (no root)
  drm-replug check CONNECTOR MODE       exit 0 if MODE (e.g. 3840x2160@144) is offered
  drm-replug replug CONNECTOR           disconnect and reconnect it in the driver (root)
  drm-replug watch CONNECTOR MODE       after each hotplug, replug while MODE is missing (root)

CONNECTOR is a DRM connector name such as HDMI-A-1 (see /sys/class/drm).`

const (
	settle     = 10 * time.Second // without hotplug events before judging the modes
	replugGap  = 2 * time.Second  // the connector stays forced off this long
	maxReplugs = 3                // per connection, so a cable that really can't do MODE isn't replugged forever
)

type mode struct {
	w, h int
	hz   float64
}

func (m mode) String() string { return fmt.Sprintf("%dx%d@%.2f", m.w, m.h, m.hz) }

func main() {
	args := os.Args[1:]
	var err error
	switch {
	case len(args) == 2 && args[0] == "modes":
		var connected bool
		var modes []mode
		if connected, modes, err = kernelModes(args[1]); err == nil && !connected {
			err = fmt.Errorf("%s is disconnected", args[1])
		}
		for _, m := range modes {
			fmt.Println(m)
		}
	case len(args) == 3 && args[0] == "check":
		var want mode
		if want, err = parseMode(args[2]); err == nil {
			connected, modes, e := kernelModes(args[1])
			if e != nil {
				err = e
			} else if !connected || !offers(modes, want) {
				os.Exit(1)
			}
		}
	case len(args) == 2 && args[0] == "replug":
		err = replug(args[1])
	case len(args) == 3 && args[0] == "watch":
		var want mode
		if want, err = parseMode(args[2]); err == nil {
			err = watch(args[1], args[2], want)
		}
	default:
		fmt.Fprintln(os.Stderr, usage)
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "drm-replug:", err)
		os.Exit(1)
	}
}

func sysfsDir(connector string) (string, error) {
	dirs, _ := filepath.Glob("/sys/class/drm/card*-" + connector)
	if len(dirs) == 0 {
		return "", fmt.Errorf("no connector %s in /sys/class/drm", connector)
	}
	return dirs[0], nil
}

// struct drm_mode_get_connector and struct drm_mode_modeinfo (include/uapi/drm/drm_mode.h).
type getConnector struct {
	encodersPtr, modesPtr, propsPtr, propValuesPtr uint64
	countModes, countProps, countEncoders          uint32
	encoderID, connectorID, connectorType          uint32
	connectorTypeID, connection, mmWidth, mmHeight uint32
	subpixel, pad                                  uint32
}

type modeInfo struct {
	clock                                         uint32
	hdisplay, hsyncStart, hsyncEnd, htotal, hskew uint16
	vdisplay, vsyncStart, vsyncEnd, vtotal, vscan uint16
	vrefresh, flags, typ                          uint32
	name                                          [32]byte
}

const (
	drmModeConnected     = 1
	drmModeFlagInterlace = 0x10
)

var ioctlGetConnector = uintptr(3<<30 | unsafe.Sizeof(getConnector{})<<16 | 'd'<<8 | 0xA7)

// kernelModes returns whether the connector is connected and the modes the
// kernel already has. It never makes the kernel probe again: only the DRM
// master (the compositor) can, through this ioctl.
func kernelModes(connector string) (bool, []mode, error) {
	dir, err := sysfsDir(connector)
	if err != nil {
		return false, nil, err
	}
	raw, err := os.ReadFile(dir + "/connector_id")
	if err != nil {
		return false, nil, err
	}
	id, _ := strconv.Atoi(strings.TrimSpace(string(raw)))
	card, err := os.Open("/dev/dri/" + strings.SplitN(filepath.Base(dir), "-", 2)[0])
	if err != nil {
		return false, nil, err
	}
	defer card.Close()

	req := getConnector{connectorID: uint32(id)}
	if err := ioctl(card.Fd(), &req); err != nil {
		return false, nil, err
	}
	infos := make([]modeInfo, req.countModes)
	req = getConnector{connectorID: uint32(id), countModes: uint32(len(infos))}
	if len(infos) > 0 {
		req.modesPtr = uint64(uintptr(unsafe.Pointer(&infos[0])))
	}
	if err := ioctl(card.Fd(), &req); err != nil {
		return false, nil, err
	}
	if req.connection != drmModeConnected {
		return false, nil, nil
	}
	var modes []mode
	for _, m := range infos[:min(len(infos), int(req.countModes))] {
		if m.flags&drmModeFlagInterlace == 0 {
			hz := float64(m.clock) * 1000 / (float64(m.htotal) * float64(m.vtotal))
			modes = append(modes, mode{int(m.hdisplay), int(m.vdisplay), hz})
		}
	}
	return true, modes, nil
}

func ioctl(fd uintptr, req *getConnector) error {
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, fd, ioctlGetConnector, uintptr(unsafe.Pointer(req))); errno != 0 {
		return errno
	}
	return nil
}

func parseMode(text string) (mode, error) {
	var m mode
	if _, err := fmt.Sscanf(text, "%dx%d@%g", &m.w, &m.h, &m.hz); err != nil {
		return m, fmt.Errorf("bad mode %q, expected WIDTHxHEIGHT@HZ such as 3840x2160@144", text)
	}
	return m, nil
}

func offers(modes []mode, want mode) bool {
	for _, m := range modes {
		if m.w == want.w && m.h == want.h && m.hz >= want.hz-1 {
			return true
		}
	}
	return false
}

// replug forces the connector off, then hands it back to the driver, which
// probes the monitor again. Both writes fire a hotplug event.
func replug(connector string) error {
	dir, err := sysfsDir(connector)
	if err != nil {
		return err
	}
	if err := os.WriteFile(dir+"/status", []byte("off"), 0); err != nil {
		return err
	}
	time.Sleep(replugGap)
	return os.WriteFile(dir+"/status", []byte("detect"), 0)
}

// drmEvents delivers a value for every kernel uevent of the drm subsystem.
func drmEvents() (<-chan struct{}, error) {
	fd, err := syscall.Socket(syscall.AF_NETLINK, syscall.SOCK_DGRAM, syscall.NETLINK_KOBJECT_UEVENT)
	if err != nil {
		return nil, err
	}
	if err := syscall.Bind(fd, &syscall.SockaddrNetlink{Family: syscall.AF_NETLINK, Groups: 1}); err != nil {
		return nil, err
	}
	events := make(chan struct{}, 16)
	go func() {
		buf := make([]byte, 64<<10)
		for {
			n, _, err := syscall.Recvfrom(fd, buf, 0)
			if err != nil {
				continue
			}
			if bytes.Contains(buf[:n], []byte("\x00SUBSYSTEM=drm\x00")) {
				select {
				case events <- struct{}{}:
				default:
				}
			}
		}
	}()
	return events, nil
}

func watch(connector, text string, want mode) error {
	events, err := drmEvents()
	if err != nil {
		return err
	}
	out := bufio.NewWriter(os.Stdout)
	logf := func(format string, a ...any) { fmt.Fprintf(out, format+"\n", a...); out.Flush() }
	timer := time.NewTimer(0) // check once at start
	replugs := 0
	for {
		select {
		case <-events:
			timer.Reset(settle)
			continue
		case <-timer.C:
		}
		connected, modes, err := kernelModes(connector)
		if err != nil {
			return err
		}
		if !connected || offers(modes, want) {
			replugs = 0
			continue
		}
		switch {
		case replugs < maxReplugs:
			replugs++
			best := 0.0
			for _, m := range modes {
				if m.w == want.w && m.h == want.h {
					best = max(best, m.hz)
				}
			}
			logf("%s came back without %s (best %.0f Hz at that size), replug %d/%d",
				connector, text, best, replugs, maxReplugs)
			if err := replug(connector); err != nil {
				return err
			}
		case replugs == maxReplugs:
			replugs++
			logf("%s still without %s after %d replugs, leaving it until it is plugged in again",
				connector, text, maxReplugs)
		}
	}
}
