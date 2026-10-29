package windows

import (
	"errors"
	"strings"

	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	"github.com/voluminor/dji-rc-joystick/mod/rc"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/dialog"
)

// // // // // // // // // //

func (obj *homeObj) replaceRemotes(remotes []rc.RemoteObj) {
	options := make([]string, 0, len(remotes))
	obj.remoteByLabel = make(map[string]rc.RemoteObj, len(remotes))
	for _, remoteObj := range remotes {
		label := formatRemoteOption(remoteObj)
		options = append(options, label)
		obj.remoteByLabel[label] = remoteObj
	}
	obj.remoteSelect.SetOptions(options)
	if len(options) == 0 {
		obj.remoteSelect.ClearSelected()
		obj.remoteSelect.Disable()
		obj.autoConnectButton.Disable()
		obj.automaticResults.Hide()
		return
	}
	obj.remoteSelect.Enable()
	obj.remoteSelect.SetSelectedIndex(0)
	obj.automaticResults.Show()
}

func (obj *homeObj) handleResults(snapshot guiremote.SnapshotObj) {
	if snapshot.SearchRevision == obj.lastSearchRevision {
		return
	}
	obj.lastSearchRevision = snapshot.SearchRevision
	if snapshot.SearchCount == 0 {
		obj.owner.log.Debug().
			Str("dialog", "search_empty").
			Msg("GUI information dialog opened")
		dialog.ShowInformation(
			obj.owner.lg("home.search.none.title", "Search"),
			obj.owner.lg("home.search.none.message", "No remotes found."),
			obj.owner.window,
		)
		return
	}
	obj.replaceRemotes(snapshot.Remotes)
}

func (obj *homeObj) handleError(snapshot guiremote.SnapshotObj) {
	if snapshot.ErrorRevision == obj.lastErrorRevision {
		return
	}
	obj.lastErrorRevision = snapshot.ErrorRevision
	if snapshot.Error != "" {
		err := errors.New(snapshot.Error)
		obj.owner.log.Debug().
			Str("dialog", "remote_runtime_error").
			Uint64("error_revision", snapshot.ErrorRevision).
			Msg("GUI error dialog opened")
		dialog.ShowError(err, obj.owner.window)
	}
}

// //

func (obj *homeObj) startSearch() {
	obj.owner.log.Debug().Msg("GUI search button activated")
	if err := obj.remote.Search(); err != nil {
		obj.owner.logError("start remote search", err)
		dialog.ShowError(err, obj.owner.window)
		return
	}
	obj.applySnapshot(obj.remote.Snapshot())
}

func (obj *homeObj) connectAutomatic() {
	remoteObj, ok := obj.remoteByLabel[obj.remoteSelect.Selected]
	if !ok {
		obj.owner.log.Debug().Msg("GUI automatic connection had no selected remote")
		return
	}
	obj.owner.log.Debug().
		Str("ip", remoteObj.IP).
		Msg("GUI automatic connect button activated")
	obj.connect(remoteObj.IP, rc.DefaultPort)
}

func (obj *homeObj) connectManual() {
	obj.owner.log.Debug().Msg("GUI manual connect button activated")
	port, err := parsePort(obj.portEntry.Text)
	if err != nil {
		displayErr := errors.New(obj.owner.lg(
			"home.manual.port.invalid",
			"Enter a port from 1 to 65535.",
		))
		obj.owner.logError("validate manual connection port", displayErr)
		dialog.ShowError(displayErr, obj.owner.window)
		return
	}
	obj.connect(strings.TrimSpace(obj.ipEntry.Text), port)
}

func (obj *homeObj) connect(ip string, port int) {
	if err := obj.remote.Connect(ip, port); err != nil {
		obj.owner.logError("connect remote", err)
		dialog.ShowError(err, obj.owner.window)
		return
	}
	preferences := obj.owner.application.Preferences()
	preferences.SetString(lastRemoteIPPreference, ip)
	preferences.SetInt(lastRemotePortPreference, port)
	obj.applySnapshot(obj.remote.Snapshot())
}

func (obj *homeObj) cancelOperation() {
	obj.owner.log.Debug().Msg("GUI operation cancel button activated")
	if obj.modal != nil {
		obj.modal.setCancelling(obj.owner.lg)
	}
	go obj.remote.Stop()
}

func (obj *homeObj) acceptFound() {
	obj.owner.log.Debug().Msg("GUI accept-found button activated")
	if obj.modal != nil {
		obj.modal.setAccepting(obj.owner.lg)
	}
	go func() {
		if err := obj.remote.AcceptFound(); err != nil {
			fyne.Do(func() {
				obj.owner.logError("accept partial search results", err)
				dialog.ShowError(err, obj.owner.window)
				obj.applySnapshot(obj.remote.Snapshot())
			})
		}
	}()
}

func (obj *homeObj) disconnect() {
	obj.owner.log.Debug().Msg("GUI disconnect button activated")
	obj.disconnectButton.Disable()
	go obj.remote.Stop()
}
