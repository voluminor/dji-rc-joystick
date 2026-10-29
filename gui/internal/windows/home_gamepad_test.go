package windows

import (
	"testing"

	"github.com/voluminor/dji-rc-joystick/gui/internal/gamepad"
	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	datasettings "github.com/voluminor/dji-rc-joystick/mod/data/settings"
	"github.com/voluminor/dji-rc-joystick/mod/rc"

	"fyne.io/fyne/v2"
)

// // // // // // // // // //

func connectedGamepadSnapshot(
	phase gamepad.PhaseID,
	profile rc.ProfileObj,
) guiremote.SnapshotObj {
	return guiremote.SnapshotObj{
		Phase:   guiremote.PhaseConnected,
		Profile: profile,
		Gamepad: gamepad.SnapshotObj{Phase: phase},
	}
}

func differentAnalogAction(current string) string {
	for _, action := range analogGamepadActions {
		if action != current {
			return action
		}
	}
	return current
}

// //

func TestGamepadSaveTracksOnlyUnsavedChanges(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	view := windowObj.home.gamepad
	applyHomeSnapshot(
		windowObj.home,
		connectedGamepadSnapshot(gamepad.PhaseStopped, rc.DefaultProfile()),
	)

	if !view.saveButton.Disabled() {
		t.Fatal("save button is enabled without changes")
	}
	if view.runButton.Disabled() {
		t.Fatal("start button is disabled for saved settings")
	}

	field := view.axes[rc.AxisLeftH].action
	savedAction := field.Selected
	changedAction := differentAnalogAction(savedAction)
	fyne.DoAndWait(func() {
		field.SetSelected(changedAction)
	})
	if !view.dirty || view.saveButton.Disabled() {
		t.Fatal("changed mapping is not marked as unsaved")
	}
	if !view.runButton.Disabled() {
		t.Fatal("start button is enabled with unsaved settings")
	}

	fyne.DoAndWait(func() {
		field.SetSelected(savedAction)
	})
	if view.dirty || !view.saveButton.Disabled() {
		t.Fatal("restored mapping is still marked as unsaved")
	}
	if view.runButton.Disabled() {
		t.Fatal("start button stayed disabled after restoring saved settings")
	}

	fyne.DoAndWait(func() {
		field.SetSelected(changedAction)
		view.save()
	})
	if view.dirty || !view.saveButton.Disabled() {
		t.Fatal("saved mapping remains dirty")
	}
	if got := windowObj.data.Mapping().Axes.LeftStickH.Action; got != changedAction {
		t.Fatalf("saved action = %q, want %q", got, changedAction)
	}
}

func TestGamepadLocksSettingsWhileRunningOrWaiting(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	view := windowObj.home.gamepad

	for _, phase := range []gamepad.PhaseID{
		gamepad.PhaseRunning,
		gamepad.PhaseWaiting,
	} {
		applyHomeSnapshot(
			windowObj.home,
			connectedGamepadSnapshot(phase, rc.DefaultProfile()),
		)
		for axis, field := range view.axes {
			if !field.action.Disabled() || !field.invert.Disabled() {
				t.Fatalf("axis %d is editable in phase %s", axis, phase)
			}
		}
		for button, field := range view.buttons {
			if !field.action.Disabled() {
				t.Fatalf("button %d is editable in phase %s", button, phase)
			}
		}
		if !view.mode.Disabled() || !view.saveButton.Disabled() {
			t.Fatalf("mode or save is enabled in phase %s", phase)
		}
		if !view.backend.Disabled() {
			t.Fatal("backend can change while emulation owns a device")
		}
		if view.runButton.Disabled() || view.runButton.Text != "Stop" {
			t.Fatalf("stop action is unavailable in phase %s", phase)
		}
	}

	if view.status.Text != "Waiting for remote — controls are neutral" {
		t.Fatalf("waiting status = %q", view.status.Text)
	}
}

