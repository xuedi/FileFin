// Package version records the FileFin release version - the single source of truth for the
// number shown in the UI and attached to a release build.
package version

// Version is the current release, following semantic versioning. It is a plain constant
// rather than a build-time -ldflags stamp so every build path (go build, the cross-compiled
// release binaries, the deploy script) reports the same number without extra flags. Bump it
// here and in the README badge when cutting a release.
const Version = "0.7.1"
