package gamepad

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/voluminor/dji-rc-joystick/mod/rc"
	"github.com/voluminor/dji-rc-joystick/mod/vjoy"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

type createDeviceFunc func(context.Context, vjoy.SpecObj) (vjoy.Interface, error)

type dependenciesObj struct {
	createDevice createDeviceFunc
}

type operationObj struct {
	ctx     context.Context
	cancel  context.CancelFunc
	done    chan struct{}
	stateC  chan rc.StateObj
	onlineC chan bool
}

// Obj owns one virtual device and a bounded latest-state feed.
type Obj struct {
	dependencies dependenciesObj
	log          zerolog.Logger
	onChange     func(SnapshotObj)

	mu        sync.Mutex
	operation *operationObj
	online    bool
	snapshot  SnapshotObj
}

// //

func cloneMapping(mapping rc.MappingObj) rc.MappingObj {
	clone := rc.MappingObj{
		Backend: mapping.Backend,
		Axes:    make(map[rc.AxisID]string, len(mapping.Axes)),
		Invert:  make(map[rc.AxisID]bool, len(mapping.Invert)),
		Mode:    mapping.Mode,
		Buttons: make(map[rc.ButtonID]string, len(mapping.Buttons)),
	}
	for axis, action := range mapping.Axes {
		clone.Axes[axis] = action
	}
	for axis, inverted := range mapping.Invert {
		clone.Invert[axis] = inverted
	}
	for button, action := range mapping.Buttons {
		clone.Buttons[button] = action
	}
	return clone
}

func offerState(target chan rc.StateObj, state rc.StateObj) {
	select {
	case target <- state:
		return
	default:
	}
	select {
	case <-target:
	default:
	}
	select {
	case target <- state:
	default:
	}
}

func offerOnline(target chan bool, online bool) {
	select {
	case target <- online:
		return
	default:
	}
	select {
	case <-target:
	default:
	}
	select {
	case target <- online:
	default:
	}
}

func drainStates(target chan rc.StateObj) {
	for {
		select {
		case <-target:
		default:
			return
		}
	}
}

func defaultDependencies(logger zerolog.Logger) dependenciesObj {
	return dependenciesObj{
		createDevice: func(
			ctx context.Context,
			spec vjoy.SpecObj,
		) (vjoy.Interface, error) {
			return createDevice(ctx, spec, logger)
		},
	}
}

func (obj *Obj) changeLocked(
	phase PhaseID,
	err error,
	clearError bool,
) SnapshotObj {
	obj.snapshot.Phase = phase
	if clearError {
		obj.snapshot.Error = ""
	}
	if err != nil {
		obj.snapshot.Error = err.Error()
		obj.snapshot.ErrorRevision++
	}
	obj.snapshot.Revision++
	return obj.snapshot
}

func (obj *Obj) notify(snapshot SnapshotObj) {
	if obj.onChange != nil {
		obj.onChange(snapshot)
	}
}

func (obj *Obj) transition(operation *operationObj, phase PhaseID) bool {
	obj.mu.Lock()
	if obj.operation != operation || obj.snapshot.Phase == PhaseStopping {
		obj.mu.Unlock()
		return false
	}
	previousPhase := obj.snapshot.Phase
	snapshot := obj.changeLocked(phase, nil, false)
	obj.mu.Unlock()

	obj.log.Debug().
		Str("from", previousPhase.String()).
		Str("to", phase.String()).
		Msg("Virtual gamepad phase changed")
	obj.notify(snapshot)
	return true
}

func (obj *Obj) finish(operation *operationObj, err error) {
	obj.mu.Lock()
	if obj.operation != operation {
		obj.mu.Unlock()
		close(operation.done)
		return
	}
	obj.operation = nil
	phase := PhaseStopped
	if err != nil {
		phase = PhaseError
	}
	snapshot := obj.changeLocked(phase, err, false)
	obj.mu.Unlock()

	event := obj.log.Info()
	message := "Virtual gamepad stopped"
	if err != nil {
		event = obj.log.Error().Err(err)
		message = "Virtual gamepad failed"
	}
	event.
		Str("phase", phase.String()).
		Msg(message)
	obj.notify(snapshot)
	close(operation.done)
}

func (obj *Obj) runDevice(
	operation *operationObj,
	device vjoy.Interface,
	mapping rc.MappingObj,
	online bool,
) error {
	neutral := rc.StateObj{Mode: rc.ModeNormal}
	if !online {
		if err := mapping.Feed(device, neutral); err != nil {
			return fmt.Errorf("neutralize virtual gamepad: %w", err)
		}
		obj.log.Warn().Msg("Virtual gamepad neutralized while remote is offline")
		if !obj.transition(operation, PhaseWaiting) {
			return nil
		}
	} else if !obj.transition(operation, PhaseRunning) {
		return nil
	}

	for {
		select {
		case <-operation.ctx.Done():
			return nil

		case nextOnline := <-operation.onlineC:
			if nextOnline == online {
				continue
			}
			online = nextOnline
			if online {
				if !obj.transition(operation, PhaseRunning) {
					return nil
				}
				obj.log.Info().Msg("Virtual gamepad resumed after remote reconnect")
				continue
			}

			drainStates(operation.stateC)
			if err := mapping.Feed(device, neutral); err != nil {
				return fmt.Errorf("neutralize virtual gamepad: %w", err)
			}
			obj.log.Warn().Msg("Virtual gamepad neutralized after remote link loss")
			if !obj.transition(operation, PhaseWaiting) {
				return nil
			}

		case state := <-operation.stateC:
			if !online {
				continue
			}
			if err := mapping.Feed(device, state); err != nil {
				return fmt.Errorf("update virtual gamepad: %w", err)
			}
		}
	}
}

