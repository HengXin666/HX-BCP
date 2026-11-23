package cfworker

import _ "embed"

// Embedded deploy assets: the cfnew worker template and the HX-CF-Tunnel
// obfuscation script ship inside the binary itself, so any installation —
// regardless of how it was upgraded — can deploy the fleet without extra
// files (20260823: production upgraded without the packaged cfworker/ dir,
// leaving the template missing and every deploy failing).
//
//go:embed assets/worker.js
var embeddedWorkerTemplate []byte

//go:embed assets/obfuscate.sh
var embeddedObfuscateScript []byte
