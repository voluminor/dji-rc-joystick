package rc

import (
	"bytes"
	"context"
	"io"
	"net"
	"testing"
	"time"
)

// // // // // // // // // //

func expectKeepalive(conn net.Conn) bool {
	buf := make([]byte, keepaliveFrameLen)
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := io.ReadFull(conn, buf); err != nil {
		return false
	}
	return bytes.Equal(buf[:4], tcpMagic[:])
}

func waitForState(t *testing.T, states <-chan StateObj, match func(StateObj) bool, what string) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case state := <-states:
			if match(state) {
				return
			}
		case <-deadline:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

// //

func TestNewValidation(t *testing.T) {
	if _, err := New(ConfigObj{Port: -1}); err == nil {
		t.Fatal("negative port accepted")
	}
	if _, err := New(ConfigObj{Port: 70000}); err == nil {
		t.Fatal("out-of-range port accepted")
	}
	if _, err := New(ConfigObj{IP: "not-an-ip"}); err == nil {
		t.Fatal("invalid IP accepted")
	}
	if _, err := New(ConfigObj{ProfileID: "unknown-model"}); err == nil {
		t.Fatal("unknown profile accepted")
	}

	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	if obj.Profile().ID != defaultProfileID {
		t.Fatalf("default profile = %q", obj.Profile().ID)
	}
}

func TestNewOwnsMapping(t *testing.T) {
	mapping := DefaultMapping()
	mapping.Invert = map[AxisID]bool{AxisLeftH: false}
	obj, err := New(ConfigObj{IP: "127.0.0.1", Mapping: &mapping})
	if err != nil {
		t.Fatal(err)
	}

	mapping.Axes[AxisLeftH] = "unmapped"
	mapping.Invert[AxisLeftH] = true
	mapping.Buttons[ButtonRTH] = "unmapped"

	if obj.mapping.Axes[AxisLeftH] == "unmapped" {
		t.Fatal("axis mapping aliases caller memory")
	}
	if obj.mapping.Invert[AxisLeftH] {
		t.Fatal("invert mapping aliases caller memory")
	}
	if obj.mapping.Buttons[ButtonRTH] == "unmapped" {
		t.Fatal("button mapping aliases caller memory")
	}
}

func TestProfileReturnsIndependentSlices(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1", ProfileID: "rc2"})
	if err != nil {
		t.Fatal(err)
	}

	profile := obj.Profile()
	profile.DeviceIDs[0] = "changed"
	profile.AbsentButtons[0] = ButtonRTH

	current := obj.Profile()
	if current.DeviceIDs[0] != "rc331" {
		t.Fatalf("stored device id changed to %q", current.DeviceIDs[0])
	}
	if current.AbsentButtons[0] != ButtonCameraHalf {
		t.Fatalf("stored absent button changed to %s", current.AbsentButtons[0])
	}
}

func TestInfoReturnsAndPublishesIndependentTelemetry(t *testing.T) {
	obj := new(Obj)
	source := InfoObj{Telemetry: []TelemetryObj{{CmdSet: 6, CmdID: 0xAE}}}

	var secondCmdSet uint8
	obj.OnInfo(func(info InfoObj) {
		info.Telemetry[0].CmdSet = 0xFF
	})
	obj.OnInfo(func(info InfoObj) {
		secondCmdSet = info.Telemetry[0].CmdSet
	})
	obj.publishInfo(source)

	if secondCmdSet != 6 {
		t.Fatalf("one callback changed another callback's telemetry to %d", secondCmdSet)
	}
	current := obj.Info()
	current.Telemetry[0].CmdSet = 0xEE
	if stored := obj.Info().Telemetry[0].CmdSet; stored != 6 {
		t.Fatalf("Info() result aliases stored telemetry: %d", stored)
	}
	if source.Telemetry[0].CmdSet != 6 {
		t.Fatalf("publishInfo changed caller telemetry to %d", source.Telemetry[0].CmdSet)
	}
}

func TestNilCallbacksAndContextAreRejectedSafely(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	obj.OnState(nil)
	obj.OnStatus(nil)
	obj.OnRaw(nil)
	obj.OnPacket(nil)
	obj.OnInfo(nil)
	obj.OnLink(nil)
	if len(obj.stateCbs)+len(obj.statusCbs)+len(obj.rawCbs)+len(obj.packetCbs)+len(obj.infoCbs)+len(obj.linkCbs) != 0 {
		t.Fatal("nil callbacks were registered")
	}
	var nilContext context.Context
	if err = obj.Start(nilContext); err == nil {
		t.Fatal("Start() accepted a nil context")
	}
}

