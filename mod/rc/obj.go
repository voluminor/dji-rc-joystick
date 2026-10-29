package rc

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/voluminor/dji-rc-joystick/mod/vjoy"
)

// // // // // // // // // //

// MaxConnections is the hard limit for overlapping RC links.
const MaxConnections = 8

// KeepaliveMode selects what the pipeline sends to sustain the data stream.
type KeepaliveMode int

const (
	// KeepaliveDefault defers to the active profile's preferred mode.
	KeepaliveDefault KeepaliveMode = iota
	// KeepaliveVersion sends General Version Inquiries (the original C driver
	// behavior).
	KeepaliveVersion
	// KeepaliveHeartbeat sends General Heartbeats (what the DJI app sends).
	KeepaliveHeartbeat
	// KeepaliveOff listens passively and never sends anything.
	KeepaliveOff
)

// ConfigObj configures the pipeline. Sink is optional; the caller creates it
// (see MappingObj.DeviceSpec) and keeps ownership: it is never closed here.
// Leave it nil and call Obj.AttachSink later, or attach a stable proxy whose
// owned OS device may change while the pipeline runs.
type ConfigObj struct {
	// IP selects a remote; empty starts local-network discovery.
	IP string
	// Port selects the remote TCP port; zero uses DefaultPort.
	Port int
	// ProfileID forces a profile; empty enables announced-ID detection.
	ProfileID string
	// Sink receives mapped states and remains owned by the caller.
	Sink vjoy.Interface
	// Keepalive selects the command used to sustain each link.
	Keepalive KeepaliveMode
	// Handshake enables recurring RC poll replies in addition to profile policy.
	Handshake bool
	// QueryInfo sends one identity query (firmware/model) on the first link.
	// It requests an ack, so the remote may reset that link soon after; the
	// other links cover the gap. Passive info (name, battery, telemetry) is
	// always collected regardless.
	QueryInfo bool
	// Connections is how many overlapping links to keep. Remotes that recycle
	// the session on a fixed timer (e.g. the DJI RC 2) never stay connected on
	// a single link; several staggered links keep the merged stream gapless.
	// 0 or 1 means a single link. Capped at MaxConnections.
	Connections int
	// Mapping binds DJI controls to virtual-device actions. nil uses
	// DefaultMapping. The same mapping must build the Sink (see DeviceSpec).
	Mapping *MappingObj
}

