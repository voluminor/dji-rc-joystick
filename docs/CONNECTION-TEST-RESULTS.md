# Local RC connection comparison

Tested October 27, 2026 on the user's DJI RC / RM330, firmware 01.00.03.14,
at the supplied LAN address, after disconnecting competing GUI sessions.
The CLI observed status and per-link events; it did not emulate game input.
Logs from comparisons before the GUI was disconnected were excluded.

An outage is a connected -> connecting transition after the first valid
controller frame. The one-second input watchdog was unchanged. Input rate
is the application's cumulative decoded-frame rate, not a USB polling rate.

| Mode | Links | Duration | Complete outages | Final input rate |
|---|---:|---:|---:|---:|
| Passive, no optional poll replies | 1 | 12 s | 3 | 2.72 Hz |
| Version inquiries | 1 | 12 s | 2 | 4.27 Hz |
| Heartbeat + poll replies | 1 | 12 s | 0 | 7.03 Hz |
| Heartbeat + poll replies | 1 | 45 s | 5 | 4.85 Hz |
| Heartbeat + poll replies | 2 | 30 s | 2 | 5.62 Hz |
| Heartbeat + poll replies | 5 | 12 s | 0 | 4.85 Hz |
| Version inquiries | 5 | 12 s | 0 | 6.08 Hz |
| Passive + poll replies | 5 | 12 s | 0 | 8.78 Hz |
| Passive + poll replies + one identity query | 5 | 45 s | 1 | 3.29 Hz |

Chosen RM330 default: five staggered readers, no unsolicited periodic
keepalives, recurring poll replies. Identity is queried once when requested.
RC 2 retains its existing passive five-reader policy.

The remote still actively closes/resets TCP connections. This work reduces
complete stream outages in the observed sample; it does not establish the
firmware's authentication/session policy or eliminate underlying resets.
The short runs and changing wireless conditions limit direct comparisons.
Longer tests with the intended game, controller movement, USB interruption,
and ordinary background network traffic are still required from the user.

Xbox validation on this PC: the bundled native client created an Xbox 360
target, submitted a neutral report and released it successfully. The debug
GUI also reached its running gamepad state with the RC connected.
