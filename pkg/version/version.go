package version

// Values for these are injected by the build
var (
	version string
	commit  string
)

// Version returns the Actions version. This is either a semantic version
// number or else, in the case of unreleased code, the string "devel".
func Version() string {
	return version
}

// Commit returns the git commit SHA for the code that Actions was built from.
func Commit() string {
	return commit
}