func TestStopPublishesDisconnected(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	if err = obj.Start(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	obj.Stop()

	_, status := obj.Snapshot()
	if status.Phase != PhaseDisconnected {
		t.Fatalf("phase after Stop() = %s, want disconnected", status.Phase)
	}
}

func TestCoordinatorPublishesEveryRawFrame(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan StateObj, 2)
	obj.OnState(func(state StateObj) { states <- state })

	ctx, cancel := context.WithCancel(context.Background())
	rawCh := make(chan rawSignalObj)
	done := make(chan struct{})
	coordinator := &coordinatorObj{
		obj: obj, start: time.Now(), connected: 1, info: newInfoAccumulator(),
	}
	coordinator.linkOnline[0] = true
	coordinator.linkGeneration[0] = 1
	go func() {
		coordinator.loop(ctx, rawCh, make(chan PacketObj), make(chan linkSignalObj))
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	rawCh <- rawSignalObj{raw: RawObj{}, index: 0, generation: 1}
	select {
	case <-states:
	case <-time.After(time.Second):
		t.Fatal("first raw frame was not published")
	}

	rawCh <- rawSignalObj{raw: RawObj{}, index: 0, generation: 1}
	select {
	case <-states:
	case <-time.After(50 * time.Millisecond):
		t.Fatal("second raw frame was suppressed")
	}
}

func TestCoordinatorRateLimitsPartialLinkCounts(t *testing.T) {
	obj, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	statuses := make(chan StatusObj, 4)
	obj.OnStatus(func(status StatusObj) { statuses <- status })

	ctx, cancel := context.WithCancel(context.Background())
	linkCh := make(chan linkSignalObj, 4)
	done := make(chan struct{})
	coordinator := &coordinatorObj{
		obj: obj, start: time.Now(), connected: 1, running: 3,
		lastPub: time.Now(), info: newInfoAccumulator(),
	}
	coordinator.linkOnline[0] = true
	coordinator.linkGeneration[0] = 1
	go func() {
		coordinator.loop(ctx, make(chan rawSignalObj), make(chan PacketObj), linkCh)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	linkCh <- linkSignalObj{
		delta: 1, generation: 1,
		event: LinkEventObj{Index: 1, Online: true},
	}
	linkCh <- linkSignalObj{
		delta: -1, generation: 1,
		event: LinkEventObj{Index: 1},
	}
	select {
	case status := <-statuses:
		t.Fatalf("partial link count was force-published: %+v", status)
	case <-time.After(30 * time.Millisecond):
	}

	linkCh <- linkSignalObj{
		delta: -1, generation: 1,
		event: LinkEventObj{Index: 0},
	}
	select {
	case status := <-statuses:
		if status.Phase != PhaseConnecting || status.LinksOnline != 0 || status.LinksTarget != 3 {
			t.Fatalf("unexpected all-offline status: %+v", status)
		}
	case <-time.After(time.Second):
		t.Fatal("all-offline transition was not published immediately")
	}
}

func TestPipelineDeliversStateAndKeepalive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	payload := centeredPayload()
	payload[0] = 0x20 // RTH pressed
	payload[2] = 0x01 // normal mode
	frame := buildRCFrame(1, payload)

	gotKeepalive := make(chan struct{}, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		if expectKeepalive(conn) {
			gotKeepalive <- struct{}{}
		}
		for i := 0; i < 200; i++ {
			if _, writeErr := conn.Write(frame); writeErr != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	obj, err := New(ConfigObj{IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Keepalive: KeepaliveVersion, Connections: 1})
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan StateObj, 64)
	obj.OnState(func(state StateObj) {
		select {
		case states <- state:
		default:
		}
	})

	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	if err := obj.Start(context.Background()); err == nil {
		t.Fatal("double Start accepted")
	}

	waitForState(t, states, func(state StateObj) bool {
		return state.Pressed(ButtonRTH) && state.Mode == ModeNormal
	}, "decoded state")

	select {
	case <-gotKeepalive:
	case <-time.After(3 * time.Second):
		t.Fatal("no keepalive received by the fake RC")
	}
}

func TestPipelinePublishesAggregateLinksAndDiagnostics(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	frame := buildRCFrame(1, centeredPayload())
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		for {
			if _, writeErr := conn.Write(frame); writeErr != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	obj, err := New(ConfigObj{
		IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Keepalive: KeepaliveOff,
		Connections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}

	statuses := make(chan StatusObj, 16)
	links := make(chan LinkEventObj, 16)
	obj.OnStatus(func(status StatusObj) { statuses <- status })
	obj.OnLink(func(event LinkEventObj) { links <- event })

	if err = obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	select {
	case event := <-links:
		if event.Index != 0 || !event.Online || event.Operation != linkOperationConnect || event.Error != nil {
			t.Fatalf("unexpected first link event: %+v", event)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("link connection event was not published")
	}

	deadline := time.After(3 * time.Second)
	for {
		select {
		case status := <-statuses:
			if status.Phase == PhaseConnected && status.LinksOnline == 1 && status.LinksTarget == 1 {
				return
			}
		case <-deadline:
			t.Fatal("aggregate connected status was not published")
		}
	}
}

func TestPipelineNoKeepalive(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	clientBytes := make(chan int, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		// Watch for anything the client sends; passive mode must stay silent.
		go func() {
			buf := make([]byte, 64)
			_ = conn.SetReadDeadline(time.Now().Add(1500 * time.Millisecond))
			n, _ := conn.Read(buf)
			clientBytes <- n
		}()

		frame := buildRCFrame(1, centeredPayload())
		for i := 0; i < 200; i++ {
			if _, writeErr := conn.Write(frame); writeErr != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	obj, err := New(ConfigObj{IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port, Keepalive: KeepaliveOff})
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan StateObj, 1)
	obj.OnState(func(state StateObj) {
		select {
		case states <- state:
		default:
		}
	})
	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	waitForState(t, states, func(StateObj) bool { return true }, "passive state")

	select {
	case n := <-clientBytes:
		if n != 0 {
			t.Fatalf("passive mode sent %d bytes to the RC", n)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("keepalive watch timed out")
	}
}

func TestPipelineAutoDetectsProfile(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	idFrame := buildIDFrame(1, "rc331")
	rcFrame := buildRCFrame(2, centeredPayload())
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()
		_, _ = conn.Write(idFrame)
		for i := 0; i < 200; i++ {
			if _, writeErr := conn.Write(rcFrame); writeErr != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	// Empty ProfileID: start on the default (rm330) and auto-detect.
	obj, err := New(ConfigObj{IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}
	if obj.Profile().ID != "rc" {
		t.Fatalf("initial profile = %q, want rc", obj.Profile().ID)
	}

	ids := make(chan string, 8)
	obj.OnStatus(func(status StatusObj) {
		if status.DeviceID != "" {
			select {
			case ids <- status.DeviceID:
			default:
			}
		}
	})
	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	select {
	case id := <-ids:
		if id != "rc331" {
			t.Fatalf("announced device id = %q, want rc331", id)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("device id never reported")
	}

	deadline := time.After(2 * time.Second)
	for obj.Profile().ID != "rc2" {
		select {
		case <-deadline:
			t.Fatalf("profile did not switch to rc2, still %q", obj.Profile().ID)
		case <-time.After(20 * time.Millisecond):
		}
	}
}

func TestPipelineHandshakeAnswersPoll(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	poll := buildFrame(6, 2, 0, 0, pollHeartbeatSet, pollHeartbeatID, []byte{0x04, 0x93})
	rcFrame := buildRCFrame(1, centeredPayload())

	gotResponse := make(chan []byte, 1)
	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		defer func() { _ = conn.Close() }()

		_, _ = conn.Write(poll)
		go func() {
			buf := make([]byte, 64)
			_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
			n, _ := conn.Read(buf)
			if n > 0 {
				gotResponse <- buf[:n]
			}
		}()
		for i := 0; i < 200; i++ {
			if _, writeErr := conn.Write(rcFrame); writeErr != nil {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
	}()

	obj, err := New(ConfigObj{
		IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		Keepalive: KeepaliveOff, Handshake: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	select {
	case resp := <-gotResponse:
		if len(resp) < 19 || !bytes.Equal(resp[:4], tcpMagic[:]) {
			t.Fatalf("not a DUML frame: % X", resp)
		}
		pkt := resp[8:]
		if pkt[4] != 2 || pkt[5] != 6 || pkt[9] != pollHeartbeatSet || pkt[10] != pollHeartbeatID || pkt[8]&0x80 == 0 {
			t.Fatalf("poll response malformed: % X", pkt)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no poll response sent")
	}
}

func TestPipelineMultiConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	frame := buildRCFrame(1, centeredPayload())
	go func() {
		for {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			go func(c net.Conn) {
				defer func() { _ = c.Close() }()
				for i := 0; i < 20; i++ {
					if _, wErr := c.Write(frame); wErr != nil {
						return
					}
					time.Sleep(15 * time.Millisecond)
				}
			}(conn) // short-lived: closes after ~300ms, like a recycling RC
		}
	}()

	obj, err := New(ConfigObj{
		IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port,
		Keepalive: KeepaliveOff, Connections: 3,
	})
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan StateObj, 64)
	obj.OnState(func(s StateObj) {
		select {
		case states <- s:
		default:
		}
	})

	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	// State must keep arriving across many individual link recycles.
	for i := 0; i < 5; i++ {
		select {
		case <-states:
		case <-time.After(5 * time.Second):
			t.Fatal("state stopped flowing across link recycles")
		}
	}
}

func TestPipelineReconnects(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ln.Close() }()

	first := centeredPayload()
	first[0] = 0x20 // RTH
	second := centeredPayload()
	second[1] = 0x01 // Record (0x0100)

	go func() {
		for _, payload := range [][]byte{first, second} {
			conn, acceptErr := ln.Accept()
			if acceptErr != nil {
				return
			}
			frame := buildRCFrame(1, payload)
			for i := 0; i < 30; i++ {
				if _, writeErr := conn.Write(frame); writeErr != nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			_ = conn.Close()
		}
	}()

	obj, err := New(ConfigObj{IP: "127.0.0.1", Port: ln.Addr().(*net.TCPAddr).Port})
	if err != nil {
		t.Fatal(err)
	}

	states := make(chan StateObj, 64)
	obj.OnState(func(state StateObj) {
		select {
		case states <- state:
		default:
		}
	})

	if err := obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	waitForState(t, states, func(state StateObj) bool {
		return state.Pressed(ButtonRTH)
	}, "state from the first connection")
	waitForState(t, states, func(state StateObj) bool {
		return state.Pressed(ButtonRecord)
	}, "state from the second connection after reconnect")
}
