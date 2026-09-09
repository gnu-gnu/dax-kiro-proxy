// Package notices retains reviewed notice texts with the development executable.
package notices

import "embed"

// Files includes the explicitly labeled historical-license reference. It does not imply
// release clearance. Test-only runtime notices are not installed with the ordinary executable.
//
//go:embed runtime/*.txt reference/*.txt
var Files embed.FS
