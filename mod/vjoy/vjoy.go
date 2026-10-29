package vjoy

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// // // // // // // // // //

// ControlKind selects how a control reaches the operating system.
type ControlKind uint8

const (
	// KindButton emits a digital joystick or gamepad button.
	KindButton ControlKind = iota
	// KindAxis emits an absolute axis value.
	KindAxis
	// KindRelative emits a relative pointer delta such as a wheel tick.
	KindRelative
)

// //

// Usage names a semantic axis or wheel. KindButton reuses it as a plain
// 1-based gamepad button number (0 auto-assigns the next free one).
type Usage uint16

const (
	// UsageX selects the X axis.
	UsageX Usage = iota
	// UsageY selects the Y axis.
	UsageY
	// UsageZ selects the Z axis.
	UsageZ
	// UsageRX selects the rotational X axis.
	UsageRX
	// UsageRY selects the rotational Y axis.
	UsageRY
	// UsageRZ selects the rotational Z axis.
	UsageRZ
	// UsageSlider0 selects the first slider axis.
	UsageSlider0
	// UsageSlider1 selects the second slider axis.
	UsageSlider1
	// UsageWheel selects the vertical mouse wheel.
	UsageWheel
	// UsageHWheel selects the horizontal mouse wheel.
	UsageHWheel
	// UsageAuto assigns the next canonical axis to KindAxis.
	UsageAuto
)

var axisUsageOrder = []Usage{
	UsageX, UsageY, UsageZ, UsageRX, UsageRY, UsageRZ, UsageSlider0, UsageSlider1,
}

const (
	maxButtons         = 79
	maxControls        = 256
	maxDeviceNameBytes = 79
)

func assignButtons(controls []ControlObj) (map[uint16]int, int, error) {
	numbers := make(map[uint16]int)
	taken := make(map[int]uint16)
	for _, c := range controls {
		if c.Kind != KindButton || c.Usage == 0 {
			continue
		}
		n := int(c.Usage)
		if n > maxButtons {
			return nil, 0, fmt.Errorf("vjoy: control %d pins button %d, out of range 1..%d", c.Handle, n, maxButtons)
		}
		if prev, dup := taken[n]; dup {
			return nil, 0, fmt.Errorf("vjoy: controls %d and %d are pinned to the same button %d", prev, c.Handle, n)
		}
		taken[n] = c.Handle
		numbers[c.Handle] = n
	}

	next, count := 1, 0
	for _, c := range controls {
		if c.Kind != KindButton {
			continue
		}
		if c.Usage == 0 {
			for _, used := taken[next]; used; _, used = taken[next] {
				next++
			}
			if next > maxButtons {
				return nil, 0, fmt.Errorf("vjoy: control %d exceeds the %d-button limit", c.Handle, maxButtons)
			}
			taken[next] = c.Handle
			numbers[c.Handle] = next
		}
		if numbers[c.Handle] > count {
			count = numbers[c.Handle]
		}
	}
	return numbers, count, nil
}

// //

// ControlObj declares one control of a virtual device. Handle is a stable,
// caller-chosen identifier later passed to Interface.Set; it is independent of
// declaration order. Min/Max bound KindAxis values and are ignored otherwise.
type ControlObj struct {
	// Handle is the caller-defined identifier passed to Interface.Set.
	Handle uint16
	// Kind selects the OS event category.
	Kind ControlKind
	// Usage selects the axis, wheel, or pinned button number.
	Usage Usage
	// Min is the lower KindAxis bound.
	Min int32
	// Max is the upper KindAxis bound.
	Max int32
}

// SpecObj describes a virtual device to create. The package knows nothing
// about DJI: any producer can define a device through it.
type SpecObj struct {
	// Backend selects the Windows implementation: vigem (default) or vjoy.
	Backend string
	// Name is the user-visible device name on platforms that support it.
	Name string
	// VendorID is the USB vendor identifier on platforms that support it.
	VendorID uint16
	// ProductID is the USB product identifier on platforms that support it.
	ProductID uint16
	// Controls declares every addressable device control.
	Controls []ControlObj
}

// //

// Interface is an OS-level virtual input device. Implementations are not safe
// for concurrent use; the caller owns serialization.
type Interface interface {
	// Set applies a value to the control with the given handle:
	//   - KindButton: value != 0 presses, value == 0 releases.
	//   - KindAxis: value is clamped to the control's [Min, Max].
	//   - KindRelative: value is the delta emitted for this frame.
	Set(handle uint16, value int32) error
	// Sync commits everything emitted since the previous Sync.
	Sync() error
	// Close releases the operating-system device and its resources.
	Close() error
}

// //

func validate(spec SpecObj) error {
	if spec.Backend != "" && spec.Backend != "vigem" && spec.Backend != "vjoy" {
		return fmt.Errorf("vjoy: unknown backend %q", spec.Backend)
	}
	if spec.Name == "" {
		return errors.New("vjoy: device name is empty")
	}
	if len(spec.Name) > maxDeviceNameBytes {
		return fmt.Errorf("vjoy: device name exceeds %d bytes", maxDeviceNameBytes)
	}
	if !utf8.ValidString(spec.Name) || strings.IndexByte(spec.Name, 0) >= 0 {
		return errors.New("vjoy: device name must be valid UTF-8 without NUL bytes")
	}
	if len(spec.Controls) > maxControls {
		return fmt.Errorf("vjoy: control count exceeds %d", maxControls)
	}
	if _, _, err := assignButtons(spec.Controls); err != nil {
		return err
	}
	seen := make(map[uint16]struct{}, len(spec.Controls))
	seenAxis := make(map[Usage]struct{})
	autoAxis := 0
	for _, c := range spec.Controls {
		if _, dup := seen[c.Handle]; dup {
			return fmt.Errorf("vjoy: duplicate control handle %d", c.Handle)
		}
		seen[c.Handle] = struct{}{}
		switch c.Kind {
		case KindButton:
			continue
		case KindRelative:
			if c.Usage != UsageWheel && c.Usage != UsageHWheel {
				return fmt.Errorf("vjoy: control %d usage %d is not a wheel", c.Handle, c.Usage)
			}
			continue
		case KindAxis:
			if c.Min >= c.Max {
				return fmt.Errorf("vjoy: control %d has an empty axis range [%d, %d]", c.Handle, c.Min, c.Max)
			}
			usage := c.Usage
			if usage == UsageAuto {
				if autoAxis >= len(axisUsageOrder) {
					return fmt.Errorf("vjoy: control %d has no free auto axis", c.Handle)
				}
				usage = axisUsageOrder[autoAxis]
				autoAxis++
			}
			if usage > UsageSlider1 {
				return fmt.Errorf("vjoy: control %d usage %d is not an axis", c.Handle, c.Usage)
			}
			if _, dup := seenAxis[usage]; dup {
				return fmt.Errorf("vjoy: control %d collides with another control on axis usage %d", c.Handle, usage)
			}
			seenAxis[usage] = struct{}{}
		default:
			return fmt.Errorf("vjoy: control %d has unknown kind %d", c.Handle, c.Kind)
		}
	}
	return nil
}

// //

// New validates spec and creates the platform virtual device.
func New(spec SpecObj) (Interface, error) {
	if err := validate(spec); err != nil {
		return nil, err
	}
	return newDevice(spec)
}
