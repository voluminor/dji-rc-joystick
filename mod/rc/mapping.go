package rc

import (
	"fmt"

	"github.com/voluminor/dji-rc-joystick/mod/vjoy"
)

// // // // // // // // // //

const (
	// Stable virtual-device control handles, keyed by semantic control id so an
	// auto-detected profile swap never shifts them. Axes use 0..AxisCount-1.
	handleMode       uint16 = 100
	handleButtonBase uint16 = 200

	// wheelAxisScale converts a full ±outMax analog deflection into a per-frame
	// scroll delta when an axis is bound to a mouse wheel.
	wheelAxisScale = 8000
)

func axisHandle(a AxisID) uint16     { return uint16(a) }
func buttonHandle(b ButtonID) uint16 { return handleButtonBase + uint16(b) }

func cloneMapping(mapping MappingObj) MappingObj {
	clone := MappingObj{
		Backend: mapping.Backend,
		Axes:    make(map[AxisID]string, len(mapping.Axes)),
		Invert:  make(map[AxisID]bool, len(mapping.Invert)),
		Mode:    mapping.Mode,
		Buttons: make(map[ButtonID]string, len(mapping.Buttons)),
	}
	for id, actionName := range mapping.Axes {
		clone.Axes[id] = actionName
	}
	for id, inverted := range mapping.Invert {
		clone.Invert[id] = inverted
	}
	for id, actionName := range mapping.Buttons {
		clone.Buttons[id] = actionName
	}
	return clone
}

func action(name string) (vjoy.ActionObj, bool, error) {
	if name == "" || name == vjoy.ActionUnmapped {
		return vjoy.ActionObj{}, false, nil
	}
	a, ok := vjoy.Action(name)
	if !ok {
		return vjoy.ActionObj{}, false, fmt.Errorf("unknown action %q", name)
	}
	return a, true, nil
}

// //

// MappingObj binds each DJI control to a named output action from mod/vjoy.
// An empty or ActionUnmapped value leaves the control inert. Keys are the
// semantic control ids; values are validated against vjoy.Action.
type MappingObj struct {
	// Backend selects the Windows virtual gamepad implementation.
	Backend string
	// Axes maps semantic axes to continuous vjoy action names.
	Axes map[AxisID]string
	// Invert reverses selected axes before output conversion.
	Invert map[AxisID]bool
	// Mode maps the flight-mode switch to a continuous action.
	Mode string
	// Buttons maps semantic buttons to discrete vjoy action names.
	Buttons map[ButtonID]string
}

// DefaultMapping reproduces the classic layout: sticks and wheels on the six
// gamepad axes, the mode switch on a slider, every button a gamepad button.
func DefaultMapping() MappingObj {
	return MappingObj{
		Axes: map[AxisID]string{
			AxisLeftH:      vjoy.ActionAxisX,
			AxisLeftV:      vjoy.ActionAxisY,
			AxisRightH:     vjoy.ActionAxisRX,
			AxisRightV:     vjoy.ActionAxisRY,
			AxisLeftWheel:  vjoy.ActionAxisZ,
			AxisRightWheel: vjoy.ActionAxisRZ,
		},
		Mode: vjoy.ActionSlider0,
		Buttons: map[ButtonID]string{
			ButtonRTH:        vjoy.ActionButton,
			ButtonRecord:     vjoy.ActionButton,
			ButtonCameraHalf: vjoy.ActionButton,
			ButtonCameraFull: vjoy.ActionButton,
			ButtonC1:         vjoy.ActionButton,
			ButtonC2:         vjoy.ActionButton,
		},
	}
}

