# OmniRouter V1.0.0 release checklist

This checklist is the release gate for the first stable release. It intentionally
separates source/CI evidence from live-provider and device acceptance; passing
one category must not be represented as passing another.

## Baseline and branch policy

- [x] `main` remains the stable branch.
- [x] Create `dev` from the current `main` baseline.
- [x] Create `feature/v1-release-readiness` from `dev`.
- [x] Merge the first reviewed, CI-green release-readiness change into `dev` (PR #1).
- [ ] Promote the accepted release candidate to `main` only after all remaining V1 gates pass.
- [ ] Record the exact release commit SHA and tag it `v1.0.0`.

## Existing verified baseline (2026-11-05)

- [x] Go 1.23 module and dependency lockfile are present.
- [x] Real `modernc.org/sqlite` integration suite passed in GitHub Actions
      run 41954487103, according to the repository validation record.
- [x] `go test ./...`, `go test -race ./...`, `go vet ./...`, and Linux
      executable build passed in that run.
- [ ] Re-run all gates on the final release candidate commit.

## V1 functional acceptance

- [ ] OpenAI-compatible non-streaming request against a controlled upstream.
- [ ] OpenAI-compatible SSE request, explicit terminal marker, and usage capture.
- [ ] Anthropic Messages text/image/tool request and streaming lifecycle.
- [ ] Gemini generateContent / streamGenerateContent text/image/function-call
      mappings for the documented supported subset.
- [ ] Client cancellation and premature upstream EOF are recorded as interrupted
      or failed, never as successful completion.
- [ ] Provider failover is verified for non-streaming requests; no unsafe retry
      occurs after a stream has started.
- [ ] SQLite restart recovery verifies Provider, Model, Account, Session,
      Credential metadata, request history, and usage aggregates.
- [ ] Credential encryption key missing/invalid/lost scenarios fail closed and
      are documented. Persistent key rotation remains explicitly out of scope
      unless implemented and tested before release.
- [ ] Admin API authorization, secret redaction, and input validation pass.
- [ ] The embedded admin page clearly exposes the V1 operational controls and
      does not imply unsupported browser-login/session automation.

## Provider acceptance

- [x] An opt-in OpenAI-compatible live smoke script is provided at
      `scripts/provider-smoke.sh`; its real-provider execution remains pending.
- [x] A manual GitHub Actions workflow is provided to run the live smoke test
      using repository secrets; it must be configured and executed before release.

Use test credentials stored only as GitHub Actions secrets or local environment
variables. Never commit credentials, cookies, API keys, or real request payloads.

- [ ] At least one real OpenAI-compatible upstream: non-streaming + SSE.
- [ ] At least one real Anthropic-compatible upstream: Messages + SSE.
- [ ] At least one real Gemini-compatible upstream: generate + stream.
- [ ] Record provider/model, date, protocol path, result, and sanitized error
      summary in the acceptance report. Do not store secrets or full prompts.

## Build and packaging

- [x] Linux amd64 and arm64 cross-builds passed in CI run 41958521186.
- [x] Windows amd64 cross-build passed in CI run 41958521186.
- [x] macOS amd64 and arm64 cross-builds passed in CI run 41958521186.
- [ ] SHA-256 checksums generated for all release assets.
- [x] Fresh-install and upgrade instructions drafted in `docs/INSTALL.md`; fresh-machine execution remains pending.
- [ ] Database backup/restore procedure documented and tested.
- [ ] Release notes list supported protocol subsets and known limitations.
- [x] Android/Termux runtime smoke script and device procedure are provided in `scripts/termux-smoke.sh` and `docs/ANDROID-TERMUX-ACCEPTANCE.md`.
- [ ] Android/Termux arm64 runtime is tested on a real device: launch, SQLite
      read/write and restart recovery, local/LAN binding, storage permissions,
      and process shutdown/restart. The Linux arm64 release binary is the
      intended Termux artifact; this is not an Android APK.
- [ ] Do not publish V1 as supporting Android/Termux until the device gate above passes.

## Publication

- [ ] Final CI is green on the exact release commit.
- [ ] No open P0/P1 defects.
- [ ] Promote the approved release candidate to `main`.
- [ ] Create annotated tag `v1.0.0`.
- [ ] Publish binaries, source archive, and `SHA256SUMS`.
- [ ] Verify public/private release visibility and asset downloads.
