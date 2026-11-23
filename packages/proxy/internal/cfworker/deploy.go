package cfworker

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// neutralWords are business-sounding names that avoid proxy/risk keywords.
var neutralWords = []string{
	"aurora", "breeze", "cascade", "dune", "ember", "falcon", "glacier",
	"harbor", "iris", "juniper", "keystone", "lumen", "meadow", "nimbus",
	"onyx", "pebble", "quartz", "ridge", "solstice", "tundra", "umbra",
	"vale", "willow", "xenon", "yarrow", "zenith", "alder", "birch",
}

// AccountCredentials is what the fleet knows about a CF account.
type AccountCredentials struct {
	Email     string
	AccountID string
	APIToken  string
}

// DeployResult describes a freshly deployed worker.
type DeployResult struct {
	Name         string
	CanonicalURL string
}

// DeployOptions carries deploy-time configuration (HX-CF-Tunnel paths).
type DeployOptions struct {
	Obfuscate ObfuscateOptions
	Random    func(int) int
}

// NeutralName generates an unused neutral worker name: <word><2-digit>.
func NeutralName(existing map[string]bool) string {
	for attempt := 0; attempt < 200; attempt++ {
		word := neutralWords[randomInt(len(neutralWords))]
		name := fmt.Sprintf("%s%d", word, 10+randomInt(90))
		if !existing[name] {
			return name
		}
	}
	return fmt.Sprintf("node%d", randomInt(1_000_000))
}

func randomHex(length int) string {
	var out strings.Builder
	for i := 0; i < length; i++ {
		out.WriteByte("0123456789abcdef"[randomInt(16)])
	}
	return out.String()
}

func randomInt(max int) int {
	if max <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(max)))
	if err != nil {
		return 0
	}
	return int(value.Int64())
}

const tokenAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomToken(length int) string {
	var out strings.Builder
	for i := 0; i < length; i++ {
		out.WriteByte(tokenAlphabet[randomInt(len(tokenAlphabet))])
	}
	return out.String()
}

// Deploy deploys one HX-CF-Tunnel style worker (cfnew template + random
// obfuscation recipe per deploy) and returns its subscription URL. On failure
// it retries with a fresh name and another obfuscation recipe, so a blocked
// recipe never leaves the fleet short (20260823 user decision: depend on
// HX-CF-Tunnel for the worker + obfuscation, random per deploy).
func Deploy(
	ctx context.Context,
	client *Client,
	account AccountCredentials,
	opts DeployOptions,
) (DeployResult, error) {
	random := opts.Random
	if random == nil {
		random = randomInt
	}
	subdomain, err := client.EnsureSubdomain(ctx, account.AccountID, "")
	if err != nil {
		return DeployResult{}, fmt.Errorf("确保子域失败: %w", err)
	}
	workers, err := client.ListWorkers(ctx, account.AccountID)
	if err != nil {
		return DeployResult{}, fmt.Errorf("列出 worker 失败: %w", err)
	}
	existing := make(map[string]bool, len(workers))
	for _, worker := range workers {
		if worker.ID != "" {
			existing[worker.ID] = true
		}
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		if err := ctx.Err(); err != nil {
			return DeployResult{}, err
		}
		name := NeutralName(existing)
		existing[name] = true
		result, err := deployOnce(ctx, client, account, subdomain, name, opts, random)
		if err == nil {
			return result, nil
		}
		lastErr = err
		_ = client.DeleteWorker(ctx, account.AccountID, name)
		// obfuscation is randomized: a broken output must not be reused,
		// drop the cached artifacts so the next attempt re-obfuscates fresh.
		clearObfuscatedArtifacts(opts.Obfuscate.OutputDir)
	}
	// Fallback: deploy the template as-is. It is the official obfuscated
	// cfnew build (~1.7MB, always under the free-plan size cap) and the
	// proven-good path — this guarantees the fleet never stays short because
	// of an oversized/broken randomized obfuscation (20260823).
	if opts.Obfuscate.TemplatePath != "" || assetExists("cfworker/worker.js") {
		name := NeutralName(existing)
		existing[name] = true
		result, fallbackErr := deployRaw(ctx, client, account, subdomain, name, opts.Obfuscate.TemplatePath)
		if fallbackErr == nil {
			return result, nil
		}
		lastErr = fmt.Errorf("%v; 模板兜底也失败: %w", lastErr, fallbackErr)
		_ = client.DeleteWorker(ctx, account.AccountID, name)
	}
	return DeployResult{}, fmt.Errorf("部署 worker 重试 4 次+兜底仍失败: %v", lastErr)
}

