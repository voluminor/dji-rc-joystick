# DJI RC Joystick

DJI RC Joystick is a set of Go modules for smart-display DJI remote
controllers. The packages discover controllers on the local network, maintain
TCP connections, decode DUML packets, publish control state, and map that state
to a virtual gamepad when the operating system supports one.

The network protocol and initial channel decoding are based on
[stiad/dji-rc-linux](https://github.com/stiad/dji-rc-linux). This project turns
that work into reusable Go packages and adds controller profiles, configurable
bindings, application clients, and platform-specific virtual-input backends.

## Controller compatibility

| Controller            | Status                                |
|-----------------------|---------------------------------------|
| DJI RC                | Supported and tested on real hardware |
| DJI RC 2              | Supported and tested on real hardware |
| DJI RC Pro            | Expected to work, but not tested      |
| DJI Smart Controller  | Expected to work, but not tested      |
| DJI RC Pro Enterprise | Expected to work, but not tested      |

DJI RC and DJI RC 2 have separate profiles for identification, available
controls, keepalive behavior, and connection count. The other controllers use
similar Android and DUML components, but discovery, complete control coverage,
and connection stability have not been verified.

## Go modules

The root module is `github.com/voluminor/dji-rc-joystick`.

- [`mod/rc`](mod/rc/README.md) handles discovery, TCP and DUML transport,
  controller profiles, state decoding, bindings, and callbacks.
- [`mod/vjoy`](mod/vjoy/README.md) defines virtual devices and provides Linux
  `uinput` and Windows vJoy backends.
- [`mod/data`](mod/data/README.md) provides shared access to settings, bindings,
  and translations.
- [`mod/data/settings`](mod/data/settings/README.md) loads, validates, and
  atomically saves JSON and YAML settings.
- [`mod/data/language`](mod/data/language/README.md) provides thread-safe access
  to embedded translation catalogs.

Each module README documents its API, concurrency model, resource limits, and
platform-specific behavior.

## CLI and GUI tools

The repository also provides ready-to-use applications:

- [CLI](cli/README.md) offers virtual-gamepad emulation, terminal monitoring,
  NDJSON output, controller discovery, and protocol diagnostics.
- [GUI](gui/README.md) provides a windowed monitor, live control visualization,
  binding configuration, and emulation controls. Its interface is available in
  [27 languages](gui/README.md#supported-interface-languages).

Release assets are published on
[GitHub Releases](https://github.com/voluminor/dji-rc-joystick/releases). The
release workflow builds CLI binaries; GUI assets are assembled manually for the
release. Supported platforms, setup, configuration, and build details are
covered in the [CLI README](cli/README.md) and [GUI README](gui/README.md).

Discovery and monitoring are available on Linux, Windows, and macOS. Virtual
gamepad output is available on Linux and Windows; macOS does not provide a
backend.

## Project

- [GitHub Releases](https://github.com/voluminor/dji-rc-joystick/releases)
- [Issues](https://github.com/voluminor/dji-rc-joystick/issues)
- [Contributing](CONTRIBUTING.md)
- [Security policy](SECURITY.md)
- [Code of Conduct](CODE_OF_CONDUCT.md)
- [License](LICENSE): GNU Lesser General Public License version 2.1

## Liftoff and other games

Games may not select the virtual controller automatically. Choose it in the
game's controller settings and run the game's calibration before use.

On Steam Deck, Desktop Mode provides the most predictable startup order: make
the virtual controller available before starting the game.

### DJI RC 2 with Liftoff on Steam Deck

<video width="850" alt="Liftoff on Steam Deck"  src="https://github.com/user-attachments/assets/112f7d0d-6a5a-4d01-8a44-08ed0122d9b6"></video>

[Open the video separately](.github/media/steamdeck-rc.mp4)

The video shows a DJI RC 2 controlling Liftoff through a virtual gamepad on
Steam Deck.
