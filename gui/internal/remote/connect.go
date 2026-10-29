package remote

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strings"

	"github.com/voluminor/dji-rc-joystick/mod/rc"
)

// // // // // // // // // //

func errorsIsCancellation(err error) bool {
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded)
}

func validateTarget(ip string, port int) (string, error) {
	ip = strings.TrimSpace(ip)
	if net.ParseIP(ip) == nil {
		return "", fmt.Errorf("invalid remote IP address %q", ip)
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("remote port %d is out of range 1..65535", port)
	}
	return ip, nil
}

func (obj *Obj) observeStatus(
	operation *operationObj,
	controller controllerInterface,
	status rc.StatusObj,
) {
	profile := controller.Profile()

	obj.mu.Lock()
	if !obj.activeLocked(operation) || obj.snapshot.Phase == PhaseDisconnecting {
		obj.mu.Unlock()
		return
	}

	previousPhase := obj.snapshot.Phase
	obj.snapshot.Status = status
	obj.snapshot.Profile = cloneProfile(profile)
	online := status.Phase == rc.PhaseConnected
	switch {
	case online:
		operation.everOnline = true
		obj.snapshot.Phase = PhaseConnected
	case operation.everOnline:
		obj.snapshot.Phase = PhaseReconnecting
	default:
		obj.snapshot.Phase = PhaseConnecting
	}
	phase := obj.snapshot.Phase
	obj.notifyLocked()
	obj.mu.Unlock()

	obj.log.Debug().
		Str("pipeline_phase", status.Phase.String()).
		Str("remote_phase", phase.String()).
		Str("ip", status.IP).
		Str("device_id", status.DeviceID).
		Int("links_online", status.LinksOnline).
		Int("links_target", status.LinksTarget).
		Float64("frame_rate", status.Rate).
		Uint64("errors", status.Errors).
		Msg("Remote status updated")
	if phase != previousPhase {
		event := obj.log.Info()
		message := "Remote connection phase changed"
		if phase == PhaseReconnecting {
			event = obj.log.Warn()
			message = "Remote connection lost; reconnecting"
		}
		event.
			Str("from", previousPhase.String()).
			Str("to", phase.String()).
			Str("ip", status.IP).
			Msg(message)
	}
	obj.gamepad.SetOnline(online)
}

func (obj *Obj) observeState(operation *operationObj, state rc.StateObj) {
	obj.mu.Lock()
	if !obj.activeLocked(operation) || obj.snapshot.Phase == PhaseDisconnecting {
		obj.mu.Unlock()
		return
	}
	obj.snapshot.State = state
	obj.notifyLocked()
	obj.mu.Unlock()

	obj.log.Trace().
		Int16("left_stick_h", state.Axes[rc.AxisLeftH]).
		Int16("left_stick_v", state.Axes[rc.AxisLeftV]).
		Int16("right_stick_h", state.Axes[rc.AxisRightH]).
		Int16("right_stick_v", state.Axes[rc.AxisRightV]).
		Int16("left_wheel", state.Axes[rc.AxisLeftWheel]).
		Int16("right_wheel", state.Axes[rc.AxisRightWheel]).
		Str("mode", state.Mode.String()).
		Uint32("buttons", state.Buttons).
		Bool("rth", state.Pressed(rc.ButtonRTH)).
		Bool("record", state.Pressed(rc.ButtonRecord)).
		Bool("camera_half", state.Pressed(rc.ButtonCameraHalf)).
		Bool("camera_full", state.Pressed(rc.ButtonCameraFull)).
		Bool("c1", state.Pressed(rc.ButtonC1)).
		Bool("c2", state.Pressed(rc.ButtonC2)).
		Msg("Decoded controller state")
	obj.gamepad.Feed(state)
}

func (obj *Obj) observeInfo(operation *operationObj, info rc.InfoObj) {
	obj.mu.Lock()
	if !obj.activeLocked(operation) || obj.snapshot.Phase == PhaseDisconnecting {
		obj.mu.Unlock()
		return
	}
	obj.snapshot.Info = displayInfo(info)
	obj.notifyLocked()
	obj.mu.Unlock()

	obj.log.Debug().
		Str("name", info.Name).
		Str("model", info.Model).
		Str("firmware", info.Firmware).
		Bool("battery_decoded", info.Battery.Decoded).
		Int("battery_percent", info.Battery.Percent).
		Int("battery_voltage_mv", info.Battery.VoltageMV).
		Int("telemetry_records", len(info.Telemetry)).
		Msg("Remote information updated")
}

func (obj *Obj) runConnection(
	ctx context.Context,
	operation *operationObj,
	controller controllerInterface,
) {
	err := controller.Start(ctx)
	if err == nil {
		<-ctx.Done()
	} else {
		err = fmt.Errorf("start remote: %w", err)
	}
	controller.Stop()
	obj.finishOperation(operation, err)
}

// //

// Connect starts one cancellable RC session for a validated target.
func (obj *Obj) Connect(ip string, port int) error {
	resolvedIP, err := validateTarget(ip, port)
	if err != nil {
		obj.log.Debug().
			Int("port", port).
			Msg("Remote connection target rejected")
		return err
	}

	obj.mu.RLock()
	active := obj.operation != nil
	obj.mu.RUnlock()
	if active {
		return errOperationActive
	}

	controller, err := obj.dependencies.newController(rc.ConfigObj{
		IP:        resolvedIP,
		Port:      port,
		QueryInfo: true,
	})
	if err != nil {
		return fmt.Errorf("create remote: %w", err)
	}

	obj.mu.Lock()
	if obj.operation != nil {
		obj.mu.Unlock()
		controller.Stop()
		return errOperationActive
	}

	operation, ctx := obj.beginOperationLocked()
	controller.OnStatus(func(status rc.StatusObj) {
		obj.observeStatus(operation, controller, status)
	})
	controller.OnState(func(state rc.StateObj) {
		obj.observeState(operation, state)
	})
	controller.OnInfo(func(info rc.InfoObj) {
		obj.observeInfo(operation, info)
	})
	if diagnostics, ok := controller.(interface{ OnLink(func(rc.LinkEventObj)) }); ok {
		diagnostics.OnLink(func(event rc.LinkEventObj) {
			obj.log.Debug().Int("link", event.Index).Bool("online", event.Online).
				Str("operation", event.Operation).Err(event.Error).Msg("RC transport event")
		})
	}

	obj.snapshot.Phase = PhaseConnecting
	obj.snapshot.TargetIP = resolvedIP
	obj.snapshot.TargetPort = port
	obj.snapshot.Status = rc.StatusObj{
		Phase: rc.PhaseConnecting,
		IP:    resolvedIP,
	}
	obj.snapshot.State = rc.StateObj{}
	obj.snapshot.Info = rc.InfoObj{}
	obj.snapshot.Profile = cloneProfile(controller.Profile())
	obj.snapshot.Error = ""
	obj.notifyLocked()
	obj.mu.Unlock()

	obj.log.Info().
		Str("ip", resolvedIP).
		Int("port", port).
		Uint64("generation", operation.generation).
		Msg("Remote connection started")
	go obj.runConnection(ctx, operation, controller)
	return nil
}
