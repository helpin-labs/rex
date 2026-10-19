# Desktop releases

A push to `main` runs the Desktop release workflow. Semantic-release analyzes
conventional commits since the last `v*` release: `fix:` makes a patch, `feat:` a
minor, and `!` or a `BREAKING CHANGE:` footer a major release. Documentation and
chore commits alone do not publish. Without a previous release, the first version
is 1.0.0. Preserve these prefixes in squash-merge titles.

The workflow plans a version without creating a tag, stamps the Node, Tauri and
Cargo manifests only in each build workspace, and builds Windows x64 and macOS
Apple Silicon installers from the same source commit. Host/UI tests, bundled
services, signatures and the Windows installed-GUI lifecycle must pass. A failed
platform blocks publication. When no semantic release is due, neither platform
is built.

The publisher verifies each installer against its version, source commit and
SHA-256 manifest, uploads both installers, the public Windows certificate and
`SHA256SUMS` to a draft release, then publishes it with a `vVERSION` tag. It does
not publish an npm package or commit generated version changes. Download all
installers from that GitHub release, rather than mixing workflow artifacts.

## Preview signatures

No certificate secrets are needed yet. Each Windows release job generates a
non-exportable RSA code-signing key in the disposable runner's current-user
certificate store. It signs Rex's host, runtime, embedding server, desktop
executable and NSIS installer. Only the public `.cer` is uploaded. Verification temporarily trusts it in the
elevated, disposable runner's machine root store, avoiding the interactive user
trust prompt. The key and runner trust are removed at job completion. The certificate changes
between builds and signatures have no trusted timestamp; they are preview
signatures, not a trusted publisher identity or a SmartScreen exemption.

On Windows, compare the installer with the release's `SHA256SUMS` using
`Get-FileHash -Algorithm SHA256`. Use `Get-AuthenticodeSignature` to inspect the
signer; its thumbprint must match the public certificate, which can be inspected
with `certutil -dump PATH.cer`. A fresh PC will report an untrusted certificate
chain. Do not install a preview certificate as a root just to dismiss the warning.
SmartScreen may require More info → Run anyway where local policy permits it.

macOS uses ad-hoc signing for the app and DMG, without notarization. Check the
download with `shasum -a 256 PATH.dmg`; `codesign --verify PATH.dmg` verifies its
signature. After dragging Rex into Applications, a first launch on another Mac
may require System Settings → Privacy & Security → Open Anyway. Managed machines
can block these preview installers.

Trusted distribution later requires a protected Windows signing identity with
timestamping, plus an Apple Developer ID and notarization. Self-signing does not
replace those steps.

## Verification and recovery

Run `pnpm test:release` for version planning, manifest synchronization and artifact
validation tests. A PR runs native Go tests and UI validation on both operating systems, plus
Windows runtime, browser and signing checks, without building installers. New PR
commits cancel obsolete PR checks. For native-shell or packaging changes, run a
manual candidate build before merge. Each platform workflow's manual dispatch accepts an optional stable `release_version` to exercise
preview signing and packaging on a feature branch; it uploads candidate artifacts
and never publishes. macOS packaging currently targets Apple Silicon; Windows
targets x64. Windows 10 and a native ARM64 Windows installer remain unverified.

If publication fails after a draft is created, use **Re-run failed jobs** on the
original workflow run. The publish job resumes only a draft for the same source
commit; it refuses to overwrite a public release. Keep the original build
artifacts available (30-day retention for release candidates). Starting a new full workflow after
tag creation is not an automatic draft-recovery mechanism. Resolve an abandoned
draft and its tag explicitly before planning another release.

## Artifact promotion and build cost

Release installers are named by platform, final version and source commit. The
publisher downloads and validates their original bytes; it does not compile,
repackage or sign. Creating a release tag does not trigger another build.

A new main workflow run reuses an available platform artifact from a completed
main run of the release workflow or its matching platform workflow for the exact
commit and version. A previously failed overall run can supply its successfully
validated platform. PR runs, other branches, other workflow identities, expired
artifacts and different versions/commits are excluded. Both downloaded manifests
and SHA-256 checksums are still verified before publication. A changed source or
final version needs new installers; development-version PR binaries are not
release candidates. Artifacts are retained for 30 days; after expiry, rebuilding
is necessary.

Windows candidate builds compile each host/runtime once and run the shared host
checks there. The selected Go test subset was redundant with all-host tests and
was removed. PRs retain a runtime-only smoke; candidates use the full sidecar
smoke instead. Chrome/Edge native-pipe, cookie handoff, ARM64 cross-build, WSL
confinement, installer lifecycle and signature checks remain. Tauri builds the UI
once, saves compiled Rust dependencies before bundling, and then signs/packages
that executable. macOS also saves compiled dependencies before bundling.

The Windows WSL image cache is keyed by the Go dependency manifests and its
build script. Reuse checks the image checksum, architecture and dependency/script fingerprint; invalid cached
images are rebuilt. Changing the script or pinned runtime refreshes the image.
Bump the script when refreshing the Alpine packages in the base image.