// Validate reports whether every bound action exists and suits its source:
// analog sources (axes, mode) need continuous actions, buttons discrete ones.
// It also rejects collisions early, with the DJI controls named: two analog
// sources driving one virtual axis, or two buttons pinned to one number.
func (m MappingObj) Validate() error {
	if m.Backend != "" && m.Backend != "vigem" && m.Backend != "vjoy" {
		return fmt.Errorf("unknown gamepad backend %q (use vigem or vjoy)", m.Backend)
	}
	if len(m.Axes) > int(AxisCount) {
		return fmt.Errorf("mapping has more than %d axes", AxisCount)
	}
	for axis := range m.Axes {
		if axis < 0 || axis >= AxisCount {
			return fmt.Errorf("mapping contains unknown axis %d", axis)
		}
	}
	if len(m.Invert) > int(AxisCount) {
		return fmt.Errorf("mapping has more than %d axis inversion entries", AxisCount)
	}
	for axis := range m.Invert {
		if axis < 0 || axis >= AxisCount {
			return fmt.Errorf("mapping contains unknown inverted axis %d", axis)
		}
	}
	if len(m.Buttons) > int(ButtonCount) {
		return fmt.Errorf("mapping has more than %d buttons", ButtonCount)
	}
	for button := range m.Buttons {
		if button < 0 || button >= ButtonCount {
			return fmt.Errorf("mapping contains unknown button %d", button)
		}
	}

	type targetObj struct {
		kind  vjoy.ControlKind
		usage vjoy.Usage
	}
	analogSeen := map[targetObj]string{}
	pinSeen := map[vjoy.Usage]string{}

	check := func(name string, wantContinuous bool, who string) error {
		a, active, err := action(name)
		if err != nil {
			return fmt.Errorf("mapping %s: %w", who, err)
		}
		if !active {
			return nil
		}
		if a.Continuous != wantContinuous {
			if wantContinuous {
				return fmt.Errorf("mapping %s: %q is a button action, this control is analog (use axis_*, wheel, hwheel)", who, name)
			}
			return fmt.Errorf("mapping %s: %q is an analog action, this control is a button (use button, buttonN, wheel ticks)", who, name)
		}
		if a.Continuous {
			t := targetObj{kind: a.Kind, usage: a.Usage}
			if prev, dup := analogSeen[t]; dup {
				return fmt.Errorf("mapping %s: action %q is already taken by %s", who, name, prev)
			}
			analogSeen[t] = who
		}
		if a.Kind == vjoy.KindButton && a.Usage > 0 {
			if prev, dup := pinSeen[a.Usage]; dup {
				return fmt.Errorf("mapping %s: button %d is already pinned by %s", who, a.Usage, prev)
			}
			pinSeen[a.Usage] = who
		}
		return nil
	}

	for _, axisMap := range universalAxes {
		if err := check(m.Axes[axisMap.Axis], true, axisMap.Axis.String()); err != nil {
			return err
		}
	}
	if err := check(m.Mode, true, "mode"); err != nil {
		return err
	}
	for _, buttonMap := range universalButtons {
		if err := check(m.Buttons[buttonMap.Button], false, buttonMap.Button.String()); err != nil {
			return err
		}
	}
	return nil
}

// DeviceSpec builds the virtual-device layout for this mapping under a model's
// identity: one control per bound (non-unmapped) DJI control.
func (m MappingObj) DeviceSpec(p ProfileObj) vjoy.SpecObj {
	var controls []vjoy.ControlObj
	add := func(handle uint16, a vjoy.ActionObj, min, max int32) {
		controls = append(controls, vjoy.ControlObj{Handle: handle, Kind: a.Kind, Usage: a.Usage, Min: min, Max: max})
	}

	for _, axisMap := range universalAxes {
		if a, active, _ := action(m.Axes[axisMap.Axis]); active {
			add(axisHandle(axisMap.Axis), a, -outMax, outMax)
		}
	}
	if a, active, _ := action(m.Mode); active {
		add(handleMode, a, 0, int32(ModeCinema))
	}
	for _, buttonMap := range universalButtons {
		if a, active, _ := action(m.Buttons[buttonMap.Button]); active {
			add(buttonHandle(buttonMap.Button), a, 0, 0)
		}
	}
	return vjoy.SpecObj{Backend: m.Backend, Name: p.Name, VendorID: p.VendorID, ProductID: p.ProductID, Controls: controls}
}