func (obj *Obj) run(
	operation *operationObj,
	mapping rc.MappingObj,
	profile rc.ProfileObj,
) {
	spec := mapping.DeviceSpec(profile)
	obj.log.Debug().
		Str("profile", profile.Name).
		Int("controls", len(spec.Controls)).
		Uint16("vendor_id", spec.VendorID).
		Uint16("product_id", spec.ProductID).
		Msg("Creating virtual gamepad device")
	device, err := obj.dependencies.createDevice(
		operation.ctx,
		spec,
	)
	if err != nil {
		switch {
		case errors.Is(err, context.Canceled),
			errors.Is(err, ErrAuthorizationCanceled):
			obj.finish(operation, nil)
		default:
			obj.finish(operation, fmt.Errorf("create virtual gamepad: %w", err))
		}
		return
	}

	obj.mu.Lock()
	active := obj.operation == operation
	online := obj.online
	obj.mu.Unlock()
	if !active {
		_ = device.Close()
		obj.finish(operation, nil)
		return
	}

	runErr := obj.runDevice(operation, device, mapping, online)
	closeErr := device.Close()
	if runErr == nil && closeErr != nil {
		runErr = fmt.Errorf("close virtual gamepad: %w", closeErr)
	}
	obj.finish(operation, runErr)
}

func newWithDependencies(
	dependencies dependenciesObj,
	onChange func(SnapshotObj),
	loggers ...zerolog.Logger,
) *Obj {
	logger := zerolog.Nop()
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	if dependencies.createDevice == nil {
		dependencies = defaultDependencies(logger)
	}
	return &Obj{
		dependencies: dependencies,
		log:          logger,
		onChange:     onChange,
		snapshot:     SnapshotObj{Phase: PhaseStopped},
	}
}

// //

// New creates a stopped virtual-gamepad runtime.
func New(onChange func(SnapshotObj), loggers ...zerolog.Logger) *Obj {
	logger := zerolog.Nop()
	if len(loggers) > 0 {
		logger = loggers[0]
	}
	return newWithDependencies(defaultDependencies(logger), onChange, logger)
}

// Snapshot returns the latest runtime state.
func (obj *Obj) Snapshot() SnapshotObj {
	obj.mu.Lock()
	defer obj.mu.Unlock()
	return obj.snapshot
}

// Start validates the mapping and creates a virtual device asynchronously.
func (obj *Obj) Start(mapping rc.MappingObj, profile rc.ProfileObj) error {
	mapping = cloneMapping(mapping)
	if err := mapping.Validate(); err != nil {
		obj.log.Debug().
			Err(err).
			Msg("Virtual gamepad mapping validation failed")
		return fmt.Errorf("validate gamepad mapping: %w", err)
	}
	if profile.Name == "" {
		obj.log.Debug().Msg("Virtual gamepad profile validation failed")
		return errors.New("gamepad profile name is empty")
	}
	obj.mu.Lock()
	if obj.operation != nil {
		obj.mu.Unlock()
		return errOperationActive
	}
	ctx, cancel := context.WithCancel(context.Background())
	operation := &operationObj{
		ctx:     ctx,
		cancel:  cancel,
		done:    make(chan struct{}),
		stateC:  make(chan rc.StateObj, 1),
		onlineC: make(chan bool, 1),
	}
	obj.operation = operation
	snapshot := obj.changeLocked(PhaseStarting, nil, true)
	obj.mu.Unlock()

	obj.log.Info().
		Str("profile", profile.Name).
		Msg("Virtual gamepad is starting")
	obj.notify(snapshot)
	go obj.run(operation, mapping, profile)
	return nil
}

// Stop closes the active virtual device and waits for its worker.
func (obj *Obj) Stop() {
	obj.mu.Lock()
	operation := obj.operation
	if operation == nil {
		obj.mu.Unlock()
		obj.log.Debug().Msg("Virtual gamepad stop requested while stopped")
		return
	}
	snapshot := obj.changeLocked(PhaseStopping, nil, false)
	operation.cancel()
	done := operation.done
	obj.mu.Unlock()

	obj.log.Info().Msg("Virtual gamepad is stopping")
	obj.notify(snapshot)
	<-done
}

// SetOnline reports whether live remote frames are currently available.
func (obj *Obj) SetOnline(online bool) {
	obj.mu.Lock()
	previous := obj.online
	obj.online = online
	operation := obj.operation
	obj.mu.Unlock()
	if operation != nil && previous != online {
		offerOnline(operation.onlineC, online)
	}
}

// Feed offers the newest decoded controller state without blocking its source.
func (obj *Obj) Feed(state rc.StateObj) {
	obj.mu.Lock()
	operation := obj.operation
	online := obj.online
	obj.mu.Unlock()
	if operation != nil && online {
		offerState(operation.stateC, state)
	}
}