func TestGamepadBackendDefaultsAndPersists(t *testing.T) {
	window, _ := newTestWindow(t, newFakeRemote())
	view := window.home.gamepad
	if view.backend.Selected != "ViGEmBus" {
		t.Fatalf("default backend = %q", view.backend.Selected)
	}
	fyne.DoAndWait(func() {
		view.backend.SetSelected("vJoy")
		view.save()
	})
	mapping := window.data.Mapping()
	if mapping.Backend != "vjoy" {
		t.Fatalf("saved backend = %q", mapping.Backend)
	}
	if datasettings.ControlMapping(mapping).DeviceSpec(rc.DefaultProfile()).Backend != "vjoy" {
		t.Fatal("selected backend did not reach device specification")
	}
}

func TestGamepadResumeWaitsForConnectionAndRunsOnce(t *testing.T) {
	remote := newFakeRemote()
	window, app := newTestWindow(t, remote)
	app.Preferences().SetBool(gamepadResumePreference, true)
	view := newHomeGamepad(window, remote)
	fyne.DoAndWait(func() {
		view.update(guiremote.SnapshotObj{Phase: guiremote.PhaseIdle})
		if remote.gamepadStarts.Load() != 0 {
			t.Error("emulation started without a connected RC")
		}
		view.update(connectedGamepadSnapshot(gamepad.PhaseStopped, rc.DefaultProfile()))
		view.update(connectedGamepadSnapshot(gamepad.PhaseStopped, rc.DefaultProfile()))
	})
	if got := remote.gamepadStarts.Load(); got != 1 {
		t.Fatalf("resume starts = %d, want 1", got)
	}
}

func TestGamepadStopDisablesResumeButShutdownDoesNot(t *testing.T) {
	remote := newFakeRemote()
	window, app := newTestWindow(t, remote)
	view := window.home.gamepad
	fyne.DoAndWait(func() {
		view.wantRunning = true
		view.update(connectedGamepadSnapshot(gamepad.PhaseRunning, rc.DefaultProfile()))
		if !app.Preferences().Bool(gamepadResumePreference) {
			t.Error("successful start was not remembered")
		}
		// Automatic teardown must not overwrite the last successful running choice.
		view.update(guiremote.SnapshotObj{Phase: guiremote.PhaseIdle})
		if !app.Preferences().Bool(gamepadResumePreference) {
			t.Error("session teardown cleared resume")
		}
		view.update(connectedGamepadSnapshot(gamepad.PhaseRunning, rc.DefaultProfile()))
		view.toggle()
		// A queued running snapshot after Stop must not re-enable resume.
		view.update(connectedGamepadSnapshot(gamepad.PhaseRunning, rc.DefaultProfile()))
	})
	if app.Preferences().Bool(gamepadResumePreference) {
		t.Fatal("Stop did not disable resume")
	}
}

func TestGamepadDisablesControlsAbsentFromProfile(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	view := windowObj.home.gamepad
	profile, ok := rc.ProfileByID("rc2")
	if !ok {
		t.Fatal("DJI RC 2 profile is missing")
	}

	applyHomeSnapshot(
		windowObj.home,
		connectedGamepadSnapshot(gamepad.PhaseStopped, profile),
	)
	cameraHalf := view.buttons[rc.ButtonCameraHalf]
	if !cameraHalf.action.Disabled() || cameraHalf.item.HintText == "" {
		t.Fatal("camera half-press is not marked unavailable for DJI RC 2")
	}
	if view.buttons[rc.ButtonRTH].action.Disabled() {
		t.Fatal("available DJI RC 2 button is disabled")
	}
}

func TestGamepadStartUsesSavedMapping(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	view := windowObj.home.gamepad
	applyHomeSnapshot(
		windowObj.home,
		connectedGamepadSnapshot(gamepad.PhaseStopped, rc.DefaultProfile()),
	)

	fyne.DoAndWait(view.toggle)
	if got := remoteObj.gamepadStarts.Load(); got != 1 {
		t.Fatalf("gamepad starts = %d, want 1", got)
	}
}
