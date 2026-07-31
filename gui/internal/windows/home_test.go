package windows

import (
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/voluminor/dji-rc-joystick/gui/internal/gamepad"
	guiremote "github.com/voluminor/dji-rc-joystick/gui/internal/remote"
	apptheme "github.com/voluminor/dji-rc-joystick/gui/internal/theme"
	"github.com/voluminor/dji-rc-joystick/mod/data"
	"github.com/voluminor/dji-rc-joystick/mod/rc"

	"fyne.io/fyne/v2"
	fynetest "fyne.io/fyne/v2/test"
	"fyne.io/fyne/v2/widget"
	"github.com/rs/zerolog"
)

// // // // // // // // // //

type fakeRemoteObj struct {
	mu            sync.Mutex
	snapshot      guiremote.SnapshotObj
	updates       chan struct{}
	searches      atomic.Int64
	accepts       atomic.Int64
	connects      atomic.Int64
	stops         atomic.Int64
	gamepadStarts atomic.Int64
	gamepadStops  atomic.Int64
}

// //

func newFakeRemote() *fakeRemoteObj {
	return &fakeRemoteObj{
		snapshot: guiremote.SnapshotObj{Phase: guiremote.PhaseIdle},
		updates:  make(chan struct{}, 1),
	}
}

func (obj *fakeRemoteObj) Search() error {
	obj.searches.Add(1)
	obj.setSnapshot(guiremote.SnapshotObj{Phase: guiremote.PhaseSearching})
	return nil
}

func (obj *fakeRemoteObj) AcceptFound() error {
	obj.accepts.Add(1)
	obj.setSnapshot(guiremote.SnapshotObj{
		Phase:          guiremote.PhaseIdle,
		SearchRevision: 1,
		SearchCount:    1,
		Remotes:        []rc.RemoteObj{{IP: "192.168.7.2"}},
	})
	return nil
}

func (obj *fakeRemoteObj) Connect(ip string, port int) error {
	obj.connects.Add(1)
	obj.setSnapshot(guiremote.SnapshotObj{
		Phase:      guiremote.PhaseConnecting,
		TargetIP:   ip,
		TargetPort: port,
	})
	return nil
}

func (obj *fakeRemoteObj) Stop() {
	obj.stops.Add(1)
	obj.setSnapshot(guiremote.SnapshotObj{Phase: guiremote.PhaseIdle})
}

func (obj *fakeRemoteObj) StartGamepad(rc.MappingObj) error {
	obj.gamepadStarts.Add(1)
	snapshot := obj.Snapshot()
	snapshot.Gamepad.Phase = gamepad.PhaseRunning
	obj.setSnapshot(snapshot)
	return nil
}

func (obj *fakeRemoteObj) StopGamepad() {
	obj.gamepadStops.Add(1)
	snapshot := obj.Snapshot()
	snapshot.Gamepad.Phase = gamepad.PhaseStopped
	obj.setSnapshot(snapshot)
}

func (obj *fakeRemoteObj) Updates() <-chan struct{} {
	return obj.updates
}

func (obj *fakeRemoteObj) Snapshot() guiremote.SnapshotObj {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.snapshot
}

func (obj *fakeRemoteObj) setSnapshot(snapshot guiremote.SnapshotObj) {
	obj.mu.Lock()
	snapshot.Revision = obj.snapshot.Revision + 1
	obj.snapshot = snapshot
	obj.mu.Unlock()
	select {
	case obj.updates <- struct{}{}:
	default:
	}
}

func newTestWindow(t *testing.T, remoteObj remoteInterface) (*Obj, fyne.App) {
	t.Helper()
	application := fynetest.NewApp()
	configPath := filepath.Join(t.TempDir(), "settings.json")
	dataObj, err := data.New([]string{"--config", configPath})
	if err != nil {
		t.Fatal(err)
	}
	themeObj := apptheme.New(dataObj.UI())
	windowObj := newWithRemote(
		application,
		zerolog.Nop(),
		dataObj,
		themeObj,
		fyne.NewStaticResource("icon.png", nil),
		remoteObj,
	)
	t.Cleanup(func() {
		windowObj.home.stop()
		remoteObj.Stop()
		windowObj.window.SetCloseIntercept(nil)
		windowObj.window.Close()
		application.Quit()
	})
	return windowObj, application
}

func applyHomeSnapshot(home *homeObj, snapshot guiremote.SnapshotObj) {
	fyne.DoAndWait(func() {
		home.applySnapshot(snapshot)
	})
}

