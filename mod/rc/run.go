package rc

import (
	"context"
	"sync"
	"time"
)

// // // // // // // // // //

const (
	streamBufSize       = 8192
	streamHeadroom      = 512
	keepaliveIdleCycles = 2
	drainAttempts       = 3
	discoverRetryDelay  = time.Second
	statusMinInterval   = time.Second

	linkOperationConnect    = "connect"
	linkOperationRead       = "read"
	linkOperationWrite      = "write"
	linkOperationDisconnect = "disconnect"

	// sessionBudget is how long an unauthenticated link tends to survive
	// before the remote recycles it; overlapping links are staggered across
	// it so their reconnect windows do not align.
	sessionBudget = 2500 * time.Millisecond
)

// //

func sleepCtx(ctx context.Context, d time.Duration) {
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}

func stopTimer(timer *time.Timer) {
	if timer.Stop() {
		return
	}
	select {
	case <-timer.C:
	default:
	}
}

func resetTimer(timer *time.Timer, duration time.Duration) {
	stopTimer(timer)
	timer.Reset(duration)
}

// //

// run resolves the address once, then merges one or more reader links into a
// single published stream via a coordinator goroutine.
func (obj *Obj) run(ctx context.Context, done chan struct{}) {
	defer close(done)
	defer func() {
		_, status := obj.Snapshot()
		status.Phase = PhaseDisconnected
		status.LinksOnline = 0
		obj.publishStatus(status, new(time.Time), true)
	}()

	ip := obj.config.IP
	var errCount uint64
	scanPub := time.Time{}
	for ip == "" && ctx.Err() == nil {
		obj.publishStatus(StatusObj{Phase: PhaseScanning, Errors: errCount}, &scanPub, true)
		found, err := Discover(ctx, &DiscoverConfigObj{Port: obj.config.Port})
		if err == nil && len(found) > 0 {
			ip = found[0].IP
			break
		}
		if ctx.Err() != nil {
			return
		}
		errCount++
		sleepCtx(ctx, discoverRetryDelay)
	}
	if ctx.Err() != nil {
		return
	}

	rawCh := make(chan rawSignalObj)
	pktCh := make(chan PacketObj, 256)
	linkCh := make(chan linkSignalObj)
	streamLiveness := new(streamLivenessObj)

	var wg sync.WaitGroup
	spawn := func(index int, delay time.Duration) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			obj.reader(ctx, index, ip, delay, streamLiveness, rawCh, pktCh, linkCh)
		}()
	}

	coord := &coordinatorObj{
		obj: obj, ip: ip, start: time.Now(), errCount: errCount,
		spawn: spawn, info: newInfoAccumulator(),
	}

	// Initial pool; auto-detection may grow it once a profile that prefers more
	// overlapping links is identified.
	coord.growTo(obj.effectiveConnections())
	coord.publishStatus(PhaseConnecting, true)

	coord.loop(ctx, rawCh, pktCh, linkCh)
	wg.Wait()
}

// coordinatorObj merges the reader links: it decodes, feeds the sink, and owns
// all publishing so those run single-threaded regardless of link count.
type coordinatorObj struct {
	obj       *Obj
	ip        string
	start     time.Time
	reads     uint64
	errCount  uint64
	connected int
	deviceID  string
	lastPub   time.Time
	spawn     func(index int, delay time.Duration) // start another reader
	running   int                                  // readers started so far
	info      *infoAccumulatorObj
	stateLive bool

	linkOnline     [MaxConnections]bool
	linkGeneration [MaxConnections]uint64
}

func (c *coordinatorObj) phase() PhaseID {
	if c.stateLive {
		return PhaseConnected
	}
	return PhaseConnecting
}

// growTo starts readers until desired are running, staggering the new ones
// across the session budget so their reconnect windows do not align.
func (c *coordinatorObj) growTo(desired int) {
	if desired < 1 {
		desired = 1
	}
	step := sessionBudget / time.Duration(desired)
	for ; c.running < desired; c.running++ {
		c.spawn(c.running, time.Duration(c.running)*step)
	}
}

func (c *coordinatorObj) publishStatus(phase PhaseID, force bool) {
	rate := 0.0
	if elapsed := time.Since(c.start).Seconds(); elapsed > 0.1 {
		rate = float64(c.reads) / elapsed
	}
	c.obj.publishStatus(StatusObj{
		Phase: phase, IP: c.ip, DeviceID: c.deviceID,
		LinksOnline: c.connected, LinksTarget: c.running,
		Rate: rate, Errors: c.errCount,
	}, &c.lastPub, force)
}

