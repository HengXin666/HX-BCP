package cfworker

import (
	"os"
	"path/filepath"
	"strings"
)

// ResolveAssetPath locates a fleet deploy asset (worker template or
// obfuscation script). Resolution order:
//  1. the configured absolute path, when it exists (development machine,
//     HX-CF-Tunnel checkout);
//  2. otherwise a path relative to the executable — the packaged layout
//     <install-root>/current/cfworker/<name> shipped by package-release.sh
//     (20260823: production lives behind Cloudflare with no direct SSH, so
//     the deploy assets travel inside the release bundle).
func ResolveAssetPath(configured, packaged string) string {
	if configured != "" {
		abs := configured
		if !filepath.IsAbs(abs) {
			abs = filepath.Join(executableDir(), abs)
		}
		if info, err := os.Stat(abs); err == nil && !info.IsDir() {
			return abs
		}
	}
	candidate := filepath.Join(executableDir(), packaged)
	if info, err := os.Stat(candidate); err == nil && !info.IsDir() {
		return candidate
	}
	return configured
}

// assetExists reports whether the packaged asset is present next to the
// executable, tolerating a configured path that points elsewhere.
func assetExists(packaged string) bool {
	candidate := filepath.Join(executableDir(), packaged)
	info, err := os.Stat(candidate)
	return err == nil && !info.IsDir()
}

func executableDir() string {
	exe, err := os.Executable()
	if err != nil {
		return "."
	}
	resolved, err := filepath.EvalSymlinks(exe)
	if err == nil {
		exe = resolved
	}
	dir := filepath.Dir(exe)
	// <current>/cfworker/… where <current> is a symlink resolved above
	return strings.TrimSuffix(dir, string(filepath.Separator))
}