// //

func TestManualTargetValidation(t *testing.T) {
	if err := validateIPAddress("192.168.7.2"); err != nil {
		t.Fatalf("validateIPAddress() error = %v", err)
	}
	if err := validateIPAddress("controller"); err == nil {
		t.Fatal("validateIPAddress() error = nil")
	}

	port, err := parsePort("40007")
	if err != nil || port != rc.DefaultPort {
		t.Fatalf("parsePort() = %d, %v", port, err)
	}
	for _, value := range []string{"", "0", "65536", "port"} {
		if _, err = parsePort(value); err == nil {
			t.Fatalf("parsePort(%q) error = nil", value)
		}
	}
}

func TestFormatRemoteOptionOmitsMissingDetails(t *testing.T) {
	tests := []struct {
		name   string
		remote rc.RemoteObj
		want   string
	}{
		{
			name: "all fields",
			remote: rc.RemoteObj{
				IP:       "192.168.7.2",
				Model:    "DJI RC 2",
				Firmware: "01.02.03.04",
			},
			want: "192.168.7.2 — DJI RC 2 — 01.02.03.04",
		},
		{
			name:   "IP only",
			remote: rc.RemoteObj{IP: "192.168.7.2"},
			want:   "192.168.7.2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatRemoteOption(test.remote); got != test.want {
				t.Fatalf("formatRemoteOption() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestFormatRemoteEndpoint(t *testing.T) {
	tests := []struct {
		name     string
		snapshot guiremote.SnapshotObj
		want     string
	}{
		{
			name: "status IPv4",
			snapshot: guiremote.SnapshotObj{
				TargetIP:   "192.168.7.1",
				TargetPort: rc.DefaultPort,
				Status:     rc.StatusObj{IP: "192.168.7.2"},
			},
			want: "192.168.7.2:40007",
		},
		{
			name: "target IPv6",
			snapshot: guiremote.SnapshotObj{
				TargetIP:   "2001:db8::2",
				TargetPort: rc.DefaultPort,
			},
			want: "[2001:db8::2]:40007",
		},
		{
			name:     "missing address",
			snapshot: guiremote.SnapshotObj{TargetPort: rc.DefaultPort},
			want:     "",
		},
		{
			name: "missing port",
			snapshot: guiremote.SnapshotObj{
				Status: rc.StatusObj{IP: "192.168.7.2"},
			},
			want: "192.168.7.2",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := formatRemoteEndpoint(test.snapshot); got != test.want {
				t.Fatalf("formatRemoteEndpoint() = %q, want %q", got, test.want)
			}
		})
	}
}

func TestHomeSwitchesModesAndValidatesManualConnect(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home

	if home.searchButton.Disabled() {
		t.Fatal("search button is disabled in automatic mode")
	}
	if home.searchButton.Importance != widget.HighImportance {
		t.Fatalf("search button importance = %d", home.searchButton.Importance)
	}

	fyne.DoAndWait(func() {
		home.mode.SetSelected(home.manualLabel)
	})
	if !home.manual || !home.automatic.Hidden || home.manualForm.Hidden {
		t.Fatalf(
			"manual mode state: manual=%t automaticHidden=%t manualHidden=%t",
			home.manual,
			home.automatic.Hidden,
			home.manualForm.Hidden,
		)
	}
	if !home.searchButton.Disabled() {
		t.Fatal("search button is enabled in manual mode")
	}
	if home.ipLabel.Alignment != fyne.TextAlignTrailing ||
		home.portLabel.Alignment != fyne.TextAlignTrailing {
		t.Fatal("manual labels are not right-aligned")
	}

	fyne.DoAndWait(func() {
		home.ipEntry.SetText("controller")
		home.portEntry.SetText("40007")
	})
	if !home.manualConnect.Disabled() {
		t.Fatal("manual connect is enabled for an invalid IP")
	}

	fyne.DoAndWait(func() {
		home.ipEntry.SetText("192.168.7.2")
	})
	if home.manualConnect.Disabled() {
		t.Fatal("manual connect is disabled for a valid target")
	}

	fyne.DoAndWait(func() {
		home.mode.SetSelected(home.automaticLabel)
	})
	if home.searchButton.Disabled() {
		t.Fatal("search button stayed disabled in automatic mode")
	}
}

func TestHomeBuildsConnectedControllerBlock(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home

	if len(home.connected.Objects) != 2 {
		t.Fatalf("connected blocks = %d, want 2", len(home.connected.Objects))
	}
	card, ok := home.connected.Objects[0].(*widget.Card)
	if !ok {
		t.Fatalf("connected block type = %T, want *widget.Card", home.connected.Objects[0])
	}
	content, ok := card.Content.(*fyne.Container)
	if !ok {
		t.Fatalf("controller block content type = %T, want *fyne.Container", card.Content)
	}
	if len(content.Objects) != 3 {
		t.Fatalf("controller block objects = %d, want toolbar, separator, and columns", len(content.Objects))
	}
	columns, ok := content.Objects[2].(*fyne.Container)
	if !ok {
		t.Fatalf("controller columns type = %T, want *fyne.Container", content.Objects[2])
	}
	if len(columns.Objects) != 2 ||
		columns.Objects[0] != home.info.root ||
		columns.Objects[1] != home.controls.root {
		t.Fatal("controller block does not contain the data and controls columns")
	}
	if home.connected.Objects[1] != home.gamepad.root {
		t.Fatal("connected view does not contain the virtual gamepad block")
	}
	if _, ok = home.info.rows["home.info.status"]; ok {
		t.Fatal("connection status is duplicated in the data table")
	}
	for key, row := range home.info.rows {
		if row.value.Wrapping != fyne.TextWrapBreak {
			t.Fatalf("row %q does not break long remote values", key)
		}
	}

	snapshot := guiremote.SnapshotObj{
		Phase:      guiremote.PhaseConnected,
		TargetIP:   "192.168.7.2",
		TargetPort: rc.DefaultPort,
		Status: rc.StatusObj{
			Phase: rc.PhaseConnected,
			IP:    "192.168.7.2",
		},
		Profile: rc.DefaultProfile(),
	}
	applyHomeSnapshot(home, snapshot)
	if home.connectionStatus.Text != "Connected" {
		t.Fatalf("connection status = %q, want Connected", home.connectionStatus.Text)
	}
	if home.connectionStatus.Importance != widget.SuccessImportance {
		t.Fatalf("connection status importance = %d, want success", home.connectionStatus.Importance)
	}
	if home.connectionEndpoint.Text != "192.168.7.2:40007" {
		t.Fatalf("connection endpoint = %q", home.connectionEndpoint.Text)
	}

	ui := windowObj.data.UI()
	ui.Language = "ru-RU"
	if err := windowObj.data.SaveUI(ui); err != nil {
		t.Fatal(err)
	}
	fyne.DoAndWait(home.refreshLanguage)
	if home.connectionStatus.Text != "Подключён" {
		t.Fatalf("localized connection status = %q, want Подключён", home.connectionStatus.Text)
	}
}

func TestHomeRefreshesEverySupportedLanguage(t *testing.T) {
	windowObj, _ := newTestWindow(t, newFakeRemote())
	home := windowObj.home

	for _, option := range windowObj.data.Language().List() {
		ui := windowObj.data.UI()
		ui.Language = option.Key
		if err := windowObj.data.SaveUI(ui); err != nil {
			t.Fatalf("SaveUI(%q) error = %v", option.Key, err)
		}

		fyne.DoAndWait(home.refreshLanguage)
		wantMode := windowObj.data.Language().Get("home.mode.automatic", "")
		if len(home.mode.Options) == 0 || home.mode.Options[0] != wantMode {
			t.Errorf("automatic mode for %q = %q, want %q", option.Key, home.mode.Options, wantMode)
		}
		wantStick := windowObj.data.Language().Get("home.controls.left_stick", "")
		if home.controls.stickTitles[leftControl].Text != wantStick {
			t.Errorf(
				"left stick for %q = %q, want %q",
				option.Key,
				home.controls.stickTitles[leftControl].Text,
				wantStick,
			)
		}
		wantGamepad := windowObj.data.Language().Get("home.gamepad.title", "")
		if home.gamepad.title.Text != wantGamepad {
			t.Errorf("gamepad title for %q = %q, want %q", option.Key, home.gamepad.title.Text, wantGamepad)
		}
	}
}

func TestHomeAppliesCompletedDiscoveryResults(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home
	if !home.automaticResults.Hidden {
		t.Fatal("automatic results are visible before completed discovery")
	}
	snapshot := guiremote.SnapshotObj{
		Phase:          guiremote.PhaseIdle,
		SearchRevision: 1,
		SearchCount:    1,
		Remotes: []rc.RemoteObj{{
			IP:       "192.168.7.2",
			Model:    "DJI RC 2",
			Firmware: "01.02.03.04",
		}},
	}

	applyHomeSnapshot(home, snapshot)
	if len(home.remoteSelect.Options) != 1 {
		t.Fatalf("remote options = %#v", home.remoteSelect.Options)
	}
	if home.remoteSelect.Selected != "192.168.7.2 — DJI RC 2 — 01.02.03.04" {
		t.Fatalf("selected remote = %q", home.remoteSelect.Selected)
	}
	if home.autoConnectButton.Disabled() {
		t.Fatal("automatic connect is disabled after result selection")
	}
	if home.automaticResults.Hidden {
		t.Fatal("automatic results are hidden after completed discovery")
	}
	if len(home.automaticResults.Objects) != 2 {
		t.Fatalf(
			"automatic result objects = %d, want select and connect button",
			len(home.automaticResults.Objects),
		)
	}
}

func TestSearchDialogOffersPartialResults(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home

	searching := guiremote.SnapshotObj{
		Phase: guiremote.PhaseSearching,
		Progress: rc.DiscoverProgressObj{
			Known: true,
			Total: 4,
		},
	}
	applyHomeSnapshot(home, searching)
	if home.modal == nil || !home.modal.acceptButton.Hidden {
		t.Fatal("partial-result button is visible before a remote is found")
	}

	searching.Progress.Probed = 1
	searching.Progress.Found = 1
	applyHomeSnapshot(home, searching)
	if home.modal.acceptButton.Hidden {
		t.Fatal("partial-result button is hidden after a remote is found")
	}
}

func TestHomeReusesRuntimeAndViewAcrossTabRefresh(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home
	root := home.root
	snapshot := guiremote.SnapshotObj{
		Phase:      guiremote.PhaseConnected,
		TargetIP:   "192.168.7.2",
		TargetPort: rc.DefaultPort,
		Status: rc.StatusObj{
			Phase:       rc.PhaseConnected,
			IP:          "192.168.7.2",
			LinksOnline: 1,
			LinksTarget: 1,
		},
		Profile: rc.DefaultProfile(),
	}

	applyHomeSnapshot(home, snapshot)
	fyne.DoAndWait(func() {
		windowObj.tabs.SelectIndex(1)
		windowObj.refreshTabs(1)
	})

	if windowObj.home != home || windowObj.renderHome() != root {
		t.Fatal("Home view was replaced during tab refresh")
	}
	if windowObj.tabs.SelectedIndex() != 1 {
		t.Fatalf("selected tab = %d", windowObj.tabs.SelectedIndex())
	}
	if remoteObj.stops.Load() != 0 {
		t.Fatalf("remote stops during refresh = %d", remoteObj.stops.Load())
	}
	if home.connected.Hidden {
		t.Fatal("connected view was hidden during tab refresh")
	}
}

func TestHomeConnectionAndReconnectDialogs(t *testing.T) {
	remoteObj := newFakeRemote()
	windowObj, _ := newTestWindow(t, remoteObj)
	home := windowObj.home

	applyHomeSnapshot(home, guiremote.SnapshotObj{
		Phase:      guiremote.PhaseConnecting,
		TargetIP:   "192.168.7.2",
		TargetPort: rc.DefaultPort,
	})
	if home.modal == nil || home.modal.phase != guiremote.PhaseConnecting {
		t.Fatal("initial connection dialog is not visible")
	}

	connected := guiremote.SnapshotObj{
		Phase:      guiremote.PhaseConnected,
		TargetIP:   "192.168.7.2",
		TargetPort: rc.DefaultPort,
		Status: rc.StatusObj{
			Phase:       rc.PhaseConnected,
			IP:          "192.168.7.2",
			LinksOnline: 1,
			LinksTarget: 5,
		},
		Profile: rc.DefaultProfile(),
	}
	applyHomeSnapshot(home, connected)
	if home.modal != nil || home.connected.Hidden {
		t.Fatal("connected state did not close the progress dialog")
	}

	connected.Phase = guiremote.PhaseReconnecting
	connected.Status.Phase = rc.PhaseConnecting
	connected.Status.LinksOnline = 0
	applyHomeSnapshot(home, connected)
	if home.modal == nil || home.modal.phase != guiremote.PhaseReconnecting {
		t.Fatal("reconnection dialog is not visible")
	}
	if home.connectionStatus.Text != "Reconnecting" ||
		home.connectionStatus.Importance != widget.WarningImportance {
		t.Fatalf(
			"reconnection status = %q / %d",
			home.connectionStatus.Text,
			home.connectionStatus.Importance,
		)
	}
}
