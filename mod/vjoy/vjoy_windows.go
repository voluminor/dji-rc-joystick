//go:build windows && !386

package vjoy

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"unsafe"

	"golang.org/x/sys/windows"
)

// // // // // // // // // //

// This backend drives two Windows subsystems. Axes and buttons feed an
// existing vJoy driver device (the driver owns the device identity, so
// SpecObj.Name/VendorID/ProductID are ignored, and a device must be
// preconfigured with vJoyConf). Relative wheels have no vJoy equivalent, so
// they are emitted as real OS events through Win32 SendInput.
//
// vJoyInterface.dll and user32.dll exports use cdecl / the Win64 convention;
// on amd64/arm64 that matches Go's expectations, so the 386 build falls back
// to the unsupported stub (this file is tagged windows && !386).

const (
	vjdStatFree = 1 // VJD_STAT_FREE

	vjoyAxisMin = 0x1
	vjoyAxisMax = 0x8000

	vjoyMaxDevices = 16 // button cap is the shared maxButtons

	// Win32 SendInput constants.
	inputMouse = 0

	mouseEventWheel  = 0x0800 // MOUSEEVENTF_WHEEL
	mouseEventHWheel = 0x1000 // MOUSEEVENTF_HWHEEL
	wheelDelta       = 120    // WHEEL_DELTA: one notch
)

var (
	// NewLazyDLL: vJoyInterface.dll ships with the vJoy driver, not with
	// Windows, so the system-DLL loader restriction does not apply.
	vjoyDLL                = windows.NewLazyDLL(vjoyDLLPath())
	procVJoyEnabled        = vjoyDLL.NewProc("vJoyEnabled")
	procGetVJDStatus       = vjoyDLL.NewProc("GetVJDStatus")
	procGetVJDAxisExist    = vjoyDLL.NewProc("GetVJDAxisExist")
	procGetVJDButtonNumber = vjoyDLL.NewProc("GetVJDButtonNumber")
	procAcquireVJD         = vjoyDLL.NewProc("AcquireVJD")
	procRelinquishVJD      = vjoyDLL.NewProc("RelinquishVJD")
	procResetVJD           = vjoyDLL.NewProc("ResetVJD")
	procSetAxis            = vjoyDLL.NewProc("SetAxis")
	procSetBtn             = vjoyDLL.NewProc("SetBtn")

	user32        = windows.NewLazySystemDLL("user32.dll")
	procSendInput = user32.NewProc("SendInput")
)

// The driver installer does not necessarily add its SDK DLL to PATH.
func vjoyDLLPath() string {
	exe, _ := os.Executable()
	local := filepath.Join(filepath.Dir(exe), "vJoyInterface.dll")
	candidates := []string{local}
	arch := "x64"
	if runtime.GOARCH == "arm64" {
		arch = "arm64"
	}
	for _, root := range []string{os.Getenv("ProgramW6432"), os.Getenv("ProgramFiles")} {
		if root != "" {
			candidates = append(candidates, filepath.Join(root, "vJoy", arch, "vJoyInterface.dll"))
		}
	}
	for _, path := range candidates {
		if info, err := os.Stat(path); err == nil && !info.IsDir() {
			return path
		}
	}
	return local
}

// //

// mouseInputObj mirrors INPUT{type; MOUSEINPUT} on 64-bit Windows (40 bytes).
type mouseInputObj struct {
	kind        uint32
	_           uint32
	dx          int32
	dy          int32
	mouseData   uint32
	dwFlags     uint32
	time        uint32
	dwExtraInfo uint64
}

type winBindObj struct {
	kind    ControlKind
	usage   uint32  // vJoy HID usage (axis)
	button  uintptr // vJoy button number, 1-based
	min     int32
	max     int32
	relFlag uint32 // MOUSEEVENTF_WHEEL / MOUSEEVENTF_HWHEEL
}

type deviceObj struct {
	device   uintptr
	acquired bool
	binds    map[uint16]winBindObj
}

// //

func hidUsage(u Usage) (uint32, bool) {
	switch u {
	case UsageX:
		return 0x30, true // HID_USAGE_X
	case UsageY:
		return 0x31, true // HID_USAGE_Y
	case UsageZ:
		return 0x32, true // HID_USAGE_Z
	case UsageRX:
		return 0x33, true // HID_USAGE_RX
	case UsageRY:
		return 0x34, true // HID_USAGE_RY
	case UsageRZ:
		return 0x35, true // HID_USAGE_RZ
	case UsageSlider0:
		return 0x36, true // HID_USAGE_SL0
	case UsageSlider1:
		return 0x37, true // HID_USAGE_SL1
	}
	return 0, false
}

func wheelFlag(u Usage) (uint32, bool) {
	switch u {
	case UsageWheel:
		return mouseEventWheel, true
	case UsageHWheel:
		return mouseEventHWheel, true
	}
	return 0, false
}

func sendWheel(flag uint32, value int32) error {
	in := mouseInputObj{
		kind:      inputMouse,
		mouseData: uint32(value * wheelDelta),
		dwFlags:   flag,
	}
	if n, _, err := procSendInput.Call(1, uintptr(unsafe.Pointer(&in)), unsafe.Sizeof(in)); n == 0 {
		return fmt.Errorf("vjoy: SendInput wheel failed: %w", err)
	}
	return nil
}

