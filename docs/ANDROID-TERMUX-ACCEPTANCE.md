# Android / Termux runtime smoke test

The release CI cross-compiles the Linux arm64 binary. That is a build check,
not proof that the binary runs correctly on an Android device. This test is
intended to be executed directly in Termux on the target phone.

## Build artifact

Use the `linux-arm64` release build. It is a Linux arm64 executable, not an
Android APK. Device compatibility must be confirmed by running it in Termux.

## Termux setup

```sh
pkg update
pkg install curl
chmod +x ./omnirouter
bash scripts/termux-smoke.sh ./omnirouter
```

The script starts OmniRouter on loopback at `127.0.0.1:18765`, uses a temporary
SQLite database under Termux's writable home/temp directory, checks
`/healthz` and `/readyz`, verifies that the database file is created, and
sends SIGTERM to verify graceful shutdown. It removes only its temporary test
directory after the process exits.

Override the listener with `OMNI_SMOKE_ADDR` and the startup wait with
`OMNI_SMOKE_START_TIMEOUT` if needed. Do not expose the service to a LAN during
this basic smoke test.

## What this does not certify

A passing smoke test is a minimum runtime check only. V1 device acceptance
must additionally verify restart recovery using a retained database, storage
permissions for the chosen production data directory, LAN binding and firewall
behavior, long-running process behavior under Android background restrictions,
and real Provider requests. Record Android version, device architecture,
Termux version, binary SHA-256, and sanitized results in the release report.
