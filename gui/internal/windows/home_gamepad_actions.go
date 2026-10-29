package windows

import (
	"errors"
	"fmt"

	"github.com/voluminor/dji-rc-joystick/gui/internal/gamepad"
	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	datasettings "github.com/voluminor/dji-rc-joystick/mod/data/settings"
	"github.com/voluminor/dji-rc-joystick/mod/rc"
	targetsettings "github.com/voluminor/dji-rc-joystick/target/settings"

	"fyne.io/fyne/v2/dialog"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// // // // // // // // // //

func (obj *homeGamepadObj) current() targetsettings.MappingObj {
	mapping := obj.saved
	mapping.Backend = backendID(obj.backend.Selected)
	axes := &mapping.Axes
	axes.LeftStickH.Action = obj.axes[rc.AxisLeftH].action.Selected
	axes.LeftStickH.Invert = obj.axes[rc.AxisLeftH].invert.Checked
	axes.LeftStickV.Action = obj.axes[rc.AxisLeftV].action.Selected
	axes.LeftStickV.Invert = obj.axes[rc.AxisLeftV].invert.Checked
	axes.RightStickH.Action = obj.axes[rc.AxisRightH].action.Selected
	axes.RightStickH.Invert = obj.axes[rc.AxisRightH].invert.Checked
	axes.RightStickV.Action = obj.axes[rc.AxisRightV].action.Selected
	axes.RightStickV.Invert = obj.axes[rc.AxisRightV].invert.Checked
	axes.LeftWheel.Action = obj.axes[rc.AxisLeftWheel].action.Selected
	axes.LeftWheel.Invert = obj.axes[rc.AxisLeftWheel].invert.Checked
	axes.RightWheel.Action = obj.axes[rc.AxisRightWheel].action.Selected
	axes.RightWheel.Invert = obj.axes[rc.AxisRightWheel].invert.Checked
	axes.Mode = obj.mode.Selected

	buttons := &mapping.Buttons
	buttons.Rth = obj.buttons[rc.ButtonRTH].action.Selected
	buttons.Record = obj.buttons[rc.ButtonRecord].action.Selected
	buttons.CameraHalf = obj.buttons[rc.ButtonCameraHalf].action.Selected
	buttons.CameraFull = obj.buttons[rc.ButtonCameraFull].action.Selected
	buttons.C1 = obj.buttons[rc.ButtonC1].action.Selected
	buttons.C2 = obj.buttons[rc.ButtonC2].action.Selected
	return mapping
}

func (obj *homeGamepadObj) handleMappingChange() {
	if obj.updating {
		return
	}
	obj.dirty = obj.current() != obj.saved
	obj.owner.log.Debug().
		Bool("dirty", obj.dirty).
		Msg("GUI gamepad mapping changed")
	obj.updateEnabled()
}

func (obj *homeGamepadObj) updateControls(editable bool) {
	if editable {
		obj.backend.Enable()
	} else {
		obj.backend.Disable()
	}
	for _, field := range obj.axes {
		if editable {
			field.action.Enable()
			field.invert.Enable()
		} else {
			field.action.Disable()
			field.invert.Disable()
		}
	}
	if editable {
		obj.mode.Enable()
	} else {
		obj.mode.Disable()
	}

	unavailableHint := obj.owner.lg(
		"home.gamepad.control.unavailable",
		"Unavailable for this remote",
	)
	hasProfile := obj.profile.Name != ""
	capabilities := obj.profile.Capabilities()
	for button, field := range obj.buttons {
		available := !hasProfile || capabilities.Buttons[button]
		if editable && available {
			field.action.Enable()
		} else {
			field.action.Disable()
		}
		if available {
			field.item.HintText = ""
		} else {
			field.item.HintText = unavailableHint
		}
	}
	obj.buttonsCard.Refresh()
}

func (obj *homeGamepadObj) updateEnabled() {
	locked := gamepadLocked(obj.phase)
	obj.updateControls(!locked)

	if obj.dirty && !locked {
		obj.saveButton.Enable()
	} else {
		obj.saveButton.Disable()
	}

	switch obj.phase {
	case gamepad.PhaseStarting,
		gamepad.PhaseRunning,
		gamepad.PhaseWaiting:
		obj.runButton.SetText(obj.owner.lg("button.stop", "Stop"))
		obj.runButton.SetIcon(theme.MediaStopIcon())
		obj.runButton.Importance = widget.DangerImportance
		obj.runButton.Enable()

	case gamepad.PhaseStopping:
		obj.runButton.SetText(obj.owner.lg("button.stop", "Stop"))
		obj.runButton.SetIcon(theme.MediaStopIcon())
		obj.runButton.Importance = widget.DangerImportance
		obj.runButton.Disable()

	default:
		obj.runButton.SetText(obj.owner.lg("button.start", "Start"))
		obj.runButton.SetIcon(theme.MediaPlayIcon())
		obj.runButton.Importance = widget.HighImportance
		if obj.remotePhase == guiremote.PhaseConnected && !obj.dirty {
			obj.runButton.Enable()
		} else {
			obj.runButton.Disable()
		}
	}
}

func (obj *homeGamepadObj) updateStatus() {
	obj.status.Importance = gamepadImportance(obj.phase)
	obj.status.SetText(gamepadStatus(obj.phase, obj.owner.lg))

	obj.remoteIndicator.SetText(
		"● " + obj.owner.lg("home.gamepad.flow.remote", "Remote"),
	)
	if obj.remotePhase == guiremote.PhaseConnected {
		obj.remoteIndicator.Importance = widget.SuccessImportance
	} else {
		obj.remoteIndicator.Importance = widget.WarningImportance
	}

	obj.gamepadIndicator.SetText(
		"● " + obj.owner.lg("home.gamepad.flow.gamepad", "Gamepad"),
	)
	obj.gamepadIndicator.Importance = gamepadImportance(obj.phase)
	obj.saveButton.SetText(
		obj.owner.lg("home.gamepad.save", "Save settings"),
	)
	obj.updateEnabled()
}

func (obj *homeGamepadObj) showError(key, fallback string, err error) {
	displayErr := fmt.Errorf("%s: %w", obj.owner.lg(key, fallback), err)
	obj.owner.logError("virtual gamepad", displayErr)
	dialog.ShowError(displayErr, obj.owner.window)
}

func (obj *homeGamepadObj) showRuntimeError(
	key string,
	fallback string,
	err error,
	revision uint64,
) {
	displayErr := fmt.Errorf("%s: %w", obj.owner.lg(key, fallback), err)
	obj.owner.log.Debug().
		Str("dialog", "gamepad_runtime_error").
		Uint64("error_revision", revision).
		Msg("GUI error dialog opened")
	dialog.ShowError(displayErr, obj.owner.window)
}

// //

func (obj *homeGamepadObj) update(snapshot guiremote.SnapshotObj) {
	obj.phase = snapshot.Gamepad.Phase
	obj.remotePhase = snapshot.Phase
	obj.profile = snapshot.Profile
	obj.updateStatus()
	if obj.wantRunning && (obj.phase == gamepad.PhaseRunning || obj.phase == gamepad.PhaseWaiting) && !obj.owner.application.Preferences().Bool(gamepadResumePreference) {
		obj.owner.application.Preferences().SetBool(gamepadResumePreference, true)
	}
	if obj.autoStartPending && obj.remotePhase == guiremote.PhaseConnected && !gamepadLocked(obj.phase) && !obj.dirty {
		// Consume before Start: callbacks can enqueue another snapshot immediately.
		obj.autoStartPending = false
		obj.start()
	}

	if snapshot.Gamepad.ErrorRevision == obj.lastErrorRevision {
		return
	}
	obj.lastErrorRevision = snapshot.Gamepad.ErrorRevision
	if snapshot.Gamepad.Error != "" {
		obj.showRuntimeError(
			"home.gamepad.error.runtime",
			"Virtual gamepad failed",
			errors.New(snapshot.Gamepad.Error),
			snapshot.Gamepad.ErrorRevision,
		)
	}
}

func (obj *homeGamepadObj) save() {
	obj.owner.log.Debug().Msg("GUI gamepad save button activated")
	mapping := obj.current()
	if err := obj.owner.data.SaveMapping(mapping); err != nil {
		obj.showError(
			"home.gamepad.error.save",
			"Could not save gamepad settings",
			err,
		)
		return
	}
	obj.saved = mapping
	obj.dirty = false
	obj.updateEnabled()
	obj.owner.log.Info().Msg("Virtual gamepad settings saved")
}

func (obj *homeGamepadObj) toggle() {
	if gamepadLocked(obj.phase) {
		obj.autoStartPending = false
		obj.wantRunning = false
		obj.owner.application.Preferences().SetBool(gamepadResumePreference, false)
		obj.owner.log.Debug().Msg("GUI gamepad stop button activated")
		obj.runButton.Disable()
		go obj.remote.StopGamepad()
		return
	}

	obj.start()
}

func (obj *homeGamepadObj) start() {
	obj.wantRunning = true
	obj.owner.log.Debug().Msg("GUI gamepad start requested")
	mapping := datasettings.ControlMapping(obj.saved)
	if err := obj.remote.StartGamepad(mapping); err != nil {
		obj.showError(
			"home.gamepad.error.start",
			"Could not start virtual gamepad",
			err,
		)
	}
}
