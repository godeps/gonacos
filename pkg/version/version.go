// Package version exposes the build-time identity of a gonacos binary.
// Values are injected via -ldflags at build time:
//
//	go build -ldflags \
//	  "-X github.com/godeps/gonacos/pkg/version.Version=1.0.0 \
//	   -X github.com/godeps/gonacos/pkg/version.Commit=$(git rev-parse --short HEAD) \
//	   -X github.com/godeps/gonacos/pkg/version.BuildDate=$(date -u +%Y-%m-%dT%H:%M:%SZ)" \
//	  ./cmd/gonacos
//
// When unset (e.g., `go run` or `go build` without ldflags), the defaults
// reveal that the binary is a development build so operators can distinguish
// a release from a local build via the /metrics endpoint.
package version

// Version is the human-readable release version. Defaults to a dev tag;
// release builds override it via -ldflags.
var Version = "0.1.0-dev"

// Commit is the short SHA of the git commit this binary was built from.
// Defaults to "unknown" when not injected.
var Commit = "unknown"

// BuildDate is the UTC build timestamp in RFC3339. Defaults to "unknown"
// when not injected.
var BuildDate = "unknown"
