// Package version reports which build of Conclave is running.
//
// The value is stamped in at link time from the release tag; a build made
// without that stamp says so plainly rather than claiming a release number it
// does not have. An honest "dev" is what keeps the update check from offering
// a developer an update over their own working tree.
package version

// Version is set with -ldflags "-X github.com/Emirfs/conclave/internal/version.Version=0.2.0".
var Version = "dev"

// IsRelease reports whether this build carries a real release number. Only a
// released build has a version the update check can compare against.
func IsRelease() bool { return Version != "dev" && Version != "" }
