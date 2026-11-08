# OmniRouter V1 final closeout report

Snapshot date: 2026-11-08
Repository: https://github.com/qingjian0/OmniRouter
Release status: NOT RELEASED — V1.0.0 gates remain open

## Branch state
- main remains the stable branch.
- dev is 98 commits ahead of main.
- Feature PRs below remain separate open Draft branches and are not part of dev.
- No v1.0.0 release tag or published artifact is claimed.

## Integration candidates
| PR | Scope | State and CI evidence |
|---|---|---|
| [#12](https://github.com/qingjian0/OmniRouter/pull/12) | Native OpenAI Responses, Anthropic Messages, Gemini adapters | Open Draft; 85 commits, 17 files; run [42451617210](https://github.com/qingjian0/OmniRouter/actions/runs/42451617210), 7/7 jobs passed |
| [#8](https://github.com/qingjian0/OmniRouter/pull/8) | Adaptive scheduler with objective, latency, reliability and price inputs | Open Draft; 83 commits, 15 files; run [42452050774](https://github.com/qingjian0/OmniRouter/actions/runs/42452050774), 7/7 jobs passed |
| [#11](https://github.com/qingjian0/OmniRouter/pull/11) | Disabled-provider candidate filtering | Open Draft; overlaps scheduler files/tests in PR #8; compare before separate merge |
| [#7](https://github.com/qingjian0/OmniRouter/pull/7) | DeepSeek web-session pairing and encrypted credential lifecycle | Open Draft; 102 commits, 28 files; run [42451245405](https://github.com/qingjian0/OmniRouter/actions/runs/42451245405), 7/7 jobs passed |

PR #9 (native Anthropic) and PR #10 (native Gemini) are earlier parallel branches. PR #12 is the consolidated candidate; do not merge all three independently without checking overlap.

## Evidence boundary
The three listed CI runs demonstrate successful deterministic branch-local validation, frontend build, tests, and cross-platform packaging jobs. They do not prove that the Draft PRs are merged into dev, that live provider calls succeed, that adaptive routing performs correctly under production traffic, or that Android/Termux works on a physical device.

PR #7 remains an inert, fail-closed web-session pairing shell. Its private inference transport, SSE normalization, conversation lineage, and serialized multi-turn execution are not implemented. Do not guess private endpoint schemas or bypass CAPTCHA, proof-of-work, WAF, or other anti-abuse controls.

## Remaining V1 gates
1. Review and integrate the selected native-adapter and adaptive-scheduler changes into dev; resolve shared-file overlap and rerun the full CI matrix on the integrated commit.
2. Run live acceptance using user-controlled credentials for at least one OpenAI-compatible upstream, DeepSeek official API, native Anthropic, and native Gemini. Record provider/model, timestamp, route, result, and sanitized errors only; never commit secrets or full prompts.
3. Verify non-streaming failover, stream no-retry behavior, cancellation, premature EOF, terminal events, usage capture, and SQLite restart recovery against controlled/live endpoints.
4. Complete admin authorization, secret redaction, input-validation review, and missing/invalid/lost credential-encryption-key tests.
5. Perform fresh-install and real deployment backup/restore acceptance.
6. Run the Linux arm64 binary inside Termux on a physical Android device; verify launch, SQLite read/write and restart recovery, storage permissions, local/LAN binding, and graceful shutdown.
7. Decide whether DeepSeek consumer-web inference is V1 scope. If required, obtain sanitized authorized fixtures and implement the transport/SSE/multi-turn path. Otherwise move it to a later release and document the limitation.
8. Only after sign-off, run CI on the exact release commit, promote to main, tag v1.0.0, publish artifacts and SHA-256 checksums, and verify downloads.

## Closeout policy
- Keep main stable; development integration targets dev.
- A green feature-branch CI is not a merge, live acceptance, or release.
- Never claim live, device, secret-validation, or production-restore tests without execution evidence.
- Keep DeepSeek web inference fail-closed until implemented and safely validated.
- Preserve unsupported protocol subsets and known limitations in release notes.

This report records observed repository state and outstanding work. It does not mark V1.0.0 accepted or authorize promotion to main.