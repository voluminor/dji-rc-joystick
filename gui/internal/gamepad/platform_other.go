//go:build !linux

package gamepad

import (
	"context"
	"io"
	"os"

	"github.com/voluminor/dji-rc-joystick/mod/vjoy"

	"github.com/rs/zerolog"
)

// // // // // // // // // //

func createDevice(
	ctx context.Context,
	spec vjoy.SpecObj,
	logger zerolog.Logger,
) (vjoy.Interface, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	device, err := vjoy.New(spec)
	if err == nil {
		logger.Info().
			Str("backend", spec.Backend).
			Str("requested_backend", os.Getenv("DJI_RC_GAMEPAD")).
			Str("access", "direct").
			Msg("Virtual gamepad device created")
	}
	return device, err
}

// //

// RunHelper reports that the privileged protocol is unavailable off Linux.
func RunHelper(
	_ []string,
	_ io.Reader,
	_ io.Writer,
	_ ...io.Writer,
) (bool, error) {
	return false, nil
}
