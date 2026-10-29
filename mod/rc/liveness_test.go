package rc

import (
	"context"
	"net"
	"sync"
	"testing"
	"time"
)

// // // // // // // // // //

const livenessTestTimeout = 4 * time.Second

type synchronizedSinkObj struct {
	mu     sync.Mutex
	values map[uint16]int32
}

type coordinatorHarnessObj struct {
	obj    *Obj
	states chan StateObj
	rawCh  chan rawSignalObj
	linkCh chan linkSignalObj
	cancel context.CancelFunc
	done   chan struct{}
}

// //

func newSynchronizedSink() *synchronizedSinkObj {
	return &synchronizedSinkObj{values: make(map[uint16]int32)}
}

func (obj *synchronizedSinkObj) Set(handle uint16, value int32) error {
	obj.mu.Lock()
	obj.values[handle] = value
	obj.mu.Unlock()
	return nil
}

func (obj *synchronizedSinkObj) Sync() error {
	return nil
}

func (obj *synchronizedSinkObj) Close() error {
	return nil
}

func (obj *synchronizedSinkObj) value(handle uint16) int32 {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.values[handle]
}

func newCoordinatorHarness(t *testing.T) *coordinatorHarnessObj {
	t.Helper()
	controller, err := New(ConfigObj{IP: "127.0.0.1"})
	if err != nil {
		t.Fatal(err)
	}
	harness := &coordinatorHarnessObj{
		obj:    controller,
		states: make(chan StateObj, 8),
		rawCh:  make(chan rawSignalObj, 8),
		linkCh: make(chan linkSignalObj),
		done:   make(chan struct{}),
	}
	harness.obj.OnState(func(state StateObj) {
		harness.states <- state
	})

	ctx, cancel := context.WithCancel(context.Background())
	harness.cancel = cancel
	coordinator := &coordinatorObj{
		obj: controller, start: time.Now(), info: newInfoAccumulator(),
	}
	go func() {
		coordinator.loop(
			ctx,
			harness.rawCh,
			make(chan PacketObj),
			harness.linkCh,
		)
		close(harness.done)
	}()
	t.Cleanup(harness.stop)
	return harness
}

func (obj *coordinatorHarnessObj) stop() {
	obj.cancel()
	<-obj.done
}

func waitForStatus(
	t *testing.T,
	statuses <-chan StatusObj,
	match func(StatusObj) bool,
	what string,
) StatusObj {
	t.Helper()
	timer := time.NewTimer(livenessTestTimeout)
	defer timer.Stop()
	for {
		select {
		case status := <-statuses:
			if match(status) {
				return status
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func waitForLivenessState(
	t *testing.T,
	states <-chan StateObj,
	match func(StateObj) bool,
	what string,
) StateObj {
	t.Helper()
	timer := time.NewTimer(livenessTestTimeout)
	defer timer.Stop()
	for {
		select {
		case state := <-states:
			if match(state) {
				return state
			}
		case <-timer.C:
			t.Fatalf("timed out waiting for %s", what)
		}
	}
}

func serveSilentConnections(
	listener net.Listener,
	frame []byte,
	accepted chan<- int,
	stop <-chan struct{},
) {
	count := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		count++
		select {
		case accepted <- count:
		case <-stop:
			_ = conn.Close()
			return
		}
		go func(index int, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			if index == 1 {
				for range 10 {
					if _, writeErr := connection.Write(frame); writeErr != nil {
						return
					}
					time.Sleep(20 * time.Millisecond)
				}
			}
			<-stop
		}(count, conn)
	}
}

func serveLiveAndSilentConnections(
	listener net.Listener,
	frame []byte,
	accepted chan<- int,
	stop <-chan struct{},
) {
	count := 0
	for {
		conn, err := listener.Accept()
		if err != nil {
			return
		}
		count++
		select {
		case accepted <- count:
		case <-stop:
			_ = conn.Close()
			return
		}
		go func(index int, connection net.Conn) {
			defer func() { _ = connection.Close() }()
			if index != 1 {
				<-stop
				return
			}

			ticker := time.NewTicker(20 * time.Millisecond)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					if _, writeErr := connection.Write(frame); writeErr != nil {
						return
					}
				case <-stop:
					return
				}
			}
		}(count, conn)
	}
}

func livenessRaw(axisValue int) RawObj {
	payload := centeredPayload()
	setAxisRaw(payload, 5, axisValue)
	return rawFromPayload(payload)
}

// //

func TestReaderPublishesBufferedFrameBeforeDisconnect(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = listener.Close() }()

	frame := buildRCFrame(1, centeredPayload())
	go func() {
		conn, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		_, _ = conn.Write(frame)
		_ = conn.Close()
	}()

	obj, err := New(ConfigObj{
		IP:        "127.0.0.1",
		Port:      listener.Addr().(*net.TCPAddr).Port,
		Keepalive: KeepaliveOff,
		QueryInfo: false,
	})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	rawCh := make(chan rawSignalObj)
	linkCh := make(chan linkSignalObj)
	done := make(chan struct{})
	go func() {
		obj.reader(
			ctx,
			0,
			"127.0.0.1",
			0,
			new(streamLivenessObj),
			rawCh,
			make(chan PacketObj, 1),
			linkCh,
		)
		close(done)
	}()
	defer func() {
		cancel()
		<-done
	}()

	select {
	case event := <-linkCh:
		if event.delta != 1 {
			t.Fatalf("first link delta = %d, want 1", event.delta)
		}
	case <-time.After(livenessTestTimeout):
		t.Fatal("timed out waiting for the link to connect")
	}

	select {
	case <-rawCh:
	case event := <-linkCh:
		t.Fatalf("link delta %d was published before the buffered frame", event.delta)
	case <-time.After(livenessTestTimeout):
		t.Fatal("timed out waiting for the buffered frame")
	}
}

