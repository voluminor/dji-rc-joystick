//go:build windows && !386

package vjoy

import (
	"testing"
	"unsafe"
)

func TestXboxReportLayout(t *testing.T) {
	r := xboxReport{}
	if unsafe.Sizeof(r) != 12 || unsafe.Offsetof(r.LX) != 4 || unsafe.Offsetof(r.RY) != 10 {
		t.Fatal("XUSB_REPORT does not match the native ABI")
	}
}

func TestXboxStickDirections(t *testing.T) {
	d := &xboxDevice{target: 1, controls: map[uint16]ControlObj{}}
	for handle, usage := range []Usage{UsageX, UsageY, UsageRX, UsageRY} {
		d.controls[uint16(handle)] = ControlObj{Kind: KindAxis, Usage: usage, Min: -32767, Max: 32767}
	}
	for _, vertical := range []int32{-32767, 0, 32767} {
		for handle, value := range []int32{32767, vertical, 32767, vertical} {
			if err := d.Set(uint16(handle), value); err != nil {
				t.Fatal(err)
			}
		}
		if d.report.LX != 32767 || d.report.RX != 32767 || d.report.LY != int16(-vertical) || d.report.RY != int16(-vertical) {
			t.Fatalf("generic Y=%d: incorrect XInput directions: %+v", vertical, d.report)
		}
	}
}

func TestXboxMappingAndNeutral(t *testing.T) {
	d := &xboxDevice{target: 1, controls: map[uint16]ControlObj{
		1: {Kind: KindAxis, Usage: UsageX, Min: -32767, Max: 32767},
		2: {Kind: KindAxis, Usage: UsageZ, Min: -32767, Max: 32767},
		3: {Kind: KindButton},
	}, buttons: map[uint16]int{3: 1}}
	for _, handle := range []uint16{1, 2, 3} {
		if err := d.Set(handle, 32767); err != nil {
			t.Fatal(err)
		}
	}
	if d.report.LX != 32767 || d.report.LT != 255 || d.report.Buttons != 0x1000 {
		t.Fatalf("pressed: %+v", d.report)
	}
	for _, handle := range []uint16{1, 2, 3} {
		if err := d.Set(handle, 0); err != nil {
			t.Fatal(err)
		}
	}
	if d.report != (xboxReport{}) {
		t.Fatalf("neutral: %+v", d.report)
	}
	if err := d.Set(1, -99999); err != nil || d.report.LX != -32767 {
		t.Fatalf("clamp: %+v, %v", d.report, err)
	}
	if err := d.Set(99, 0); err == nil {
		t.Fatal("unknown handle accepted")
	}
}
