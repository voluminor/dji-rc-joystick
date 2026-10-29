//go:build windows && !386

package vjoy

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// XUSB_REPORT is passed by reference under the Windows x64 ABI (12 bytes).
type xboxReport struct {
	Buttons        uint16
	LT, RT         uint8
	LX, LY, RX, RY int16
}

type xboxDevice struct {
	dll            *windows.DLL
	client, target uintptr
	controls       map[uint16]ControlObj
	buttons        map[uint16]int
	report         xboxReport
}

var xboxButtons = []uint16{0x1000, 0x2000, 0x4000, 0x8000, 0x100, 0x200, 0x20, 0x10, 0x40, 0x80, 1, 2, 4, 8, 0x400}

func newDevice(spec SpecObj) (Interface, error) {
	backend := spec.Backend
	if backend == "" {
		backend = strings.ToLower(os.Getenv("DJI_RC_GAMEPAD"))
	}
	switch backend {
	case "vjoy":
		return newVJoyDevice(spec)
	case "", "xbox", "vigem":
		return newXboxDevice(spec)
	default:
		return nil, fmt.Errorf("unknown Windows gamepad backend %q (use vigem or vjoy)", backend)
	}
}

func (d *xboxDevice) call(name string, args ...uintptr) uintptr {
	// All exports are resolved before any resources are allocated.
	p, _ := d.dll.FindProc(name)
	r, _, _ := p.Call(args...)
	return r
}

func newXboxDevice(spec SpecObj) (Interface, error) {
	if runtime.GOARCH != "amd64" {
		return nil, fmt.Errorf("Xbox emulation currently requires the Windows x64 build")
	}
	d := &xboxDevice{controls: make(map[uint16]ControlObj)}
	var err error
	d.buttons, _, err = assignButtons(spec.Controls)
	if err != nil {
		return nil, err
	}
	for _, c := range spec.Controls {
		if c.Kind == KindAxis && (c.Usage == UsageAuto || c.Usage == UsageSlider1) {
			return nil, fmt.Errorf("Xbox: use X/Y for left stick, RX/RY for right stick, Z/RZ for triggers, slider0 for mode D-pad; unsupported control %d", c.Handle)
		}
		if c.Kind == KindButton && d.buttons[c.Handle] > len(xboxButtons) {
			return nil, fmt.Errorf("Xbox supports buttons 1..%d", len(xboxButtons))
		}
		d.controls[c.Handle] = c
	}
	// The mode switch and explicit buttons cannot independently own D-pad bits.
	for _, c := range spec.Controls {
		if c.Kind == KindAxis && c.Usage == UsageSlider0 {
			for _, n := range d.buttons {
				if n == 11 || n == 12 {
					return nil, fmt.Errorf("Xbox: mode D-pad conflicts with button %d; unmap one of them", n)
				}
			}
		}
	}
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	// Resolve only beside the executable; never search the working directory/PATH.
	d.dll, err = windows.LoadDLL(filepath.Join(filepath.Dir(exe), "ViGEmClient.dll"))
	if err != nil {
		return nil, fmt.Errorf("Xbox: ViGEmClient.dll must be beside the application (use the complete debug package): %w", err)
	}
	for _, name := range []string{"vigem_alloc", "vigem_free", "vigem_connect", "vigem_disconnect", "vigem_target_x360_alloc", "vigem_target_free", "vigem_target_add", "vigem_target_remove", "vigem_target_x360_update"} {
		if _, err = d.dll.FindProc(name); err != nil {
			d.dll.Release()
			return nil, fmt.Errorf("Xbox: incompatible ViGEmClient: %w", err)
		}
	}
	d.client = d.call("vigem_alloc")
	if d.client == 0 {
		d.Close()
		return nil, fmt.Errorf("Xbox: client allocation failed")
	}
	if code := d.call("vigem_connect", d.client); code != 0x20000000 {
		d.Close()
		return nil, fmt.Errorf("Xbox: cannot connect to ViGEmBus (0x%x); install the official ViGEmBus driver and reboot", code)
	}
	d.target = d.call("vigem_target_x360_alloc")
	if d.target == 0 {
		d.Close()
		return nil, fmt.Errorf("Xbox: target allocation failed")
	}
	if code := d.call("vigem_target_add", d.client, d.target); code != 0x20000000 {
		d.Close()
		return nil, fmt.Errorf("Xbox: attach failed (0x%x)", code)
	}
	return d, nil
}

func (d *xboxDevice) Set(handle uint16, value int32) error {
	if d.target == 0 {
		return fmt.Errorf("Xbox: device closed")
	}
	c, ok := d.controls[handle]
	if !ok {
		return fmt.Errorf("Xbox: unknown control %d", handle)
	}
	switch c.Kind {
	case KindButton:
		mask := xboxButtons[d.buttons[handle]-1]
		d.report.Buttons &^= mask
		if value != 0 {
			d.report.Buttons |= mask
		}
	case KindRelative:
		flag, _ := wheelFlag(c.Usage)
		if value != 0 {
			return sendWheel(flag, value)
		}
	case KindAxis:
		if value < c.Min {
			value = c.Min
		}
		if value > c.Max {
			value = c.Max
		}
		n := (int64(value)-int64(c.Min))*65534/(int64(c.Max)-int64(c.Min)) - 32767
		switch c.Usage {
		case UsageX:
			d.report.LX = int16(n)
		case UsageY:
			// Generic joystick Y grows down; XInput thumb Y grows up.
			d.report.LY = int16(-n)
		case UsageRX:
			d.report.RX = int16(n)
		case UsageRY:
			d.report.RY = int16(-n)
		case UsageZ:
			d.report.LT = xboxTrigger(n)
		case UsageRZ:
			d.report.RT = xboxTrigger(n)
		case UsageSlider0:
			d.report.Buttons &^= 3
			if value == c.Min {
				d.report.Buttons |= 1
			} else if value == c.Max {
				d.report.Buttons |= 2
			}
		}
	}
	return nil
}

// Centered DJI wheels release the triggers at rest; positive travel presses.
func xboxTrigger(n int64) uint8 {
	if n <= 0 {
		return 0
	}
	return uint8(n * 255 / 32767)
}

func (d *xboxDevice) Sync() error {
	if d.target == 0 {
		return fmt.Errorf("Xbox: device closed")
	}
	code := d.call("vigem_target_x360_update", d.client, d.target, uintptr(unsafe.Pointer(&d.report)))
	runtime.KeepAlive(d)
	if code != 0x20000000 {
		return fmt.Errorf("Xbox: update failed (0x%x)", code)
	}
	return nil
}

func (d *xboxDevice) Close() error {
	if d.dll == nil {
		return nil
	}
	if d.target != 0 {
		d.call("vigem_target_remove", d.client, d.target)
		d.call("vigem_target_free", d.target)
		d.target = 0
	}
	if d.client != 0 {
		d.call("vigem_disconnect", d.client)
		d.call("vigem_free", d.client)
		d.client = 0
	}
	err := d.dll.Release()
	d.dll = nil
	return err
}
