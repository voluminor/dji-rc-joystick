//go:build windows && amd64

// xbox-check verifies the bundled native client and installed bus without an RC.
package main

import (
	"fmt"
	"os"

	"github.com/voluminor/dji-rc-joystick/mod/rc"
	"github.com/voluminor/dji-rc-joystick/mod/vjoy"
)

func main() {
	if err := check(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println("PASS: Xbox 360 device created, neutral report submitted, device released.")
}

func check() error {
	if err := os.Setenv("DJI_RC_GAMEPAD", "xbox"); err != nil {
		return err
	}
	mapping := rc.DefaultMapping()
	d, err := vjoy.New(mapping.DeviceSpec(rc.DefaultProfile()))
	if err != nil {
		return err
	}
	defer d.Close()
	return mapping.Feed(d, rc.StateObj{Mode: rc.ModeNormal})
}
