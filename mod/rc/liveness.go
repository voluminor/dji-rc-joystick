package rc

import (
	"errors"
	"sync/atomic"
	"time"
)

// // // // // // // // // //

const frameIdleTimeout = time.Second

// Keep input fail-safe at one second, but allow the transport time to recover
// from USB/network scheduling stalls without destroying every backup socket.
const transportIdleTimeout = 3 * time.Second

var errFrameIdle = errors.New("rc: no valid controller frame received for one second")

// streamLiveness keeps quiet backup links open while any reader is delivering.
type streamLivenessObj struct {
	lastFrame atomic.Int64
}

type readerLivenessObj struct {
	generation  uint64
	connectedAt time.Time
	stream      *streamLivenessObj
}

type linkSignalObj struct {
	delta      int
	generation uint64
	event      LinkEventObj
}

type rawSignalObj struct {
	raw        RawObj
	index      int
	generation uint64
}

// //

func (obj *streamLivenessObj) observed(now time.Time) {
	obj.lastFrame.Store(now.UnixNano())
}

func (obj *streamLivenessObj) latest(fallback time.Time) time.Time {
	lastFrame := obj.lastFrame.Load()
	if lastFrame <= fallback.UnixNano() {
		return fallback
	}
	return time.Unix(0, lastFrame)
}

func (obj *readerLivenessObj) connected(now time.Time) uint64 {
	obj.generation++
	obj.connectedAt = now
	return obj.generation
}

func (obj *readerLivenessObj) observed(now time.Time) {
	obj.stream.observed(now)
}

func (obj *readerLivenessObj) expired(now time.Time) bool {
	if obj.connectedAt.IsZero() {
		return false
	}
	return now.Sub(obj.stream.latest(obj.connectedAt)) >= transportIdleTimeout
}

func (c *coordinatorObj) applyLinkSignal(signal linkSignalObj) int {
	index := signal.event.Index
	if index < 0 || index >= MaxConnections {
		return 0
	}

	switch {
	case signal.delta > 0:
		if c.linkOnline[index] {
			c.linkGeneration[index] = signal.generation
			return 0
		}
		c.linkOnline[index] = true
		c.linkGeneration[index] = signal.generation
		return 1
	case signal.delta < 0:
		if !c.linkOnline[index] ||
			c.linkGeneration[index] != signal.generation {
			return 0
		}
		c.linkOnline[index] = false
		return -1
	default:
		return 0
	}
}

func (c *coordinatorObj) acceptsRaw(signal rawSignalObj) bool {
	return signal.index >= 0 &&
		signal.index < MaxConnections &&
		c.linkOnline[signal.index] &&
		c.linkGeneration[signal.index] == signal.generation
}

func (c *coordinatorObj) neutralize() {
	if !c.stateLive {
		return
	}
	state := neutralState()
	c.obj.publishSyntheticState(state)
	// Unknown is appropriate for display, but Feed skips an unknown mode.
	// Explicitly center its output so a mode mapped to Xbox D-pad cannot stick.
	output := state
	output.Mode = ModeNormal
	if c.obj.feed(output) != nil {
		c.errCount++
	}
	c.stateLive = false
}

func neutralState() StateObj {
	return StateObj{Mode: ModeUnknown}
}
