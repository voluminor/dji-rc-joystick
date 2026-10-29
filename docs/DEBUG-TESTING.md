# Windows debug build

Extract the complete package into a writable folder. Install the official
[ViGEmBus driver](https://github.com/nefarius/ViGEmBus/releases/tag/v1.22.0)
and reboot if requested. Existing DS4Windows users may already have it.
vJoy and DS4Windows are not required. No driver is installed automatically.
ViGEm is retired: https://docs.nefarius.at/projects/ViGEm/End-of-Life/

Double-click **Start-Debug.cmd**. Keep ViGEmClient.dll beside the EXE.
Connect the DJI RC over USB/network as usual, select its IP, then start the
virtual gamepad. Use Windows `joy.cpl` to inspect the Xbox 360 controller.
The **Output backend** dropdown selects ViGEmBus (default) or vJoy. Stop
emulation before changing it, then click **Save settings**. The vJoy option
requires its driver and SDK DLL; the app also checks the standard vJoy x64
installation directory. ViGEmBus uses the bundled ViGEmClient.dll.

When emulation reaches its running state, the app remembers that choice.
On the next launch it starts emulation automatically after the RC connects.
Pressing **Stop** disables automatic emulation for the next session. Closing
the app or disconnecting the RC preserves the choice. Auto-start is attempted
once per launch, so a missing driver does not trigger repeated error dialogs.
The manual connection fields also remember the most recently used IP/port.
The launcher uses a separate debug-settings.json and saves logs in logs/.
It leaves your existing application settings alone.

Output mapping:
- X/Y: left stick; RX/RY: right stick.
  The Xbox backend converts vertical direction to XInput's positive-up
  convention; keep the default vertical Invert settings enabled.
- Z/RZ: left/right triggers. Center and negative wheel travel release the
  trigger; positive travel presses it. Xbox has no centered wheel axes.
- Flight mode is unmapped by default so the D-pad stays released at rest.
  Opting into slider0 maps Sport/Normal/Cine to held D-pad up/neutral/down.
- Buttons 1..15: A, B, X, Y, LB, RB, Back, Start, LS, RS, Up, Down, Left,
  Right, Guide. Avoid buttons 11/12 when mode uses the D-pad.
- slider1 and auto axes are unsupported; mouse-wheel bindings still work.

Test both sticks, buttons, wheels and the mode switch for a few minutes.
Unplug/replug USB: sticks/buttons should neutralize within about one second,
and output should recover when valid frames resume. The virtual Xbox device
stays attached during reconnects. Record the controller model, firmware,
Windows version, approximate failure time and whether `joy.cpl` also freezes.
Send the logs/ files and a screenshot of any error with feedback.

Transport changes: DJI RC / RM330 uses five staggered passive readers and
recurring poll replies, rather than repeated version inquiries. RC 2
retains its passive, overlapping-link profile. Both use a two-second TCP
connect timeout and three-second transport silence tolerance.
The independent one-second input fail-safe remains. These changes were tested
on an RM330 with firmware 01.00.03.14; longer user testing is still needed.
The RC can still reset individual TCP sessions; the application reconnects.
In isolated 45-second comparisons on this PC, single-link heartbeat mode had
five complete stream outages, while this passive pool had one. This is an
improvement in this test, not a guarantee of uninterrupted input. Detailed
comparison results are recorded in docs/CONNECTION-TEST-RESULTS.md in source.

Run xbox-check.exe in a terminal to test Xbox driver setup without an RC.

To use the legacy backend, select vJoy in the dropdown and save settings.
The DJI_RC_GAMEPAD environment variable remains a fallback for callers without
an explicit backend in their device specification. Saved settings take priority.
Existing custom settings may need the mapping above; the debug package uses
fresh defaults.

Build from source with Go 1.26+ and an x64 MinGW-w64 GCC on PATH:
`powershell -ExecutionPolicy Bypass -File scripts/build-debug.ps1`.
The script also recognizes portable compilers in tmp/go and tmp/w64devkit.
Build retains symbols and disables inlining/optimization for Delve debugging.
