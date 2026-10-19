# Build once and promote desktop artifacts

## Objective

Keep fast native and UI checks on PRs. Produce each final-version installer once for a trusted source commit, then publish its verified bytes. Tags and publication retries must not compile again.

## Implementation order

1. Keep the PowerShell 7 signing fix and its captured-child regression on the existing branch.
2. Skip both release platform jobs when semantic-release finds no new version. Reuse completed platform artifacts for the same commit and final version from trusted main-branch runs; rebuild only a missing platform. Keep source/version/checksum validation before publication and refuse PR artifacts. Tags remain outputs of publication, not build triggers.
3. Make ordinary PRs run native Go tests, runtime/browser smoke checks and UI validation. Full packaging remains available through manual candidate builds and runs for actual main releases. Remove the obsolete feature-branch push triggers and cancel superseded PR checks.
4. Run each Windows host/runtime build once per candidate. Share the meaningful Windows host checks with candidate packaging; remove the selected Go test subset already covered by all-host tests and the runtime-only smoke where the full sidecar check already covers it. Keep Chrome and Edge native-pipe checks, cookie handoff, all host tests, ARM64 cross-compilation, WSL confinement, installed GUI lifecycle and signing verification.
5. Compile Tauri without bundling, save Rust dependencies, then bundle/sign the existing executable. Eliminate the extra Windows UI build. Cache and checksum-check the WSL image against the pinned dependency/build-script inputs.
6. Add release artifact-selection tests for version/commit/platform matching, expired artifacts, untrusted runs and partial reuse. Exercise reuse with mocked GitHub responses and preserve the existing artifact tamper/mismatched-source tests. Validate workflow syntax and shell scripts locally, run release-tool tests, then push to the same branch and update PR #11.

## Boundaries and recovery

- Final installer artifacts use commit/version/platform names and retain their manifests for 30 days. Caches accelerate compilation but are not publication evidence.
- Reuse only artifacts uploaded by the allowed packaging workflows on main after their platform checks. Re-run failed jobs to resume the same release. A new whole run can reuse a successfully validated platform from a prior failed run for the same source and version.
- PRs are not final-version release candidates; native-shell/installer changes should receive a manual signed candidate build before merge. A merge that changes the source or planned version needs a new candidate.
- Preserve the current draft-release checks; do not merge, publish a release or change runtime/application behavior in this task.

## Verification record

Implemented on `fix/windows-release-signing`, extending PR #11.

- All 20 release-tool tests passed: semantic versions, asset integrity, trusted exact-build discovery, rejected provenance, partial reuse and publish/build conditions.
- actionlint 1.7.7 passed for all three modified workflows.
- Windows VM tests passed for PowerShell syntax, valid WSL cache reuse and rejection of tampered images, wrong architectures, malformed metadata and changed dependencies.
- Standalone `tauri bundle` produced an app and DMG from the existing isolated macOS build without compilation. The resulting app passed deep/strict signature verification.
- The real GitHub artifact-discovery CLI completed read-only with no matching artifacts, validating its API request and output contract. Positive reuse is covered with mocked GitHub responses; end-to-end trusted-main reuse requires retained artifacts after merge.
- Application and agent-runtime Go/Rust source were not changed. The hosted Windows candidate at the earlier signing-fix commit completed signing, GUI, reinstall and uninstall/data-retention assertions; its final directory cleanup raced the uninstaller and marked the job failed. Cleanup now reports warnings without overriding functional results. Final workflow packaging and main-only publication/reuse must be distinguished from local checks.


### Browser readiness follow-up

The hosted cookie-handoff check timed out querying Storage.getCookies during startup. The fixture now waits for its delayed login response to cause an authenticated browser request, then closes the plain browser and verifies the bot inherits that same HttpOnly cookie. This retains the 30-second deadline and native-pipe check without querying cookie storage during initialization.

Real-browser checks passed twice on macOS and twice in the Windows VM. A third Windows iteration reached the later agent-browser launch and timed out; the VM subsequently stopped accepting SSH. Hosted confirmation of the final test change remains required. Browser application code is unchanged.