// Obj runs the RC pipeline: discover -> connect -> parse -> decode -> publish.
type Obj struct {
	config      ConfigObj
	mapping     MappingObj // resolved control mapping, immutable after New
	autoProfile bool       // pick the profile from the announced device id

	mu        sync.Mutex
	profile   ProfileObj
	stateCbs  []func(StateObj)
	statusCbs []func(StatusObj)
	rawCbs    []func(RawObj)
	packetCbs []func(PacketObj)
	infoCbs   []func(InfoObj)
	linkCbs   []func(LinkEventObj)
	state     StateObj
	status    StatusObj
	info      InfoObj

	sinkMu sync.RWMutex
	sink   vjoy.Interface

	runMu  sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

// //

// New validates the config and prepares the pipeline; nothing runs yet.
func New(config ConfigObj) (*Obj, error) {
	port, err := normalizePort(config.Port)
	if err != nil {
		return nil, err
	}
	config.Port = port

	if config.IP != "" && net.ParseIP(config.IP) == nil {
		return nil, fmt.Errorf("rc: invalid IP address %q", config.IP)
	}

	// Empty profile id: start on the default and auto-detect from the remote.
	autoProfile := config.ProfileID == ""
	profile := DefaultProfile()
	if !autoProfile {
		resolved, ok := ProfileByID(config.ProfileID)
		if !ok {
			return nil, fmt.Errorf("rc: unknown profile %q", config.ProfileID)
		}
		profile = resolved
	}

	mapping := DefaultMapping()
	if config.Mapping != nil {
		if err := config.Mapping.Validate(); err != nil {
			return nil, fmt.Errorf("rc: %w", err)
		}
		mapping = cloneMapping(*config.Mapping)
	} else if err := mapping.Validate(); err != nil {
		return nil, fmt.Errorf("rc: %w", err)
	}
	config.Mapping = nil
	sink := config.Sink
	config.Sink = nil

	return &Obj{
		config:      config,
		mapping:     mapping,
		autoProfile: autoProfile,
		sink:        sink,
		profile:     profile,
		status:      StatusObj{Phase: PhaseDisconnected, IP: config.IP},
	}, nil
}

// AttachSink replaces the active sink; nil detaches it. On return, no feed is
// using the old sink. The caller retains ownership of both sinks.
func (obj *Obj) AttachSink(sink vjoy.Interface) {
	obj.sinkMu.Lock()
	obj.sink = sink
	obj.sinkMu.Unlock()
}

// Profile returns the active remote profile (may change after auto-detection).
func (obj *Obj) Profile() ProfileObj {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return cloneProfile(obj.profile)
}

// OnState registers a synchronous decoded-state listener. A nil callback is
// ignored; a slow callback applies backpressure to the entire pipeline.
func (obj *Obj) OnState(cb func(StateObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.stateCbs = append(obj.stateCbs, cb)
	obj.mu.Unlock()
}

// OnStatus registers a synchronous health listener. A nil callback is ignored.
func (obj *Obj) OnStatus(cb func(StatusObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.statusCbs = append(obj.statusCbs, cb)
	obj.mu.Unlock()
}

// OnRaw registers a synchronous raw-channel listener for diagnostics and
// mapping work. A nil callback is ignored.
func (obj *Obj) OnRaw(cb func(RawObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.rawCbs = append(obj.rawCbs, cb)
	obj.mu.Unlock()
}

// OnPacket registers a synchronous listener for bounded DUML packet
// diagnostics. A nil callback is ignored.
func (obj *Obj) OnPacket(cb func(PacketObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.packetCbs = append(obj.packetCbs, cb)
	obj.mu.Unlock()
}

// OnInfo registers a synchronous listener for independent device-info
// snapshots. A nil callback is ignored.
func (obj *Obj) OnInfo(cb func(InfoObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.infoCbs = append(obj.infoCbs, cb)
	obj.mu.Unlock()
}

// OnLink registers a synchronous per-link diagnostic listener. Callbacks run
// on the coordinator goroutine; routine consumers should prefer OnStatus.
func (obj *Obj) OnLink(cb func(LinkEventObj)) {
	if cb == nil {
		return
	}
	obj.mu.Lock()
	obj.linkCbs = append(obj.linkCbs, cb)
	obj.mu.Unlock()
}

// Info returns an independent copy of the latest device information.
func (obj *Obj) Info() InfoObj {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return cloneInfo(obj.info)
}

// Snapshot returns the latest state and status as independent values.
func (obj *Obj) Snapshot() (StateObj, StatusObj) {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.state, obj.status
}

// Start launches the pipeline and rejects a nil context or a concurrent run.
func (obj *Obj) Start(ctx context.Context) error {
	if ctx == nil {
		return errors.New("rc: context is nil")
	}

	obj.runMu.Lock()
	defer obj.runMu.Unlock()

	if obj.done != nil {
		select {
		case <-obj.done:
		default:
			return errors.New("rc: already started")
		}
	}

	runCtx, cancel := context.WithCancel(ctx)
	obj.cancel = cancel
	obj.done = make(chan struct{})
	go obj.run(runCtx, obj.done)
	return nil
}

// Stop cancels the pipeline and waits for it to finish. Idempotent.
func (obj *Obj) Stop() {
	obj.runMu.Lock()
	cancel, done := obj.cancel, obj.done
	obj.runMu.Unlock()

	if cancel != nil {
		cancel()
	}
	if done != nil {
		<-done
	}
}
