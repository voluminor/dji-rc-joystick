package rc

import (
	"testing"

	"github.com/voluminor/dji-rc-joystick/mod/vjoy"
)

// // // // // // // // // //

// setAxisRaw encodes a raw axis value into the byte-interleaved layout.
func setAxisRaw(payload []byte, channel, value int) {
	wrapOffset := value / 256
	pos := value % 256
	if pos < 0 {
		pos += 256
		wrapOffset--
	}
	payload[2*channel+1] = byte(pos)
	payload[2*channel+2] = byte(wrapOffset + 4)
}

func rawFromPayload(payload []byte) RawObj {
	var raw RawObj
	raw.Len = copy(raw.Payload[:], payload)
	n := 0
	for i := 0; i+1 < len(payload) && n < maxChannels; i += 2 {
		raw.Channels[n] = int16(uint16(payload[i]) | uint16(payload[i+1])<<8)
		n++
	}
	raw.Count = n
	return raw
}

func centeredPayload() []byte {
	payload := make([]byte, 17)
	for ch := 2; ch <= 7; ch++ {
		setAxisRaw(payload, ch, 0)
	}
	return payload
}

// //

func TestDecodeAxesUsePhysicalDirection(t *testing.T) {
	tests := []struct {
		axis    AxisID
		channel int
	}{
		{axis: AxisLeftH, channel: 5},
		{axis: AxisLeftV, channel: 4},
		{axis: AxisRightH, channel: 2},
		{axis: AxisRightV, channel: 3},
		{axis: AxisLeftWheel, channel: 6},
		{axis: AxisRightWheel, channel: 7},
	}
	for _, test := range tests {
		t.Run(test.axis.String(), func(t *testing.T) {
			payload := centeredPayload()
			setAxisRaw(payload, test.channel, axisRange)

			raw := rawFromPayload(payload)
			state := decodeState(&raw)
			if state.Axes[test.axis] != outMax {
				t.Fatalf("%s = %d, want %d", test.axis, state.Axes[test.axis], outMax)
			}
		})
	}
}

func TestDecodeClampsOutOfRange(t *testing.T) {
	payload := centeredPayload()
	setAxisRaw(payload, 4, -1024) // left stick vertical, beyond ±660

	raw := rawFromPayload(payload)
	state := decodeState(&raw)
	if state.Axes[AxisLeftV] != -outMax {
		t.Fatalf("AxisLeftV = %d, want clamp to %d", state.Axes[AxisLeftV], -outMax)
	}
}

func TestDecodeButtonsAndMode(t *testing.T) {
	payload := centeredPayload()
	payload[0] = 0x20               // RTH
	payload[1] = 0x01               // Record (0x0100)
	payload[2] = 0x02 | 0x04 | 0x08 // cinema mode + C1 + C2

	raw := rawFromPayload(payload)
	state := decodeState(&raw)

	for _, pressed := range []ButtonID{ButtonRTH, ButtonRecord, ButtonC1, ButtonC2} {
		if !state.Pressed(pressed) {
			t.Fatalf("button %s must be pressed", pressed)
		}
	}
	for _, released := range []ButtonID{ButtonCameraHalf, ButtonCameraFull} {
		if state.Pressed(released) {
			t.Fatalf("button %s must be released", released)
		}
	}
	if state.Mode != ModeCinema {
		t.Fatalf("mode = %s, want cinema", state.Mode)
	}
}

func TestDecodeShortPayload(t *testing.T) {
	raw := rawFromPayload([]byte{0x20, 0x00, 0x01})
	state := decodeState(&raw)

	for axis := AxisID(0); axis < AxisCount; axis++ {
		if state.Axes[axis] != 0 {
			t.Fatalf("axis %d decoded from a short payload", axis)
		}
	}
	if !state.Pressed(ButtonRTH) {
		t.Fatal("channel 0 button lost on a short payload")
	}
	if state.Mode != ModeUnknown {
		t.Fatalf("mode = %s, want unknown (channel 1 incomplete)", state.Mode)
	}
}