func (c *coordinatorObj) loop(ctx context.Context, rawCh <-chan rawSignalObj, pktCh <-chan PacketObj,
	linkCh <-chan linkSignalObj) {
	ticker := time.NewTicker(statusMinInterval)
	defer ticker.Stop()
	idleTimer := time.NewTimer(frameIdleTimeout)
	stopTimer(idleTimer)
	defer idleTimer.Stop()
	var idle <-chan time.Time

	for {
		select {
		case <-ctx.Done():
			c.neutralize()
			return

		case signal := <-rawCh:
			if !c.acceptsRaw(signal) {
				continue
			}
			raw := signal.raw
			wasLive := c.stateLive
			c.reads++
			state := decodeState(&raw)
			c.obj.publishState(state, raw)
			c.stateLive = true
			resetTimer(idleTimer, frameIdleTimeout)
			idle = idleTimer.C
			if c.obj.feed(state) != nil {
				c.errCount++
			}
			c.publishStatus(c.phase(), !wasLive)

		case packet := <-pktCh:
			c.obj.publishPacket(packet)
			if c.info.observe(packet) {
				c.obj.publishInfo(c.info.snapshot())
			}
			if id, ok := packetDeviceID(packet); ok && id != c.deviceID {
				c.deviceID = id
				if c.obj.autoProfile {
					// Decode and the device layout are universal, so any match
					// is safe to adopt; the profile only changes identity,
					// transport, and capabilities.
					if matched, found := ProfileByDeviceID(id); found &&
						matched.ID != c.obj.Profile().ID {
						c.obj.setProfile(matched)
						// A recycling remote may prefer more overlapping links.
						c.growTo(c.obj.effectiveConnections())
					}
				}
				c.publishStatus(c.phase(), true)
			}

		case signal := <-linkCh:
			if signal.event.Error != nil {
				c.errCount++
			}
			prev := c.connected
			delta := c.applyLinkSignal(signal)
			c.connected += delta
			if c.connected < 0 {
				c.connected = 0
			}
			c.obj.publishLink(signal.event)
			if delta != 0 && prev != c.connected {
				linkBoundary := (prev == 0) != (c.connected == 0)
				c.publishStatus(c.phase(), linkBoundary)
			}

		case <-idle:
			idle = nil
			c.neutralize()
			c.publishStatus(c.phase(), true)

		case <-ticker.C:
			c.publishStatus(c.phase(), false)
		}
	}
}

