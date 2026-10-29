package settings

import (
	"github.com/voluminor/dji-rc-joystick/mod/rc"
	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"
)

// // // // // // // // // //

// ControlMapping converts generated mapping settings into a runtime
// rc.MappingObj. Call rc.MappingObj.Validate before use; action strings are
// validated there against mod/vjoy's action dictionary.
func ControlMapping(mapping targetsettings.MappingObj) rc.MappingObj {
	a := mapping.Axes
	b := mapping.Buttons
	return rc.MappingObj{
		Backend: mapping.Backend,
		Axes: map[rc.AxisID]string{
			rc.AxisLeftH:      a.LeftStickH.Action,
			rc.AxisLeftV:      a.LeftStickV.Action,
			rc.AxisRightH:     a.RightStickH.Action,
			rc.AxisRightV:     a.RightStickV.Action,
			rc.AxisLeftWheel:  a.LeftWheel.Action,
			rc.AxisRightWheel: a.RightWheel.Action,
		},
		Invert: map[rc.AxisID]bool{
			rc.AxisLeftH:      a.LeftStickH.Invert,
			rc.AxisLeftV:      a.LeftStickV.Invert,
			rc.AxisRightH:     a.RightStickH.Invert,
			rc.AxisRightV:     a.RightStickV.Invert,
			rc.AxisLeftWheel:  a.LeftWheel.Invert,
			rc.AxisRightWheel: a.RightWheel.Invert,
		},
		Mode: a.Mode,
		Buttons: map[rc.ButtonID]string{
			rc.ButtonRTH:        b.Rth,
			rc.ButtonRecord:     b.Record,
			rc.ButtonCameraHalf: b.CameraHalf,
			rc.ButtonCameraFull: b.CameraFull,
			rc.ButtonC1:         b.C1,
			rc.ButtonC2:         b.C2,
		},
	}
}

// //

// ControlMapping returns the current settings as a runtime control mapping.
func (obj *Obj) ControlMapping() rc.MappingObj {
	return ControlMapping(obj.Snapshot().Mapping)
}