func TestPipelineTimesOutHalfOpenLinkAndNeutralizes(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stopServer := make(chan struct{})
	accepted := make(chan int, 8)
	payload := centeredPayload()
	setAxisRaw(payload, 5, axisRange)
	payload[0] = 0x20
	frame := buildRCFrame(1, payload)
	go serveSilentConnections(listener, frame, accepted, stopServer)
	defer func() {
		close(stopServer)
		_ = listener.Close()
	}()

	sink := newSynchronizedSink()
	obj, err := New(ConfigObj{
		IP:          "127.0.0.1",
		Port:        listener.Addr().(*net.TCPAddr).Port,
		Keepalive:   KeepaliveOff,
		Sink:        sink,
		Connections: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	states := make(chan StateObj, 32)
	statuses := make(chan StatusObj, 32)
	obj.OnState(func(state StateObj) {
		states <- state
	})
	obj.OnStatus(func(status StatusObj) {
		statuses <- status
	})
	if err = obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	waitForLivenessState(t, states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == outMax && state.Pressed(ButtonRTH)
	}, "initial deflected state")
	waitForStatus(t, statuses, func(status StatusObj) bool {
		return status.LinksOnline == 1
	}, "online link")

	neutral := waitForLivenessState(t, states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == 0 &&
			!state.Pressed(ButtonRTH) &&
			state.Mode == ModeUnknown
	}, "neutral state after the link became silent")
	if neutral != (StateObj{Mode: ModeUnknown}) {
		t.Fatalf("neutral state = %+v", neutral)
	}
	waitForStatus(t, statuses, func(status StatusObj) bool {
		return status.LinksOnline == 0
	}, "all links offline")
	if value := sink.value(axisHandle(AxisLeftH)); value != 0 {
		t.Fatalf("neutral sink left-h = %d, want 0", value)
	}
	if value := sink.value(buttonHandle(ButtonRTH)); value != 0 {
		t.Fatalf("neutral sink RTH = %d, want 0", value)
	}
	if value := sink.value(handleMode); value != int32(ModeNormal) {
		t.Fatalf("neutral sink mode = %d, want centered mode", value)
	}

	timer := time.NewTimer(livenessTestTimeout)
	defer timer.Stop()
	for {
		select {
		case count := <-accepted:
			if count >= 2 {
				return
			}
		case <-timer.C:
			t.Fatal("timed out waiting for a reconnect attempt")
		}
	}
}