func deployOnce(
	ctx context.Context,
	client *Client,
	account AccountCredentials,
	subdomain, name string,
	opts DeployOptions,
	random func(int) int,
) (DeployResult, error) {
	artifact, err := ObfuscateWorker(ctx, opts.Obfuscate, random)
	if err != nil {
		return DeployResult{}, err
	}
	script, err := os.ReadFile(artifact)
	if err != nil {
		return DeployResult{}, fmt.Errorf("读取混淆产物失败: %w", err)
	}
	// cfnew-compatible worker: env-var configuration (u=uuid, d=path).
	// d MUST be lowercase hex (uuid4().hex[:12]): the worker only routes
	// lowercase-hex custom paths — mixed-case paths 404 (20260823 live test).
	uuidVal := randomUUID()
	pathVal := "/" + randomHex(12)
	variables := map[string]string{"u": uuidVal, "d": pathVal}
	if err := client.UploadWorker(ctx, account.AccountID, name, script, "", variables); err != nil {
		return DeployResult{}, fmt.Errorf("上传 worker 失败: %w", err)
	}
	if err := client.EnableScriptSubdomain(ctx, account.AccountID, name); err != nil {
		return DeployResult{}, fmt.Errorf("启用子域失败: %w", err)
	}
	workerURL := "https://" + name + "." + subdomain + ".workers.dev"
	canonical := workerURL + pathVal + "/sub?app=xray"
	// verify the subscription actually serves nodes; CF propagation takes a
	// few seconds after upload (20260823: some randomized obfuscation outputs
	// are broken — deploy must verify and let the retry re-obfuscate).
	time.Sleep(6 * time.Second)
	ok, detail := ProbeSubscription(ctx, canonical, 20*time.Second)
	if !ok {
		return DeployResult{}, fmt.Errorf("部署后验证失败: %s", detail)
	}
	return DeployResult{
		Name:         name,
		CanonicalURL: canonical,
	}, nil
}

func deployRaw(
	ctx context.Context,
	client *Client,
	account AccountCredentials,
	subdomain, name, templatePath string,
) (DeployResult, error) {
	templatePath = ResolveAssetPath(templatePath, "cfworker/worker.js")
	script, err := os.ReadFile(templatePath)
	if err != nil {
		// embedded fallback: the cfnew template ships inside the binary
		script = embeddedWorkerTemplate
	}
	pathVal := "/" + randomHex(12)
	variables := map[string]string{"u": randomUUID(), "d": pathVal}
	if err := client.UploadWorker(ctx, account.AccountID, name, script, "", variables); err != nil {
		return DeployResult{}, fmt.Errorf("上传模板 worker 失败: %w", err)
	}
	if err := client.EnableScriptSubdomain(ctx, account.AccountID, name); err != nil {
		return DeployResult{}, fmt.Errorf("启用子域失败: %w", err)
	}
	canonical := "https://" + name + "." + subdomain + ".workers.dev" + pathVal + "/sub?app=xray"
	time.Sleep(6 * time.Second)
	ok, detail := ProbeSubscription(ctx, canonical, 20*time.Second)
	if !ok {
		return DeployResult{}, fmt.Errorf("模板 worker 验证失败: %s", detail)
	}
	return DeployResult{Name: name, CanonicalURL: canonical}, nil
}

func clearObfuscatedArtifacts(scratch string) {
	if scratch == "" {
		return
	}
	entries, err := os.ReadDir(scratch)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || name == "tpl.plain.js" {
			continue
		}
		if strings.HasSuffix(name, ".js") || strings.HasSuffix(name, ".err") {
			_ = os.Remove(filepath.Join(scratch, name))
		}
	}
}
