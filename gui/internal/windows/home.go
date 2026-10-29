package windows

import (
	"context"
	"errors"
	"strconv"
	"sync/atomic"
	"time"

	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	"github.com/voluminor/dji-rc-joystick/mod/rc"

	"fyne.io/fyne/v2"
	"fyne.io/fyne/v2/container"
	"fyne.io/fyne/v2/theme"
	"fyne.io/fyne/v2/widget"
)

// // // // // // // // // //

const homeRefreshInterval = time.Second / 30

const lastRemoteIPPreference = "connection.last_ip"
const lastRemotePortPreference = "connection.last_port"

type homeObj struct {
	owner    *Obj
	remote   remoteInterface
	root     fyne.CanvasObject
	info     *homeInfoObj
	controls *homeControlsObj
	gamepad  *homeGamepadObj

	connectionStatus   *widget.Label
	connectionEndpoint *widget.Label
	mode               *widget.RadioGroup
	automaticLabel     string
	manualLabel        string
	manual             bool
	updatingMode       bool
	automatic          *fyne.Container
	automaticResults   *fyne.Container
	manualForm         *fyne.Container
	disconnected       *fyne.Container
	connected          *fyne.Container
	remoteSelect       *widget.Select
	remoteByLabel      map[string]rc.RemoteObj
	searchButton       *widget.Button
	autoConnectButton  *widget.Button
	ipLabel            *widget.Label
	portLabel          *widget.Label
	ipEntry            *widget.Entry
	portEntry          *widget.Entry
	manualConnect      *widget.Button
	disconnectButton   *widget.Button
	modal              *progressDialogObj

	lastSnapshot       guiremote.SnapshotObj
	lastSearchRevision uint64
	lastErrorRevision  uint64
	wasConnected       bool

	cancelUpdates context.CancelFunc
	updatesDone   chan struct{}
	closed        atomic.Bool
}

// //

func newHome(owner *Obj) *homeObj {
	home := &homeObj{
		owner:         owner,
		remote:        owner.remote,
		info:          newHomeInfo(),
		controls:      newHomeControls(),
		remoteByLabel: make(map[string]rc.RemoteObj),
		updatesDone:   make(chan struct{}),
	}
	home.gamepad = newHomeGamepad(owner, owner.remote)
	home.build()
	home.refreshLanguage()
	home.applySnapshot(home.remote.Snapshot())
	return home
}

func (obj *homeObj) buildAutomatic() *fyne.Container {
	obj.remoteSelect = widget.NewSelect(nil, func(value string) {
		remoteObj, ok := obj.remoteByLabel[value]
		obj.owner.log.Debug().
			Bool("valid", ok).
			Str("ip", remoteObj.IP).
			Msg("GUI remote selection changed")
		if ok {
			obj.autoConnectButton.Enable()
		} else {
			obj.autoConnectButton.Disable()
		}
	})
	obj.remoteSelect.Disable()

	obj.searchButton = widget.NewButtonWithIcon("", theme.SearchIcon(), obj.startSearch)
	obj.searchButton.Importance = widget.HighImportance
	obj.autoConnectButton = widget.NewButtonWithIcon(
		obj.owner.lg("button.connect", "Connect"),
		theme.LoginIcon(),
		obj.connectAutomatic,
	)
	obj.autoConnectButton.Importance = widget.HighImportance
	obj.autoConnectButton.Disable()

	obj.automaticResults = container.NewVBox(
		obj.remoteSelect,
		obj.autoConnectButton,
	)
	obj.automaticResults.Hide()
	return container.NewVBox(obj.automaticResults)
}

func (obj *homeObj) buildManual() *fyne.Container {
	obj.ipLabel = widget.NewLabel(obj.owner.lg("home.manual.ip", "IP address:"))
	obj.ipLabel.Alignment = fyne.TextAlignTrailing
	obj.portLabel = widget.NewLabel(obj.owner.lg("home.manual.port", "Port:"))
	obj.portLabel.Alignment = fyne.TextAlignTrailing
	obj.ipEntry = widget.NewEntry()
	obj.ipEntry.Validator = func(value string) error {
		if err := validateIPAddress(value); err != nil {
			return errors.New(obj.owner.lg(
				"home.manual.ip.invalid",
				"Enter a valid IP address.",
			))
		}
		return nil
	}
	obj.ipEntry.AlwaysShowValidationError = true
	obj.portEntry = widget.NewEntry()
	obj.portEntry.Validator = func(value string) error {
		if _, err := parsePort(value); err != nil {
			return errors.New(obj.owner.lg(
				"home.manual.port.invalid",
				"Enter a port from 1 to 65535.",
			))
		}
		return nil
	}
	obj.portEntry.AlwaysShowValidationError = true

	obj.manualConnect = widget.NewButtonWithIcon(
		obj.owner.lg("button.connect", "Connect"),
		theme.LoginIcon(),
		obj.connectManual,
	)
	obj.manualConnect.Importance = widget.HighImportance
	obj.manualConnect.Disable()

	obj.portEntry.SetText(strconv.Itoa(rc.DefaultPort))
	obj.ipEntry.OnChanged = func(string) {
		obj.updateManualConnect()
		obj.owner.log.Debug().
			Str("field", "ip").
			Bool("valid", validateIPAddress(obj.ipEntry.Text) == nil).
			Int("length", len(obj.ipEntry.Text)).
			Msg("GUI connection field changed")
	}
	obj.portEntry.OnChanged = func(string) {
		obj.updateManualConnect()
		_, err := parsePort(obj.portEntry.Text)
		obj.owner.log.Debug().
			Str("field", "port").
			Bool("valid", err == nil).
			Int("length", len(obj.portEntry.Text)).
			Msg("GUI connection field changed")
	}
	preferences := obj.owner.application.Preferences()
	if ip := preferences.String(lastRemoteIPPreference); validateIPAddress(ip) == nil {
		obj.ipEntry.SetText(ip)
	}
	if port := preferences.Int(lastRemotePortPreference); port > 0 && port <= 65535 {
		obj.portEntry.SetText(strconv.Itoa(port))
	}
	obj.updateManualConnect()

	fields := container.NewGridWithColumns(
		2,
		obj.ipLabel,
		obj.ipEntry,
		obj.portLabel,
		obj.portEntry,
	)
	return container.NewVBox(fields, obj.manualConnect)
}

