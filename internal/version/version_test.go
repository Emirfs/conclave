package version

import "testing"

// The default must never look like a release: an unstamped build compared
// against a published tag would offer an update on every start.
func TestUnstampedBuildIsNotARelease(t *testing.T) {
	original := Version
	defer func() { Version = original }()

	Version = "dev"
	if IsRelease() {
		t.Error("an unstamped build reported itself as a release")
	}
	Version = ""
	if IsRelease() {
		t.Error("an empty version reported itself as a release")
	}
	Version = "0.2.0"
	if !IsRelease() {
		t.Error("a stamped build did not report itself as a release")
	}
}