func TestPipelineKeepsSilentBackupWhileStreamIsLive(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	stopServer := make(chan struct{})
	accepted := make(chan int, 8)
	frame := buildRCFrame(1, centeredPayload())
	go serveLiveAndSilentConnections(listener, frame, accepted, stopServer)
	defer func() {
		close(stopServer)
		_ = listener.Close()
	}()

	obj, err := New(ConfigObj{
		IP:          "127.0.0.1",
		Port:        listener.Addr().(*net.TCPAddr).Port,
		ProfileID:   "rc2",
		Keepalive:   KeepaliveOff,
		Connections: 2,
		QueryInfo:   false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = obj.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer obj.Stop()

	for count := 1; count <= 2; count++ {
		select {
		case got := <-accepted:
			if got != count {
				t.Fatalf("accepted connection = %d, want %d", got, count)
			}
		case <-time.After(livenessTestTimeout):
			t.Fatalf("timed out waiting for connection %d", count)
		}
	}

	select {
	case count := <-accepted:
		t.Fatalf("silent backup was reconnected while another link was live: connection %d", count)
	case <-time.After(frameIdleTimeout + 250*time.Millisecond):
	}
}

func TestCoordinatorRejectsExpiredSessionFrames(t *testing.T) {
	harness := newCoordinatorHarness(t)

	harness.linkCh <- linkSignalObj{
		delta: 1, generation: 1,
		event: LinkEventObj{Index: 0, Online: true},
	}
	harness.rawCh <- rawSignalObj{
		index: 0, generation: 1, raw: livenessRaw(axisRange),
	}
	waitForLivenessState(t, harness.states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == outMax
	}, "state from the first reader session")

	harness.linkCh <- linkSignalObj{
		delta: -1, generation: 1,
		event: LinkEventObj{Index: 0},
	}
	waitForLivenessState(t, harness.states, func(state StateObj) bool {
		return state == neutralState()
	}, "neutral state after the first reader session")
	harness.linkCh <- linkSignalObj{
		delta: 1, generation: 2,
		event: LinkEventObj{Index: 0, Online: true},
	}
	harness.rawCh <- rawSignalObj{
		index: 0, generation: 1, raw: livenessRaw(axisRange),
	}
	select {
	case state := <-harness.states:
		t.Fatalf("expired session state was published: %+v", state)
	case <-time.After(100 * time.Millisecond):
	}

	harness.rawCh <- rawSignalObj{
		index: 0, generation: 2, raw: livenessRaw(-axisRange),
	}
	waitForLivenessState(t, harness.states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == -outMax
	}, "state from the replacement reader session")
}

func TestCoordinatorKeepsStateUntilStreamTimeout(t *testing.T) {
	harness := newCoordinatorHarness(t)
	statuses := make(chan StatusObj, 16)
	harness.obj.OnStatus(func(status StatusObj) {
		statuses <- status
	})

	for index := range 2 {
		harness.linkCh <- linkSignalObj{
			delta: 1, generation: 1,
			event: LinkEventObj{Index: index, Online: true},
		}
	}
	harness.rawCh <- rawSignalObj{
		index: 0, generation: 1, raw: livenessRaw(axisRange),
	}
	waitForLivenessState(t, harness.states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == outMax
	}, "state with two online links")
	waitForStatus(t, statuses, func(status StatusObj) bool {
		return status.Phase == PhaseConnected && status.LinksOnline == 2
	}, "live aggregate stream")

	harness.linkCh <- linkSignalObj{
		delta: -1, generation: 1,
		event: LinkEventObj{Index: 0},
	}
	select {
	case state := <-harness.states:
		t.Fatalf("partial link loss changed state: %+v", state)
	case <-time.After(100 * time.Millisecond):
	}

	harness.linkCh <- linkSignalObj{
		delta: -1, generation: 1,
		event: LinkEventObj{Index: 1},
	}
	waitForStatus(t, statuses, func(status StatusObj) bool {
		return status.Phase == PhaseConnected && status.LinksOnline == 0
	}, "live stream during a brief total link gap")
	select {
	case state := <-harness.states:
		t.Fatalf("brief total link loss changed state: %+v", state)
	case <-time.After(100 * time.Millisecond):
	}
	waitForLivenessState(t, harness.states, func(state StateObj) bool {
		return state == neutralState()
	}, "neutral state after the aggregate stream timed out")
	waitForStatus(t, statuses, func(status StatusObj) bool {
		return status.Phase == PhaseConnecting && status.LinksOnline == 0
	}, "reconnecting phase after the aggregate stream timed out")
}

func TestCoordinatorNeutralizesOnCancellation(t *testing.T) {
	sink := newSynchronizedSink()
	obj, err := New(ConfigObj{IP: "127.0.0.1", Sink: sink})
	if err != nil {
		t.Fatal(err)
	}
	states := make(chan StateObj, 8)
	obj.OnState(func(state StateObj) {
		states <- state
	})

	ctx, cancel := context.WithCancel(context.Background())
	rawCh := make(chan rawSignalObj)
	done := make(chan struct{})
	coordinator := &coordinatorObj{
		obj: obj, start: time.Now(), connected: 1, info: newInfoAccumulator(),
	}
	coordinator.linkOnline[0] = true
	coordinator.linkGeneration[0] = 1
	go func() {
		coordinator.loop(
			ctx,
			rawCh,
			make(chan PacketObj),
			make(chan linkSignalObj),
		)
		close(done)
	}()

	rawCh <- rawSignalObj{
		index: 0, generation: 1, raw: livenessRaw(axisRange),
	}
	waitForLivenessState(t, states, func(state StateObj) bool {
		return state.Axes[AxisLeftH] == outMax
	}, "active state before cancellation")
	cancel()
	<-done

	state, _ := obj.Snapshot()
	if state != neutralState() {
		t.Fatalf("state after cancellation = %+v", state)
	}
	if value := sink.value(axisHandle(AxisLeftH)); value != 0 {
		t.Fatalf("sink left-h after cancellation = %d, want 0", value)
	}
}
