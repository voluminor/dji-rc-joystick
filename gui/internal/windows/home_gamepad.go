package windows

import (
	"runtime"
	"strconv"

	"github.com/voluminor/dji-rc-joystick/gui/internal/gamepad"
	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	"github.com/voluminor/dji-rc-joystick/mod/rc"
	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// // // // // // // // // //

var analogGamepadActions = []string{
	"unmapped",
	"axis_x",
	"axis_y",
	"axis_z",
	"axis_rx",
	"axis_ry",
	"axis_rz",
	"axis_slider0",
	"axis_slider1",
	"wheel",
	"hwheel",
}

var buttonGamepadActions = buildButtonGamepadActions()

type axisFieldObj struct {
	action *widget.Select
	invert *widget.Check
	item   *widget.FormItem
}

type buttonFieldObj struct {
	action *widget.Select
	item   *widget.FormItem
}

type homeGamepadObj struct {
	owner  *Obj
	remote remoteInterface
	root   *widget.Card

	title            *widget.Label
	status           *widget.Label
	remoteIndicator  *widget.Label
	gamepadIndicator *widget.Label
	saveButton       *widget.Button
	runButton        *widget.Button
	platformHint     *widget.Label
	backend          *widget.Select

	leftStickCard  *widget.Card
	rightStickCard *widget.Card
	extraCard      *widget.Card
	buttonsCard    *widget.Card

	axes     map[rc.AxisID]*axisFieldObj
	mode     *widget.Select
	modeItem *widget.FormItem
	buttons  map[rc.ButtonID]*buttonFieldObj

	saved             targetsettings.MappingObj
	profile           rc.ProfileObj
	phase             gamepad.PhaseID
	remotePhase       guiremote.PhaseID
	lastErrorRevision uint64
	updating          bool
	dirty             bool
	autoStartPending  bool
	wantRunning       bool
}

const gamepadResumePreference = "gamepad.resume"

func backendLabel(backend string) string {
	if backend == "vjoy" {
		return "vJoy"
	}
	return "ViGEmBus"
}

func backendID(label string) string {
	if label == "vJoy" {
		return "vjoy"
	}
	return "vigem"
}

func buildButtonGamepadActions() []string {
	actions := make([]string, 0, 85)
	actions = append(actions, "unmapped", "button")
	for index := 1; index <= 79; index++ {
		actions = append(actions, "button"+strconv.Itoa(index))
	}
	return append(
		actions,
		"wheel_up",
		"wheel_down",
		"hwheel_left",
		"hwheel_right",
	)
}

func gamepadLocked(phase gamepad.PhaseID) bool {
	switch phase {
	case gamepad.PhaseStarting,
		gamepad.PhaseRunning,
		gamepad.PhaseWaiting,
		gamepad.PhaseStopping:
		return true
	default:
		return false
	}
}

func gamepadImportance(phase gamepad.PhaseID) widget.Importance {
	switch phase {
	case gamepad.PhaseRunning:
		return widget.SuccessImportance
	case gamepad.PhaseStarting,
		gamepad.PhaseWaiting,
		gamepad.PhaseStopping:
		return widget.WarningImportance
	case gamepad.PhaseError:
		return widget.DangerImportance
	default:
		return widget.LowImportance
	}
}

func gamepadStatus(
	phase gamepad.PhaseID,
	lg func(string, string) string,
) string {
	switch phase {
	case gamepad.PhaseStarting:
		return lg("home.gamepad.status.starting", "Starting emulation")
	case gamepad.PhaseRunning:
		return lg("home.gamepad.status.running", "Emulation is running")
	case gamepad.PhaseWaiting:
		return lg(
			"home.gamepad.status.waiting",
			"Waiting for remote — controls are neutral",
		)
	case gamepad.PhaseStopping:
		return lg("home.gamepad.status.stopping", "Stopping emulation")
	case gamepad.PhaseError:
		return lg("home.gamepad.status.error", "Emulation error")
	default:
		return lg("home.gamepad.status.stopped", "Emulation is stopped")
	}
}

func platformHint(lg func(string, string) string) string {
	switch runtime.GOOS {
	case "linux":
		return lg(
			"home.gamepad.platform.linux",
			"Linux: system authorization is required only when /dev/uinput cannot be accessed directly.",
		)
	case "windows":
		return lg(
			"home.gamepad.platform.windows",
			"Windows: an installed, enabled, and configured vJoy device is required.",
		)
	default:
		return lg(
			"home.gamepad.platform.unsupported",
			"Virtual gamepad emulation is not supported on this platform.",
		)
	}
}

func (obj *homeGamepadObj) newAxisField(
	axis rc.AxisID,
) *axisFieldObj {
	field := &axisFieldObj{}
	field.action = widget.NewSelect(analogGamepadActions, func(string) {
		obj.handleMappingChange()
	})
	field.invert = widget.NewCheck("", func(bool) {
		obj.handleMappingChange()
	})
	field.item = widget.NewFormItem(
		"",
		container.NewBorder(nil, nil, nil, field.invert, field.action),
	)
	obj.axes[axis] = field
	return field
}

func (obj *homeGamepadObj) newButtonField(
	button rc.ButtonID,
) *buttonFieldObj {
	field := &buttonFieldObj{}
	field.action = widget.NewSelect(buttonGamepadActions, func(string) {
		obj.handleMappingChange()
	})
	field.item = widget.NewFormItem("", field.action)
	obj.buttons[button] = field
	return field
}

func (obj *homeGamepadObj) buildHeader() *fyne.Container {
	obj.title = widget.NewLabel("")
	obj.title.TextStyle = fyne.TextStyle{Bold: true}
	obj.status = widget.NewLabel("")
	obj.status.Importance = widget.LowImportance

	obj.remoteIndicator = widget.NewLabel("")
	obj.gamepadIndicator = widget.NewLabel("")
	flow := container.NewHBox(
		obj.remoteIndicator,
		widget.NewLabel("→"),
		obj.gamepadIndicator,
	)

	obj.saveButton = widget.NewButtonWithIcon(
		"",
		theme.DocumentSaveIcon(),
		obj.save,
	)
	obj.runButton = widget.NewButtonWithIcon(
		"",
		theme.MediaPlayIcon(),
		obj.toggle,
	)
	obj.runButton.Importance = widget.HighImportance

	return container.NewBorder(
		nil,
		nil,
		container.NewVBox(obj.title, obj.status),
		container.NewHBox(obj.saveButton, obj.runButton),
		container.NewCenter(flow),
	)
}

func (obj *homeGamepadObj) buildFields() {
	leftH := obj.newAxisField(rc.AxisLeftH)
	leftV := obj.newAxisField(rc.AxisLeftV)
	rightH := obj.newAxisField(rc.AxisRightH)
	rightV := obj.newAxisField(rc.AxisRightV)
	leftWheel := obj.newAxisField(rc.AxisLeftWheel)
	rightWheel := obj.newAxisField(rc.AxisRightWheel)

	obj.mode = widget.NewSelect(analogGamepadActions, func(string) {
		obj.handleMappingChange()
	})
	obj.modeItem = widget.NewFormItem("", obj.mode)

	obj.leftStickCard = widget.NewCard(
		"",
		"",
		widget.NewForm(leftH.item, leftV.item),
	)
	obj.rightStickCard = widget.NewCard(
		"",
		"",
		widget.NewForm(rightH.item, rightV.item),
	)
	obj.extraCard = widget.NewCard(
		"",
		"",
		widget.NewForm(leftWheel.item, rightWheel.item, obj.modeItem),
	)

	rth := obj.newButtonField(rc.ButtonRTH)
	record := obj.newButtonField(rc.ButtonRecord)
	cameraHalf := obj.newButtonField(rc.ButtonCameraHalf)
	cameraFull := obj.newButtonField(rc.ButtonCameraFull)
	c1 := obj.newButtonField(rc.ButtonC1)
	c2 := obj.newButtonField(rc.ButtonC2)
	obj.buttonsCard = widget.NewCard(
		"",
		"",
		widget.NewForm(
			rth.item,
			record.item,
			cameraHalf.item,
			cameraFull.item,
			c1.item,
			c2.item,
		),
	)
}

func (obj *homeGamepadObj) build() {
	obj.buildFields()
	obj.backend = widget.NewSelect([]string{"ViGEmBus", "vJoy"}, func(string) {
		obj.handleMappingChange()
		obj.refreshPlatformHint()
	})
	backendRow := container.NewBorder(nil, nil, widget.NewLabel("Output backend"), nil, obj.backend)
	if runtime.GOOS != "windows" {
		backendRow.Hide()
	}
	header := obj.buildHeader()
	columns := container.NewGridWithColumns(
		2,
		container.NewVBox(
			obj.leftStickCard,
			obj.rightStickCard,
			obj.extraCard,
		),
		obj.buttonsCard,
	)
	obj.platformHint = widget.NewLabel("")
	obj.platformHint.Importance = widget.LowImportance
	obj.platformHint.Wrapping = fyne.TextWrapWord
	obj.root = widget.NewCard(
		"",
		"",
		container.NewVBox(
			header,
			backendRow,
			widget.NewSeparator(),
			columns,
			widget.NewSeparator(),
			obj.platformHint,
		),
	)
}

func (obj *homeGamepadObj) load(mapping targetsettings.MappingObj) {
	obj.updating = true
	obj.saved = mapping
	obj.backend.SetSelected(backendLabel(mapping.Backend))

	axes := mapping.Axes
	obj.axes[rc.AxisLeftH].action.SetSelected(axes.LeftStickH.Action)
	obj.axes[rc.AxisLeftH].invert.SetChecked(axes.LeftStickH.Invert)
	obj.axes[rc.AxisLeftV].action.SetSelected(axes.LeftStickV.Action)
	obj.axes[rc.AxisLeftV].invert.SetChecked(axes.LeftStickV.Invert)
	obj.axes[rc.AxisRightH].action.SetSelected(axes.RightStickH.Action)
	obj.axes[rc.AxisRightH].invert.SetChecked(axes.RightStickH.Invert)
	obj.axes[rc.AxisRightV].action.SetSelected(axes.RightStickV.Action)
	obj.axes[rc.AxisRightV].invert.SetChecked(axes.RightStickV.Invert)
	obj.axes[rc.AxisLeftWheel].action.SetSelected(axes.LeftWheel.Action)
	obj.axes[rc.AxisLeftWheel].invert.SetChecked(axes.LeftWheel.Invert)
	obj.axes[rc.AxisRightWheel].action.SetSelected(axes.RightWheel.Action)
	obj.axes[rc.AxisRightWheel].invert.SetChecked(axes.RightWheel.Invert)
	obj.mode.SetSelected(axes.Mode)

	buttons := mapping.Buttons
	obj.buttons[rc.ButtonRTH].action.SetSelected(buttons.Rth)
	obj.buttons[rc.ButtonRecord].action.SetSelected(buttons.Record)
	obj.buttons[rc.ButtonCameraHalf].action.SetSelected(buttons.CameraHalf)
	obj.buttons[rc.ButtonCameraFull].action.SetSelected(buttons.CameraFull)
	obj.buttons[rc.ButtonC1].action.SetSelected(buttons.C1)
	obj.buttons[rc.ButtonC2].action.SetSelected(buttons.C2)

	obj.dirty = false
	obj.updating = false
	obj.updateEnabled()
}

func newHomeGamepad(owner *Obj, remote remoteInterface) *homeGamepadObj {
	obj := &homeGamepadObj{
		owner:            owner,
		remote:           remote,
		axes:             make(map[rc.AxisID]*axisFieldObj, rc.AxisCount),
		buttons:          make(map[rc.ButtonID]*buttonFieldObj, rc.ButtonCount),
		phase:            gamepad.PhaseStopped,
		autoStartPending: owner.application.Preferences().Bool(gamepadResumePreference),
		wantRunning:      owner.application.Preferences().Bool(gamepadResumePreference),
	}
	obj.build()
	obj.load(owner.data.Mapping())
	obj.refreshLanguage()
	return obj
}

// //

func (obj *homeGamepadObj) refreshLanguage() {
	lg := obj.owner.lg
	obj.title.SetText(lg("home.gamepad.title", "Virtual gamepad"))
	obj.leftStickCard.SetTitle(lg("home.gamepad.group.left_stick", "Left stick"))
	obj.rightStickCard.SetTitle(lg("home.gamepad.group.right_stick", "Right stick"))
	obj.extraCard.SetTitle(lg("home.gamepad.group.extra", "Wheels and mode"))
	obj.buttonsCard.SetTitle(lg("home.gamepad.group.buttons", "Buttons"))

	obj.axes[rc.AxisLeftH].item.Text = lg(
		"home.gamepad.control.left_stick_h",
		"Horizontal",
	)
	obj.axes[rc.AxisLeftV].item.Text = lg(
		"home.gamepad.control.left_stick_v",
		"Vertical",
	)
	obj.axes[rc.AxisRightH].item.Text = lg(
		"home.gamepad.control.right_stick_h",
		"Horizontal",
	)
	obj.axes[rc.AxisRightV].item.Text = lg(
		"home.gamepad.control.right_stick_v",
		"Vertical",
	)
	obj.axes[rc.AxisLeftWheel].item.Text = lg(
		"home.gamepad.control.left_wheel",
		"Left wheel",
	)
	obj.axes[rc.AxisRightWheel].item.Text = lg(
		"home.gamepad.control.right_wheel",
		"Right wheel",
	)
	obj.modeItem.Text = lg("home.gamepad.control.mode", "Flight mode")
	for _, field := range obj.axes {
		field.invert.SetText(lg("home.gamepad.invert", "Invert"))
	}

	obj.buttons[rc.ButtonRTH].item.Text = lg(
		"home.gamepad.control.rth",
		"Return home / pause",
	)
	obj.buttons[rc.ButtonRecord].item.Text = lg(
		"home.gamepad.control.record",
		"Record",
	)
	obj.buttons[rc.ButtonCameraHalf].item.Text = lg(
		"home.gamepad.control.camera_half",
		"Camera half-press",
	)
	obj.buttons[rc.ButtonCameraFull].item.Text = lg(
		"home.gamepad.control.camera_full",
		"Camera shutter",
	)
	obj.buttons[rc.ButtonC1].item.Text = lg(
		"home.gamepad.control.c1",
		"Button C1",
	)
	obj.buttons[rc.ButtonC2].item.Text = lg(
		"home.gamepad.control.c2",
		"Button C2",
	)

	obj.refreshPlatformHint()
	obj.updateStatus()
	obj.leftStickCard.Refresh()
	obj.rightStickCard.Refresh()
	obj.extraCard.Refresh()
	obj.buttonsCard.Refresh()
}

func (obj *homeGamepadObj) refreshPlatformHint() {
	if obj.platformHint == nil {
		return
	}
	if runtime.GOOS == "windows" && obj.backend.Selected != "vJoy" {
		obj.platformHint.SetText(obj.owner.lg("home.gamepad.platform.vigem", "Windows: ViGEmBus and ViGEmClient.dll are required for Xbox 360 emulation."))
		return
	}
	obj.platformHint.SetText(platformHint(obj.owner.lg))
}
