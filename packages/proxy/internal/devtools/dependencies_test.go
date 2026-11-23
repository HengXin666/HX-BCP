package devtools

import (
	"flag"
	"os"
	"os/exec"
	"testing"
)

var dependencyProxy = flag.String(
	"dependency-proxy",
	"",
	"optional HTTP proxy used by the nested Go command while bootstrapping module dependencies",
)

var moduleProxy = flag.String(
	"module-proxy",
	"",
	"optional GOPROXY value used by the nested Go command while bootstrapping module dependencies",
)

func TestBootstrapDependencies(t *testing.T) {
	if *dependencyProxy == "" && *moduleProxy == "" {
		t.Skip("set -dependency-proxy or -module-proxy to explicitly bootstrap dependencies")
	}

	command := exec.Command("go", "test", "-mod=mod", "./internal/store")
	command.Dir = "../.."
	command.Env = append(os.Environ(), "NO_PROXY=127.0.0.1,localhost")
	if *dependencyProxy != "" {
		command.Env = append(command.Env,
			"HTTP_PROXY="+*dependencyProxy,
			"HTTPS_PROXY="+*dependencyProxy,
			"ALL_PROXY="+*dependencyProxy,
		)
	}
	if *moduleProxy != "" {
		command.Env = append(command.Env, "GOPROXY="+*moduleProxy)
	}
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bootstrap module dependencies: %v\n%s", err, output)
	}
}
