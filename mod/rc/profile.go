package rc

// // // // // // // // // //

const (
	outMax           = 32767
	defaultProfileID = "rc"

	// Universal decode constants, shared by every DJI screen controller seen
	// so far (RM330, RC 2): byte-interleaved axes centered at 4, ±660 range.
	wrapCenter = 4
	axisRange  = 660
)

// //

type axisMapObj struct {
	Axis    AxisID
	Channel int
}

type buttonMapObj struct {
	Button  ButtonID
	Channel int
	Mask    uint16
}

type modeMapObj struct {
	Channel int
	Mask    uint16
}

// CapabilitiesObj tells consumers which controls the model actually has.
type CapabilitiesObj struct {
	// Axes marks every axis physically reported by the profile.
	Axes [AxisCount]bool
	// Buttons marks every button physically reported by the profile.
	Buttons [ButtonCount]bool
	// HasMode reports whether the flight-mode switch is available.
	HasMode bool
}

// The universal wire layout. Every DJI screen controller observed shares it,
// so decoding is model-independent; profiles only tweak identity, transport,
// and which controls are meaningful.
var (
	universalAxes = []axisMapObj{
		{Axis: AxisLeftH, Channel: 5},
		{Axis: AxisLeftV, Channel: 4},
		{Axis: AxisRightH, Channel: 2},
		{Axis: AxisRightV, Channel: 3},
		{Axis: AxisLeftWheel, Channel: 6},
		{Axis: AxisRightWheel, Channel: 7},
	}
	universalButtons = []buttonMapObj{
		{Button: ButtonRTH, Channel: 0, Mask: 0x0020},
		{Button: ButtonRecord, Channel: 0, Mask: 0x0100},
		{Button: ButtonCameraHalf, Channel: 0, Mask: 0x0080},
		{Button: ButtonCameraFull, Channel: 0, Mask: 0x0040},
		{Button: ButtonC1, Channel: 1, Mask: 0x0004},
		{Button: ButtonC2, Channel: 1, Mask: 0x0008},
	}
	universalMode = modeMapObj{Channel: 1, Mask: 0x0003}
)

// ProfileObj describes one remote model. It is a thin descriptor: the wire
// decoding is universal, so a profile only carries identity, transport hints,
// and which controls the model actually reports.
type ProfileObj struct {
	// ID is the stable profile identifier used in ConfigObj.
	ID string
	// Name is the user-visible model name.
	Name string
	// VendorID is used for the virtual device identity.
	VendorID uint16
	// ProductID is used for the virtual device identity.
	ProductID uint16
	// DeviceIDs contains identities announced by matching remotes.
	DeviceIDs []string

	// Keepalive is the model preference used by KeepaliveDefault.
	Keepalive KeepaliveMode
	// Connections is the preferred overlapping-link count.
	Connections int
	// Handshake answers recurring RC polls by default for this model.
	Handshake bool

	// AbsentButtons lists universal controls the model never reports.
	AbsentButtons []ButtonID
}

// //

func builtin() []ProfileObj {
	return []ProfileObj{
		{
			ID:        "rc",
			Name:      "DJI RC",
			VendorID:  0x2CA3,
			ProductID: 0x1023,
			DeviceIDs: []string{"rm330"},
			// RM330 firmware 01.00.03.14 recycles sessions and reacts poorly
			// to repeated inquiries. Overlap passive readers and answer polls.
			Keepalive:   KeepaliveOff,
			Connections: 5,
			Handshake:   true,
		},
		{
			// Field-tested (announces "rc331"): identical wire layout to the
			// RM330, but its camera button only ever reports full-press (0x40);
			// the half-press bit 0x80 is never set. It also recycles the
			// port-40007 session ~every 2.5s and resets fast under any prod, so
			// it stays silent across several overlapping links.
			ID:            "rc2",
			Name:          "DJI RC 2",
			VendorID:      0x2CA3,
			ProductID:     0x1023,
			DeviceIDs:     []string{"rc331"},
			Keepalive:     KeepaliveOff,
			Connections:   5,
			AbsentButtons: []ButtonID{ButtonCameraHalf},
		},
	}
}

// decodeAxis converts the byte-interleaved encoding into ±32767.
// position = payload[2*ch+1], wrap = payload[2*ch+2],
// value = (wrap - wrapCenter) * 256 + position, range ±axisRange.
func cloneProfile(profile ProfileObj) ProfileObj {
	profile.DeviceIDs = append([]string(nil), profile.DeviceIDs...)
	profile.AbsentButtons = append([]ButtonID(nil), profile.AbsentButtons...)
	return profile
}

func decodeAxis(axisMap axisMapObj, raw *RawObj) int16 {
	posByte := 2*axisMap.Channel + 1
	wrapByte := 2*axisMap.Channel + 2
	if axisMap.Channel < 0 || wrapByte >= raw.Len {
		return 0
	}

	value := (int32(raw.Payload[wrapByte])-wrapCenter)*256 + int32(raw.Payload[posByte])
	scaled := value * outMax / axisRange
	if scaled > outMax {
		scaled = outMax
	}
	if scaled < -outMax {
		scaled = -outMax
	}
	return int16(scaled)
}

// decodeState maps raw channels into a semantic state using the universal
// layout. It is model-independent; a model that lacks a button simply never
// sets that bit on the wire.
func decodeState(raw *RawObj) StateObj {
	var state StateObj

	for _, axisMap := range universalAxes {
		state.Axes[axisMap.Axis] = decodeAxis(axisMap, raw)
	}

	for _, buttonMap := range universalButtons {
		if buttonMap.Channel < 0 || buttonMap.Channel >= raw.Count {
			continue
		}
		if uint16(raw.Channels[buttonMap.Channel])&buttonMap.Mask != 0 {
			state.Buttons |= 1 << uint(buttonMap.Button)
		}
	}

	state.Mode = ModeUnknown
	if universalMode.Channel < raw.Count {
		value := uint16(raw.Channels[universalMode.Channel]) & universalMode.Mask
		if value <= uint16(ModeCinema) {
			state.Mode = ModeID(value)
		}
	}

	return state
}

func (p ProfileObj) hasButton(b ButtonID) bool {
	for _, absent := range p.AbsentButtons {
		if absent == b {
			return false
		}
	}
	return true
}

// //

// Profiles returns independent copies of all known remote profiles.
func Profiles() []ProfileObj {
	return builtin()
}

// ProfileByID finds a profile by its identifier.
func ProfileByID(id string) (ProfileObj, bool) {
	for _, profile := range builtin() {
		if profile.ID == id {
			return profile, true
		}
	}
	return ProfileObj{}, false
}

// ProfileByDeviceID finds a profile matching an announced device identity.
func ProfileByDeviceID(id string) (ProfileObj, bool) {
	for _, profile := range builtin() {
		for _, known := range profile.DeviceIDs {
			if known == id {
				return profile, true
			}
		}
	}
	return ProfileObj{}, false
}

// DefaultProfile is the profile used before a remote is identified.
func DefaultProfile() ProfileObj {
	profile, _ := ProfileByID(defaultProfileID)
	return profile
}

// Capabilities reports which semantic controls this model provides.
func (p ProfileObj) Capabilities() CapabilitiesObj {
	var caps CapabilitiesObj
	for _, axisMap := range universalAxes {
		caps.Axes[axisMap.Axis] = true
	}
	for _, buttonMap := range universalButtons {
		caps.Buttons[buttonMap.Button] = p.hasButton(buttonMap.Button)
	}
	caps.HasMode = true
	return caps
}