func deviceFits(device uintptr, axisUsages []uint32, buttons int) bool {
	for _, u := range axisUsages {
		if exists, _, _ := procGetVJDAxisExist.Call(device, uintptr(u)); exists == 0 {
			return false
		}
	}
	n, _, _ := procGetVJDButtonNumber.Call(device)
	return int(int32(n)) >= buttons
}

func acquireDevice(dev *deviceObj, axisUsages []uint32, buttons int) error {
	if err := vjoyDLL.Load(); err != nil {
		return fmt.Errorf("vjoy: vJoy driver is not installed (vJoyInterface.dll not found): %w", err)
	}
	if enabled, _, _ := procVJoyEnabled.Call(); enabled == 0 {
		return errors.New("vjoy: vJoy driver is installed but not enabled")
	}
	for device := uintptr(1); device <= vjoyMaxDevices; device++ {
		if status, _, _ := procGetVJDStatus.Call(device); status != vjdStatFree {
			continue
		}
		if !deviceFits(device, axisUsages, buttons) {
			continue
		}
		if ok, _, _ := procAcquireVJD.Call(device); ok == 0 {
			continue
		}
		_, _, _ = procResetVJD.Call(device)
		dev.device = device
		dev.acquired = true
		return nil
	}
	return fmt.Errorf(
		"vjoy: no free vJoy device with %d axes and %d buttons; configure one with vJoyConf",
		len(axisUsages), buttons)
}

// //

func newVJoyDevice(spec SpecObj) (Interface, error) {
	dev := &deviceObj{
		binds: make(map[uint16]winBindObj, len(spec.Controls)),
	}

	buttonNums, buttonCount, err := assignButtons(spec.Controls)
	if err != nil {
		return nil, err
	}

	var axisUsages []uint32
	autoAxis := 0
	for _, c := range spec.Controls {
		switch c.Kind {
		case KindAxis:
			if len(axisUsages) >= 8 {
				return nil, fmt.Errorf("vjoy: control %d exceeds the vJoy 8-axis limit", c.Handle)
			}
			usage := c.Usage
			if usage == UsageAuto {
				if autoAxis >= len(axisUsageOrder) {
					return nil, fmt.Errorf("vjoy: control %d has no free auto axis", c.Handle)
				}
				usage = axisUsageOrder[autoAxis]
				autoAxis++
			}
			hid, ok := hidUsage(usage)
			if !ok {
				return nil, fmt.Errorf("vjoy: control %d usage %d is not an axis", c.Handle, c.Usage)
			}
			axisUsages = append(axisUsages, hid)
			dev.binds[c.Handle] = winBindObj{kind: KindAxis, usage: hid, min: c.Min, max: c.Max}

		case KindButton:
			dev.binds[c.Handle] = winBindObj{kind: KindButton, button: uintptr(buttonNums[c.Handle])}

		case KindRelative:
			flag, ok := wheelFlag(c.Usage)
			if !ok {
				return nil, fmt.Errorf("vjoy: control %d usage %d is not a wheel", c.Handle, c.Usage)
			}
			dev.binds[c.Handle] = winBindObj{kind: KindRelative, relFlag: flag}

		default:
			return nil, fmt.Errorf("vjoy: control %d has unknown kind %d", c.Handle, c.Kind)
		}
	}

	// A vJoy device is needed only for axes or buttons. It must offer buttons
	// up to the highest assigned number, not just the count of controls.
	if len(axisUsages) > 0 || buttonCount > 0 {
		if err := acquireDevice(dev, axisUsages, buttonCount); err != nil {
			return nil, err
		}
	}
	return dev, nil
}

func (d *deviceObj) Set(handle uint16, value int32) error {
	b, ok := d.binds[handle]
	if !ok {
		return fmt.Errorf("vjoy: unknown control handle %d", handle)
	}
	switch b.kind {
	case KindAxis:
		return d.setAxis(b, value)
	case KindButton:
		pressed := uintptr(0)
		if value != 0 {
			pressed = 1
		}
		if ok, _, _ := procSetBtn.Call(pressed, d.device, b.button); ok == 0 {
			return fmt.Errorf("vjoy: set button %d failed", b.button)
		}
		return nil
	case KindRelative:
		return sendWheel(b.relFlag, value)
	default:
		return fmt.Errorf("vjoy: control handle %d has unknown kind %d", handle, b.kind)
	}
}

func (d *deviceObj) setAxis(b winBindObj, value int32) error {
	if value < b.min {
		value = b.min
	}
	if value > b.max {
		value = b.max
	}
	// Map the spec range onto the fixed vJoy axis range 0x1..0x8000.
	span := int64(b.max) - int64(b.min)
	scaled := vjoyAxisMin + (int64(value)-int64(b.min))*(vjoyAxisMax-vjoyAxisMin)/span
	if ok, _, _ := procSetAxis.Call(uintptr(scaled), d.device, uintptr(b.usage)); ok == 0 {
		return fmt.Errorf("vjoy: set axis usage 0x%x failed", b.usage)
	}
	return nil
}

// Sync is a no-op: vJoy applies axis/button changes immediately and SendInput
// delivers wheel events as they are emitted.
func (d *deviceObj) Sync() error { return nil }

func (d *deviceObj) Close() error {
	if !d.acquired {
		return nil
	}
	d.acquired = false
	_, _, _ = procRelinquishVJD.Call(d.device)
	return nil
}