func TestProfileRegistry(t *testing.T) {
	for _, id := range []string{"rc", "rc2"} {
		profile, ok := ProfileByID(id)
		if !ok {
			t.Fatalf("profile %q missing", id)
		}

		axes, buttons := countControls(DefaultMapping().DeviceSpec(profile))
		if axes != len(universalAxes)+1 { // 6 axes + mode switch
			t.Fatalf("%s: %d spec axes, want %d", id, axes, len(universalAxes)+1)
		}
		if buttons != len(universalButtons) {
			t.Fatalf("%s: %d spec buttons, want %d (universal layout)", id, buttons, len(universalButtons))
		}

		caps := profile.Capabilities()
		if !caps.HasMode {
			t.Fatalf("%s: mode switch capability missing", id)
		}
		for axis := AxisID(0); axis < AxisCount; axis++ {
			if !caps.Axes[axis] {
				t.Fatalf("%s: axis %d capability missing", id, axis)
			}
		}
	}

	if _, ok := ProfileByID("unknown-model"); ok {
		t.Fatal("unknown profile id resolved")
	}
	if len(Profiles()) < 2 {
		t.Fatal("registry lost profiles")
	}
}

func TestProfileByDeviceID(t *testing.T) {
	profile, ok := ProfileByDeviceID("rc331")
	if !ok || profile.ID != "rc2" {
		t.Fatalf("rc331 -> %q (ok=%t), want rc2", profile.ID, ok)
	}
	if _, ok := ProfileByDeviceID("unknown-id"); ok {
		t.Fatal("unknown device id resolved")
	}
}

func TestRC2TransportHints(t *testing.T) {
	rc2, _ := ProfileByID("rc2")
	if rc2.Keepalive != KeepaliveOff {
		t.Fatalf("rc2 keepalive = %d, want KeepaliveOff", rc2.Keepalive)
	}
	if rc2.Connections != 5 {
		t.Fatalf("rc2 connections = %d, want 5", rc2.Connections)
	}

	rm330, _ := ProfileByID("rc")
	if rm330.Keepalive != KeepaliveOff || rm330.Connections != 5 || !rm330.Handshake {
		t.Fatal("rc should use staggered passive readers with poll replies")
	}
}

func TestEffectiveTransportResolution(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.effectiveConnections() != 5 || obj.effectiveKeepalive() != KeepaliveOff || !obj.Profile().Handshake {
		t.Fatalf("default transport = %d/%d", obj.effectiveConnections(), obj.effectiveKeepalive())
	}

	rc2, _ := ProfileByID("rc2")
	obj.setProfile(rc2)
	if obj.effectiveConnections() != rc2.Connections || obj.effectiveKeepalive() != KeepaliveOff {
		t.Fatalf("rc2 transport = %d/%d", obj.effectiveConnections(), obj.effectiveKeepalive())
	}

	locked, _ := New(ConfigObj{IP: "127.0.0.1", ProfileID: "rc2", Connections: 2, Keepalive: KeepaliveVersion})
	if locked.effectiveConnections() != 2 || locked.effectiveKeepalive() != KeepaliveVersion {
		t.Fatalf("explicit config overridden: %d/%d", locked.effectiveConnections(), locked.effectiveKeepalive())
	}
}

func TestRC2HasNoCameraHalfPress(t *testing.T) {
	rc2, _ := ProfileByID("rc2")
	if rc2.Capabilities().Buttons[ButtonCameraHalf] {
		t.Fatal("rc2 must not expose a camera half-press")
	}
	rm330, _ := ProfileByID("rc")
	if !rm330.Capabilities().Buttons[ButtonCameraHalf] {
		t.Fatal("rc must keep its camera half-press")
	}
}

func countControls(spec vjoy.SpecObj) (axes, buttons int) {
	for _, c := range spec.Controls {
		switch c.Kind {
		case vjoy.KindAxis:
			axes++
		case vjoy.KindButton:
			buttons++
		}
	}
	return axes, buttons
}

func TestProfilesShareUniversalLayout(t *testing.T) {
	for _, profile := range Profiles() {
		axes, buttons := countControls(DefaultMapping().DeviceSpec(profile))
		if axes != len(universalAxes)+1 || buttons != len(universalButtons) {
			t.Fatalf("%q: layout %d axes / %d buttons diverges from universal", profile.ID, axes, buttons)
		}
	}
}
