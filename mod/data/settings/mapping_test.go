package settings

import (
	"testing"

	"github.com/voluminor/dji-rc-joystick/mod/rc"
	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"
)

// // // // // // // // // //

func TestDefaultConfigMappingValid(t *testing.T) {
	config := targetsettings.New()
	m := ControlMapping(config.Mapping)
	if err := m.Validate(); err != nil {
		t.Fatalf("generated default mapping invalid: %v", err)
	}

	for axis, action := range m.Axes {
		if action == "" {
			t.Fatalf("axis %s: empty action in defaults", axis)
		}
	}
	if m.Mode == "" {
		t.Fatal("mode: empty action in defaults")
	}
	for button, action := range m.Buttons {
		if action == "" {
			t.Fatalf("button %s: empty action in defaults", button)
		}
	}
}

func TestDefaultFlightModeDoesNotHoldDPad(t *testing.T) {
	mapping := ControlMapping(targetsettings.New().Mapping)
	if mapping.Mode != "unmapped" {
		t.Fatalf("flight mode must be opt-in; got %q", mapping.Mode)
	}
	for _, control := range mapping.DeviceSpec(rc.DefaultProfile()).Controls {
		if control.Handle == 100 {
			t.Fatal("default device includes a flight-mode output that can hold D-pad")
		}
	}
}

func TestConfigMappingCarriesInvert(t *testing.T) {
	cfg := targetsettings.New()
	m := ControlMapping(cfg.Mapping)
	want := map[rc.AxisID]bool{
		rc.AxisLeftH:      cfg.Mapping.Axes.LeftStickH.Invert,
		rc.AxisLeftV:      cfg.Mapping.Axes.LeftStickV.Invert,
		rc.AxisRightH:     cfg.Mapping.Axes.RightStickH.Invert,
		rc.AxisRightV:     cfg.Mapping.Axes.RightStickV.Invert,
		rc.AxisLeftWheel:  cfg.Mapping.Axes.LeftWheel.Invert,
		rc.AxisRightWheel: cfg.Mapping.Axes.RightWheel.Invert,
	}
	for axis, expected := range want {
		if m.Invert[axis] != expected {
			t.Fatalf("%s invert = %t, want %t", axis, m.Invert[axis], expected)
		}
	}
}

func TestDefaultConfigMappingDirectionCompatibility(t *testing.T) {
	m := ControlMapping(targetsettings.New().Mapping)
	want := map[rc.AxisID]bool{
		rc.AxisLeftH:      false,
		rc.AxisLeftV:      true,
		rc.AxisRightH:     false,
		rc.AxisRightV:     true,
		rc.AxisLeftWheel:  false,
		rc.AxisRightWheel: false,
	}
	for axis, expected := range want {
		if m.Invert[axis] != expected {
			t.Fatalf("%s default invert = %t, want %t", axis, m.Invert[axis], expected)
		}
	}
}

func TestConfigMappingRejectsBadAction(t *testing.T) {
	cfg := targetsettings.New()
	cfg.Mapping.Buttons.Rth = "not_an_action"
	if err := ControlMapping(cfg.Mapping).Validate(); err == nil {
		t.Fatal("invalid action in config accepted")
	}
}

func TestConfigMappingRejectsPinCollision(t *testing.T) {
	cfg := targetsettings.New()
	cfg.Mapping.Buttons.C1 = cfg.Mapping.Buttons.Rth
	if err := ControlMapping(cfg.Mapping).Validate(); err == nil {
		t.Fatal("duplicate pinned button accepted")
	}
}
