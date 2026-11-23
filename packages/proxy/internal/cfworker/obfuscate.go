package cfworker

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// obfuscateRecipes are the HX-CF-Tunnel obfuscation recipes (obfuscate.sh).
// Ordered smallest→largest artifact: the free CF plan caps worker uploads
// (~10MiB empirical), so the fleet tries light first to guarantee deploys
// succeed; a blocked recipe falls through to the next. 20260823 live test:
// light/dict (~10MB) upload OK, std (~16MB) rejected.
var obfuscateRecipes = []string{"light", "dict", "high", "std"}

// ObfuscateOptions points at the HX-CF-Tunnel scripts and a scratch dir.
type ObfuscateOptions struct {
	TemplatePath string // plain/cfnew worker source (HX-CF-Tunnel dist-obf)
	ScriptPath   string // HX-CF-Tunnel scripts/obfuscate.sh
	OutputDir    string // scratch dir for obfuscated artifacts
}

// ObfuscateWorker runs obfuscate.sh with a randomly chosen recipe, retrying
// with another recipe until the artifact exists (20260823: each deploy uses a
// random obfuscation so no two workers share a fingerprint).
func ObfuscateWorker(ctx context.Context, opts ObfuscateOptions, rng func(int) int) (string, error) {
	scratch := opts.OutputDir
	if scratch == "" {
		scratch = filepath.Join(os.TempDir(), "hx-cfworker-obf")
	}
	if err := os.MkdirAll(scratch, 0o755); err != nil {
		return "", fmt.Errorf("混淆目录创建失败: %w", err)
	}
	templatePath := ResolveAssetPath(opts.TemplatePath, "cfworker/worker.js")
	plain := filepath.Join(scratch, "tpl.plain.js")
	if _, err := os.Stat(plain); err != nil {
		template, readErr := os.ReadFile(templatePath)
		if readErr != nil {
			return "", fmt.Errorf("读取 worker 模板 %s 失败: %w", opts.TemplatePath, readErr)
		}
		if err := os.WriteFile(plain, template, 0o644); err != nil {
			return "", err
		}
	}
	var lastErr error
	for attempt := 0; attempt < len(obfuscateRecipes); attempt++ {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		recipe := obfuscateRecipes[rng(len(obfuscateRecipes))]
		artifact := filepath.Join(scratch, "tpl.plain."+recipe+".js")
		command := exec.CommandContext(ctx, "bash", ResolveAssetPath(opts.ScriptPath, "cfworker/obfuscate.sh"), plain, scratch, recipe)
		if output, err := command.CombinedOutput(); err != nil {
			lastErr = fmt.Errorf("混淆(%s)失败: %s", recipe, strings.TrimSpace(string(output)))
			continue
		}
		if info, err := os.Stat(artifact); err == nil && info.Size() > 1000 {
			return artifact, nil
		}
		lastErr = fmt.Errorf("混淆(%s)未产出文件", recipe)
	}
	return "", fmt.Errorf("混淆全部失败: %v", lastErr)
}