func (obj *homeObj) build() {
	obj.automatic = obj.buildAutomatic()
	obj.manualForm = obj.buildManual()
	obj.mode = widget.NewRadioGroup(nil, obj.changeMode)
	obj.mode.Horizontal = true
	obj.mode.Required = true

	obj.disconnected = container.NewVBox(
		container.NewBorder(nil, nil, nil, obj.searchButton, obj.mode),
		obj.automatic,
		obj.manualForm,
	)
	obj.disconnectButton = widget.NewButtonWithIcon(
		obj.owner.lg("button.disconnect", "Disconnect"),
		theme.MediaStopIcon(),
		obj.disconnect,
	)
	obj.disconnectButton.Importance = widget.DangerImportance
	obj.connected = obj.buildConnected()

	content := container.NewVBox(
		obj.disconnected,
		obj.connected,
	)
	obj.root = container.NewVScroll(content)
	obj.setManual(false)
	obj.showDisconnected()
}

func (obj *homeObj) setManual(manual bool) {
	obj.manual = manual
	if manual {
		obj.searchButton.Disable()
		obj.automatic.Hide()
		obj.manualForm.Show()
	} else {
		obj.searchButton.Enable()
		obj.manualForm.Hide()
		obj.automatic.Show()
	}
	obj.disconnected.Refresh()
}

func (obj *homeObj) changeMode(value string) {
	if obj.updatingMode {
		return
	}
	manual := value == obj.manualLabel
	obj.owner.log.Debug().
		Bool("manual", manual).
		Msg("GUI connection mode changed")
	obj.setManual(manual)
}

func (obj *homeObj) updateManualConnect() {
	_, portErr := parsePort(obj.portEntry.Text)
	if validateIPAddress(obj.ipEntry.Text) == nil && portErr == nil {
		obj.manualConnect.Enable()
	} else {
		obj.manualConnect.Disable()
	}
}

func (obj *homeObj) showDisconnected() {
	obj.connected.Hide()
	obj.disconnected.Show()
}

func (obj *homeObj) showConnected() {
	obj.disconnected.Hide()
	obj.connected.Show()
}

func (obj *homeObj) refreshLanguage() {
	obj.automaticLabel = obj.owner.lg("home.mode.automatic", "Automatic")
	obj.manualLabel = obj.owner.lg("home.mode.manual", "Manual")
	obj.updatingMode = true
	obj.mode.Options = []string{obj.automaticLabel, obj.manualLabel}
	if obj.manual {
		obj.mode.SetSelected(obj.manualLabel)
	} else {
		obj.mode.SetSelected(obj.automaticLabel)
	}
	obj.mode.Refresh()
	obj.updatingMode = false

	obj.remoteSelect.PlaceHolder = obj.owner.lg(
		"home.automatic.placeholder",
		"Search for and select a remote",
	)
	obj.remoteSelect.Refresh()
	obj.autoConnectButton.SetText(obj.owner.lg("button.connect", "Connect"))
	obj.ipLabel.SetText(obj.owner.lg("home.manual.ip", "IP address:"))
	obj.portLabel.SetText(obj.owner.lg("home.manual.port", "Port:"))
	obj.manualConnect.SetText(obj.owner.lg("button.connect", "Connect"))
	obj.disconnectButton.SetText(obj.owner.lg("button.disconnect", "Disconnect"))
	obj.info.refreshLanguage(obj.owner.lg)
	obj.controls.refreshLanguage(obj.owner.lg)
	obj.gamepad.refreshLanguage()
	if obj.wasConnected {
		obj.updateConnectionSummary(obj.lastSnapshot)
		obj.info.update(obj.lastSnapshot, obj.owner.lg)
		obj.controls.update(obj.lastSnapshot)
	}
}