// reader owns one link: connect, read, answer the RC's polls, and forward raw
// channel data and packet headers to the coordinator. It never publishes.
func (obj *Obj) reader(ctx context.Context, index int, ip string, delay time.Duration,
	streamLiveness *streamLivenessObj, rawCh chan<- rawSignalObj,
	pktCh chan<- PacketObj, linkCh chan<- linkSignalObj) {
	if delay > 0 {
		sleepCtx(ctx, delay)
	}

	var (
		stream      [streamBufSize]byte
		streamLen   int
		channels    [maxChannels]int16
		packets     [maxParsePackets]PacketObj
		raw         RawObj
		seq         uint16 = 1
		idle        int
		up          bool
		retryDelay  bool
		hadState    bool
		versionDone bool // one-shot identity query on the first link only
		liveness    = readerLivenessObj{stream: streamLiveness}
		generation  uint64
	)

	link := newLink(ip, obj.config.Port)
	notify := func(delta int, online bool, operation string, eventErr error, diagnosticOnly bool) {
		if diagnosticOnly && !obj.hasLinkCallbacks() {
			return
		}
		signal := linkSignalObj{
			delta:      delta,
			generation: generation,
			event: LinkEventObj{
				Index: index, Online: online, Operation: operation, Error: eventErr,
			},
		}
		select {
		case linkCh <- signal:
		case <-ctx.Done():
		}
	}
	markDown := func(operation string, eventErr error) {
		retryDelay = !hadState
		if up {
			notify(-1, false, operation, eventErr, false)
			up = false
			return
		}
		notify(0, false, operation, eventErr, true)
	}
	defer func() {
		link.disconnect()
		if up {
			notify(-1, false, linkOperationDisconnect, nil, false)
		}
	}()

	for ctx.Err() == nil {
		if !link.connected() {
			if up {
				markDown(linkOperationDisconnect, nil)
			}
			if retryDelay {
				sleepCtx(ctx, reconnectDelay)
				retryDelay = false
				if ctx.Err() != nil {
					return
				}
			}
			streamLen, idle = 0, 0
			if connectErr := link.connect(); connectErr != nil {
				notify(0, false, linkOperationConnect, connectErr, true)
				sleepCtx(ctx, reconnectDelay)
				continue
			}
			up = true
			hadState = false
			generation = liveness.connected(time.Now())
			notify(1, true, linkOperationConnect, nil, false)
			// One-shot identity query on link 0. It requests an ack, so the
			// RC may reset this link soon after answering; the other links
			// cover the gap and this link rejoins silently.
			if obj.config.QueryInfo && index == 0 && !versionDone {
				req := buildVersionRequest(seq)
				if sendErr := link.send(req[:]); sendErr != nil {
					markDown(linkOperationWrite, sendErr)
					continue
				}
				versionDone = true
				seq++
			}
			// First keepalive right away to trigger the data stream.
			if frame, send := keepaliveFrame(obj.effectiveKeepalive(), seq); send {
				if sendErr := link.send(frame[:]); sendErr != nil {
					markDown(linkOperationWrite, sendErr)
					continue
				}
				seq++
			}
		}

		if liveness.expired(time.Now()) {
			link.disconnect()
			markDown(linkOperationRead, errFrameIdle)
			streamLen = 0
			continue
		}

		n, err := link.read(stream[streamLen:], readTimeout)
		if err != nil {
			markDown(linkOperationRead, err)
			streamLen = 0
			continue
		}
		if n == 0 {
			idle++
			if idle >= keepaliveIdleCycles {
				if frame, send := keepaliveFrame(obj.effectiveKeepalive(), seq); send {
					if sendErr := link.send(frame[:]); sendErr != nil {
						markDown(linkOperationWrite, sendErr)
						continue
					}
					seq++
				}
				idle = 0
			}
			continue
		}
		streamLen += n
		idle = 0

		// Bytes returned before EOF remain valid and must reach the coordinator
		// before the session is marked offline.
		var pendingReadErr error
		for i := 0; i < drainAttempts && streamLen < streamBufSize-streamHeadroom; i++ {
			more, moreErr := link.read(stream[streamLen:], drainTimeout)
			if moreErr != nil {
				pendingReadErr = moreErr
				break
			}
			if more == 0 {
				break
			}
			streamLen += more
		}

		numCh, payloadLen, consumed, packetCount := parseStream(stream[:streamLen], &channels, &raw.Payload, &packets)

		for i := 0; i < packetCount; i++ {
			packet := packets[i]
			select {
			case pktCh <- packet:
			case <-ctx.Done():
				return
			}
			// Explicit ack requests, plus (with Handshake) the recurring polls
			// the RC sends every session and closes the link without.
			if pendingReadErr != nil {
				continue
			}
			if !packet.IsResponse() && packet.AckType() != 0 {
				if sendErr := link.send(buildResponse(packet, nil)); sendErr != nil {
					markDown(linkOperationWrite, sendErr)
				}
			} else if (obj.config.Handshake || obj.Profile().Handshake) && isPoll(packet) {
				if sendErr := link.send(buildResponse(packet, packet.Payload[:packet.Len])); sendErr != nil {
					markDown(linkOperationWrite, sendErr)
				}
			}
		}

		switch {
		case consumed > 0 && consumed < streamLen:
			copy(stream[:], stream[consumed:streamLen])
			streamLen -= consumed
		case consumed > 0:
			streamLen = 0
		}
		if streamLen > streamBufSize-streamHeadroom {
			streamLen = 0
		}

		if numCh > 0 {
			liveness.observed(time.Now())
			hadState = true
			raw.Len = payloadLen
			raw.Count = numCh
			raw.Channels = channels
			select {
			case rawCh <- rawSignalObj{
				raw: raw, index: index, generation: generation,
			}:
			case <-ctx.Done():
				return
			}
		}
		if pendingReadErr != nil {
			streamLen = 0
			markDown(linkOperationRead, pendingReadErr)
			continue
		}
	}
}
