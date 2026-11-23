package hxpxy

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/HengXin666/hx-pxy/internal/systemsettings"
)

func TestRunScriptSyntaxAndHelp(t *testing.T) {
	t.Parallel()

	command := exec.Command("bash", "-n", "run.sh")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("bash -n run.sh failed: %v\n%s", err, output)
	}

	command = exec.Command("bash", "run.sh", "--help")
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("run.sh --help failed: %v\n%s", err, output)
	}
	help := string(output)
	for _, expected := range []string{
		"--backend-only",
		"--require-frontend",
		"--install-frontend-deps",
		"--no-install-frontend-deps",
		"--mihomo",
		"Ctrl+C stops all child processes",
		"random loopback high port",
		"ports 49152-65535",
	} {
		if !strings.Contains(help, expected) {
			t.Errorf("run.sh help is missing %q", expected)
		}
	}
}

func TestRunScriptUsesAndReleasesRandomHighPorts(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "run.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create run log: %v", err)
	}
	defer logFile.Close()

	command := exec.Command(
		"bash",
		"run.sh",
		"--data-dir", t.TempDir(),
	)
	command.Stdout = logFile
	command.Stderr = logFile
	if err := command.Start(); err != nil {
		t.Fatalf("run.sh Start() error = %v", err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	stopped := false
	defer func() {
		if stopped {
			return
		}
		_ = command.Process.Signal(syscall.SIGTERM)
		select {
		case <-waitResult:
		case <-time.After(5 * time.Second):
			_ = command.Process.Kill()
			<-waitResult
		}
	}()

	backendPattern := regexp.MustCompile(`starting backend at http://127\.0\.0\.1:([0-9]+)`)
	frontendPattern := regexp.MustCompile(`starting frontend at http://127\.0\.0\.1:([0-9]+)`)
	backendPort := 0
	frontendPort := 0
	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	deadline := time.Now().Add(90 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-waitResult:
			stopped = true
			content, _ := os.ReadFile(logPath)
			t.Fatalf("run.sh exited before random ports were ready: %v\n%s", waitErr, content)
		default:
		}

		content, readErr := os.ReadFile(logPath)
		if readErr != nil {
			t.Fatalf("read run log: %v", readErr)
		}
		if backendPort == 0 {
			backendPort = capturedPort(t, backendPattern, content)
		}
		if frontendPort == 0 {
			frontendPort = capturedPort(t, frontendPattern, content)
		}
		if backendPort != 0 && frontendPort != 0 {
			response, requestErr := client.Get(fmt.Sprintf("http://127.0.0.1:%d/health/ready", frontendPort))
			if requestErr == nil {
				response.Body.Close()
				if response.StatusCode == http.StatusOK {
					ready = true
					break
				}
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		content, _ := os.ReadFile(logPath)
		t.Fatalf("random backend and frontend did not become ready\n%s", content)
	}

	for name, port := range map[string]int{"backend": backendPort, "frontend": frontendPort} {
		if port < 49152 || port > 65535 {
			t.Fatalf("%s port = %d, want random high port", name, port)
		}
	}
	if backendPort == frontendPort {
		t.Fatalf("backend and frontend selected the same port %d", backendPort)
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatalf("signal run.sh: %v", err)
	}
	select {
	case <-waitResult:
		stopped = true
	case <-time.After(12 * time.Second):
		t.Fatal("run.sh did not clean up random-port process groups")
	}

	for name, port := range map[string]int{"backend": backendPort, "frontend": frontendPort} {
		listener, listenErr := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
		if listenErr != nil {
			t.Fatalf("%s port %d was not released: %v", name, port, listenErr)
		}
		if closeErr := listener.Close(); closeErr != nil {
			t.Fatalf("close %s port probe: %v", name, closeErr)
		}
	}
}

func capturedPort(t *testing.T, pattern *regexp.Regexp, content []byte) int {
	t.Helper()
	match := pattern.FindSubmatch(content)
	if len(match) != 2 {
		return 0
	}
	port, err := strconv.Atoi(string(match[1]))
	if err != nil {
		t.Fatalf("parse captured port %q: %v", match[1], err)
	}
	return port
}

func TestRunScriptStartsAndStopsBackend(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("listener Close() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.Command(
		"bash",
		"run.sh",
		"--backend-only",
		"--listen", address,
		"--data-dir", t.TempDir(),
	)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("run.sh Start() error = %v", err)
	}

	waitResult := make(chan error, 1)
	go func() {
		waitResult <- command.Wait()
	}()

	client := &http.Client{
		Timeout: 250 * time.Millisecond,
		Transport: &http.Transport{
			Proxy: nil,
		},
	}
	healthURL := "http://" + address + "/health/ready"
	deadline := time.Now().Add(90 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-waitResult:
			t.Fatalf("run.sh exited before ready: %v\nstdout:\n%s\nstderr:\n%s", waitErr, stdout.String(), stderr.String())
		default:
		}
		response, requestErr := client.Get(healthURL)
		if requestErr == nil {
			response.Body.Close()
			if response.StatusCode == http.StatusOK {
				ready = true
				break
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		_ = command.Process.Kill()
		<-waitResult
		t.Fatalf("backend did not become ready\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		_ = command.Process.Kill()
		<-waitResult
		t.Fatalf("signal run.sh: %v", err)
	}
	select {
	case <-waitResult:
	case <-time.After(5 * time.Second):
		_ = command.Process.Kill()
		<-waitResult
		t.Fatal("run.sh did not stop after SIGTERM")
	}

	response, requestErr := client.Get(healthURL)
	if requestErr == nil {
		response.Body.Close()
		// The port was chosen by bind-then-release, so a parallel test's
		// httptest server can win the reuse window on shared CI runners.
		// Only a 200 health response proves the backend is still alive; any
		// other responder is a port-reuse artifact, not our process.
		if response.StatusCode == http.StatusOK {
			t.Fatal("backend still accepts requests after run.sh stopped")
		}
		t.Logf("port %s answered %d after run.sh stopped; assumed port-reuse artifact", address, response.StatusCode)
	}
	runLog, err := os.ReadFile(filepath.Join(".tmp", "run", "run.log"))
	if err != nil {
		t.Fatalf("read local run log: %v", err)
	}
	if !strings.Contains(string(runLog), "local run stopping") || !strings.Contains(string(runLog), "local run exited") {
		t.Fatalf("local run log does not contain lifecycle events:\n%s", runLog)
	}
	for _, path := range []string{
		filepath.Join(".tmp", "run", "run.log"),
		filepath.Join(".tmp", "run", "logs", "backend.log"),
		filepath.Join(".tmp", "run", "logs", "frontend.log"),
	} {
		info, statErr := os.Stat(path)
		if statErr != nil {
			t.Fatalf("stat local log %q: %v", path, statErr)
		}
		if info.Mode().Perm() != 0o600 {
			t.Fatalf("local log %q mode = %o, want 600", path, info.Mode().Perm())
		}
	}
}

func TestRunScriptRejectsOccupiedBackendAddressBeforeBuild(t *testing.T) {
	t.Parallel()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	command := exec.Command(
		"bash",
		"run.sh",
		"--backend-only",
		"--listen", listener.Addr().String(),
		"--binary", filepath.Join(t.TempDir(), "missing-hx-pxyd"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("run.sh unexpectedly succeeded\n%s", output)
	}
	message := string(output)
	if !strings.Contains(message, "backend address "+listener.Addr().String()+" is already in use") {
		t.Fatalf("run.sh output = %q, want occupied-address error", message)
	}
	if strings.Contains(message, "binary does not exist") || strings.Contains(message, "building backend") {
		t.Fatalf("run.sh did not reject the occupied address before build preparation: %q", message)
	}
}

func TestBackendRejectsOccupiedAddressBeforeOpeningPersistentState(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen() error = %v", err)
	}
	defer listener.Close()

	buildDirectory := t.TempDir()
	binary := filepath.Join(buildDirectory, "hx-pxyd")
	build := exec.Command("go", "build", "-o", binary, "./cmd/hx-pxyd")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build backend: %v\n%s", err, output)
	}

	dataDirectory := t.TempDir()
	databasePath := filepath.Join(dataDirectory, "hx-pxy.db")
	masterKeyPath := filepath.Join(dataDirectory, "master.key")
	command := exec.Command(
		binary,
		"--listen", listener.Addr().String(),
		"--data-dir", dataDirectory,
		"--config", filepath.Join(dataDirectory, "config.yaml"),
		"--database", databasePath,
		"--master-key", masterKeyPath,
		"--runtime-config", filepath.Join(dataDirectory, "runtime", "active.yaml"),
		"--snapshots", filepath.Join(dataDirectory, "snapshots"),
	)
	output, err := command.CombinedOutput()
	if err == nil {
		t.Fatalf("backend unexpectedly succeeded\n%s", output)
	}
	if !strings.Contains(string(output), "listen on management API address "+listener.Addr().String()) {
		t.Fatalf("backend output = %q, want occupied-address error", output)
	}
	for _, unexpected := range []string{databasePath, masterKeyPath} {
		if _, statErr := os.Stat(unexpected); !os.IsNotExist(statErr) {
			t.Fatalf("persistent state %q exists after early listen failure (Stat error = %v)", unexpected, statErr)
		}
	}
}

func TestRunScriptStartsFrontendAndProxiesBackend(t *testing.T) {
	backendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("backend Listen() error = %v", err)
	}
	backendAddress := backendListener.Addr().String()
	if err := backendListener.Close(); err != nil {
		t.Fatalf("backend listener Close() error = %v", err)
	}
	frontendListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("frontend Listen() error = %v", err)
	}
	frontendPort := frontendListener.Addr().(*net.TCPAddr).Port
	if err := frontendListener.Close(); err != nil {
		t.Fatalf("frontend listener Close() error = %v", err)
	}

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command := exec.Command(
		"bash",
		"run.sh",
		"--listen", backendAddress,
		"--frontend-host", "127.0.0.1",
		"--frontend-port", fmt.Sprintf("%d", frontendPort),
		"--data-dir", t.TempDir(),
	)
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		t.Fatalf("run.sh Start() error = %v", err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()

	client := &http.Client{Timeout: 500 * time.Millisecond, Transport: &http.Transport{Proxy: nil}}
	frontendURL := fmt.Sprintf("http://127.0.0.1:%d", frontendPort)
	deadline := time.Now().Add(90 * time.Second)
	ready := false
	for time.Now().Before(deadline) {
		select {
		case waitErr := <-waitResult:
			t.Fatalf("run.sh exited before frontend ready: %v\nstdout:\n%s\nstderr:\n%s", waitErr, stdout.String(), stderr.String())
		default:
		}
		pageResponse, pageErr := client.Get(frontendURL + "/")
		proxyResponse, proxyErr := client.Get(frontendURL + "/health/ready")
		if pageErr == nil {
			pageResponse.Body.Close()
		}
		if proxyErr == nil {
			proxyResponse.Body.Close()
		}
		if pageErr == nil && proxyErr == nil && pageResponse.StatusCode == http.StatusOK && proxyResponse.StatusCode == http.StatusOK {
			ready = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !ready {
		_ = command.Process.Kill()
		<-waitResult
		t.Fatalf("frontend or proxy did not become ready\nstdout:\n%s\nstderr:\n%s", stdout.String(), stderr.String())
	}

	client.Timeout = 2 * time.Second
	createRequest, err := http.NewRequest(
		http.MethodPost,
		frontendURL+"/api/v1/subscriptions",
		bytes.NewBufferString(`{"name":"run-e2e","source_type":"inline","source_config":{"inline":"vless://11111111-1111-1111-1111-111111111111@run.example.com:443#run"},"refresh_interval_seconds":3600}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	createRequest.Header.Set("Content-Type", "application/json")
	createResponse, err := client.Do(createRequest)
	if err != nil {
		t.Fatalf("create subscription through frontend proxy: %v", err)
	}
	defer createResponse.Body.Close()
	if createResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create subscription status = %d", createResponse.StatusCode)
	}
	var created struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(createResponse.Body).Decode(&created); err != nil {
		t.Fatalf("decode created subscription: %v", err)
	}
	if created.ID == "" {
		t.Fatal("created subscription id is empty")
	}

	refreshRequest, err := http.NewRequest(http.MethodPost, frontendURL+"/api/v1/subscriptions/"+created.ID+"/refresh", nil)
	if err != nil {
		t.Fatal(err)
	}
	refreshResponse, err := client.Do(refreshRequest)
	if err != nil {
		t.Fatalf("refresh subscription through frontend proxy: %v", err)
	}
	refreshResponse.Body.Close()
	if refreshResponse.StatusCode != http.StatusOK {
		t.Fatalf("refresh subscription status = %d", refreshResponse.StatusCode)
	}

	nodeResponse, err := client.Get(frontendURL + "/api/v1/nodes?protocol=vless&state=candidate")
	if err != nil {
		t.Fatalf("list nodes through frontend proxy: %v", err)
	}
	defer nodeResponse.Body.Close()
	if nodeResponse.StatusCode != http.StatusOK {
		t.Fatalf("list nodes status = %d", nodeResponse.StatusCode)
	}
	var nodeList struct {
		Items []struct {
			ID       string `json:"id"`
			Protocol string `json:"protocol"`
		} `json:"items"`
	}
	if err := json.NewDecoder(nodeResponse.Body).Decode(&nodeList); err != nil {
		t.Fatalf("decode node list: %v", err)
	}
	if len(nodeList.Items) != 1 || nodeList.Items[0].ID == "" || nodeList.Items[0].Protocol != "vless" {
		t.Fatalf("unexpected node list: %#v", nodeList.Items)
	}

	groupRequest, err := http.NewRequest(
		http.MethodPost,
		frontendURL+"/api/v1/proxy-groups",
		bytes.NewBufferString(`{"name":"run-direct","strategy":"manual","source_spec":{"node_ids":[],"include_direct":true},"empty_behavior":"fail-closed"}`),
	)
	if err != nil {
		t.Fatal(err)
	}
	groupRequest.Header.Set("Content-Type", "application/json")
	groupResponse, err := client.Do(groupRequest)
	if err != nil {
		t.Fatalf("create proxy group through frontend proxy: %v", err)
	}
	if groupResponse.StatusCode != http.StatusCreated {
		groupResponse.Body.Close()
		t.Fatalf("create proxy group status = %d", groupResponse.StatusCode)
	}
	var createdGroup struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(groupResponse.Body).Decode(&createdGroup); err != nil {
		groupResponse.Body.Close()
		t.Fatalf("decode proxy group: %v", err)
	}
	groupResponse.Body.Close()
	if createdGroup.ID == "" {
		t.Fatal("created proxy group id is empty")
	}

	// The shared inbound is the default entry point, and a member does not own
	// the socket: its bind address and port are replaced by the family's, and it
	// is selected by the username minted with its credential. So the only way to
	// reach a published service is the way a real consumer does it — read the
	// node list and dial what it says. Dialing the port named in the create
	// request would assert an endpoint nothing listens on.
	proxyListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve shared inbound port: %v", err)
	}
	sharedPort := proxyListener.Addr().(*net.TCPAddr).Port
	if err := proxyListener.Close(); err != nil {
		t.Fatalf("release shared inbound port: %v", err)
	}
	// 见 .agents/notes/implemented/architecture/2026-10-13-upgrade-adopts-pre-rename-installation.md
	// Pin the aggregate Mixed entry point to a port this test owns, so it cannot
	// collide with a data plane another test or the host already runs. This runs
	// after the first group exists because the settings update reconciles the
	// aggregate listeners, which needs a group to anchor to.
	client.Timeout = 10 * time.Second
	settingsResponse, err := client.Get(frontendURL + "/api/v1/settings")
	if err != nil {
		t.Fatalf("read settings through frontend proxy: %v", err)
	}
	var settings systemsettings.Settings
	if decodeErr := json.NewDecoder(settingsResponse.Body).Decode(&settings); decodeErr != nil {
		settingsResponse.Body.Close()
		t.Fatalf("decode settings: %v", decodeErr)
	}
	settingsResponse.Body.Close()
	if settingsResponse.StatusCode != http.StatusOK {
		t.Fatalf("read settings status = %d", settingsResponse.StatusCode)
	}
	settings.SharedInbound.Mode = systemsettings.SharedInboundShared
	settings.SharedInbound.MixedBindAddress = "127.0.0.1"
	settings.SharedInbound.MixedPort = sharedPort
	settingsBody, err := json.Marshal(settings)
	if err != nil {
		t.Fatalf("encode settings: %v", err)
	}
	settingsRequest, err := http.NewRequest(http.MethodPut, frontendURL+"/api/v1/settings", bytes.NewReader(settingsBody))
	if err != nil {
		t.Fatal(err)
	}
	settingsRequest.Header.Set("Content-Type", "application/json")
	settingsUpdate, err := client.Do(settingsRequest)
	if err != nil {
		t.Fatalf("update settings through frontend proxy: %v", err)
	}
	settingsUpdateBody, _ := io.ReadAll(settingsUpdate.Body)
	settingsUpdate.Body.Close()
	if settingsUpdate.StatusCode != http.StatusOK {
		t.Fatalf("update settings status = %d: %s", settingsUpdate.StatusCode, settingsUpdateBody)
	}
	client.Timeout = 2 * time.Second

	// A port is still required by the create contract even though the family
	// owns the socket and replaces it; passing the reserved one keeps the
	// request valid and lets the endpoint assertion below catch a mismatch.
	listenerPayload := fmt.Sprintf(
		`{"name":"run-mixed","kind":"mixed","bind_address":"127.0.0.1","port":%d,"proxy_group_id":%q}`,
		sharedPort,
		createdGroup.ID,
	)
	listenerRequest, err := http.NewRequest(
		http.MethodPost,
		frontendURL+"/api/v1/listeners",
		bytes.NewBufferString(listenerPayload),
	)
	if err != nil {
		t.Fatal(err)
	}
	listenerRequest.Header.Set("Content-Type", "application/json")
	listenerResponse, err := client.Do(listenerRequest)
	if err != nil {
		t.Fatalf("create listener through frontend proxy: %v", err)
	}
	var createdListener struct {
		ID          string `json:"id"`
		SharePath   string `json:"share_path"`
		BindAddress string `json:"bind_address"`
		Port        int    `json:"port"`
	}
	if decodeErr := json.NewDecoder(listenerResponse.Body).Decode(&createdListener); decodeErr != nil {
		listenerResponse.Body.Close()
		t.Fatalf("decode listener: %v", decodeErr)
	}
	listenerResponseBody, _ := io.ReadAll(listenerResponse.Body)
	listenerResponse.Body.Close()
	if listenerResponse.StatusCode != http.StatusCreated {
		t.Fatalf("create listener status = %d: %s", listenerResponse.StatusCode, listenerResponseBody)
	}
	if createdListener.SharePath == "" {
		t.Fatal("created listener has no share_path")
	}
	// A member joined the family, so its stored endpoint is the family's, not the
	// one it was created with.
	if createdListener.BindAddress != "127.0.0.1" || createdListener.Port != sharedPort {
		t.Fatalf("member endpoint = %s:%d, want the shared 127.0.0.1:%d", createdListener.BindAddress, createdListener.Port, sharedPort)
	}

	shareToken := strings.TrimPrefix(createdListener.SharePath, "/sub/")
	consumerResponse, err := client.Get(frontendURL + "/nodes/" + shareToken)
	if err != nil {
		t.Fatalf("read consumer node list: %v", err)
	}
	var consumerPayload struct {
		Nodes []struct {
			Name              string `json:"name"`
			Protocol          string `json:"protocol"`
			Host              string `json:"host"`
			Port              int    `json:"port"`
			BrowserCompatible bool   `json:"browser_compatible"`
			Auth              *struct {
				Username string `json:"username"`
				Password string `json:"password"`
			} `json:"auth"`
		} `json:"nodes"`
	}
	if decodeErr := json.NewDecoder(consumerResponse.Body).Decode(&consumerPayload); decodeErr != nil {
		consumerResponse.Body.Close()
		t.Fatalf("decode consumer node list: %v", decodeErr)
	}
	consumerResponse.Body.Close()
	if consumerResponse.StatusCode != http.StatusOK {
		t.Fatalf("consumer node list status = %d", consumerResponse.StatusCode)
	}
	if len(consumerPayload.Nodes) == 0 {
		t.Fatal("consumer node list is empty")
	}
	dialNode := consumerPayload.Nodes[0]
	if !dialNode.BrowserCompatible {
		t.Fatalf("consumer node %q is not browser-compatible, so it cannot be dialed as an HTTP proxy", dialNode.Name)
	}
	if dialNode.Auth == nil || dialNode.Auth.Username == "" || dialNode.Auth.Password == "" {
		t.Fatal("shared-inbound member was published without a credential")
	}
	if dialNode.Host != "127.0.0.1" || dialNode.Port != sharedPort {
		t.Fatalf("consumer node endpoint = %s:%d, want the shared 127.0.0.1:%d", dialNode.Host, dialNode.Port, sharedPort)
	}

	origin := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		_, _ = writer.Write([]byte("run-script-mihomo-proxy"))
	}))
	defer origin.Close()
	proxyURL, err := url.Parse(fmt.Sprintf("http://%s:%s@%s:%d",
		url.QueryEscape(dialNode.Auth.Username), url.QueryEscape(dialNode.Auth.Password), dialNode.Host, dialNode.Port))
	if err != nil {
		t.Fatal(err)
	}
	proxyClient := &http.Client{
		Timeout: 5 * time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
	}
	// The aggregate listener is applied with the rest of the configuration, so
	// the first dial can land before Mihomo has bound the port.
	var proxiedResponse *http.Response
	dialDeadline := time.Now().Add(30 * time.Second)
	for {
		proxiedResponse, err = proxyClient.Get(origin.URL)
		if err == nil {
			break
		}
		if time.Now().After(dialDeadline) {
			t.Fatalf("request through the shared Mihomo listener: %v", err)
		}
		time.Sleep(200 * time.Millisecond)
	}

	proxiedBody, err := io.ReadAll(proxiedResponse.Body)
	proxiedResponse.Body.Close()
	if err != nil {
		t.Fatalf("read proxied response: %v", err)
	}
	if string(proxiedBody) != "run-script-mihomo-proxy" {
		t.Fatalf("proxied response = %q", proxiedBody)
	}
	if err := command.Process.Signal(syscall.SIGTERM); err != nil {
		_ = command.Process.Kill()
		<-waitResult
		t.Fatalf("signal run.sh: %v", err)
	}
	select {
	case <-waitResult:
	case <-time.After(8 * time.Second):
		_ = command.Process.Kill()
		<-waitResult
		t.Fatal("run.sh did not stop frontend and backend")
	}
	if response, requestErr := client.Get(frontendURL + "/"); requestErr == nil {
		response.Body.Close()
		t.Fatal("frontend still accepts requests after run.sh stopped")
	}
}

func TestRunScriptDoesNotRegisterSystemServices(t *testing.T) {
	t.Parallel()

	content, err := os.ReadFile("run.sh")
	if err != nil {
		t.Fatalf("ReadFile(run.sh) error = %v", err)
	}
	script := string(content)
	for _, forbidden := range []string{
		"systemctl ",
		"useradd ",
		"groupadd ",
		"sudo ",
		"/etc/systemd/system",
	} {
		if strings.Contains(script, forbidden) {
			t.Errorf("run.sh must not contain service-install operation %q", forbidden)
		}
	}
	for _, expected := range []string{
		"go build",
		"--master-key",
		"--mihomo",
		"package.json",
		"VITE_BACKEND_TARGET",
		"--strictPort",
		"--clearScreen false",
		"setsid",
		"wait -n",
	} {
		if !strings.Contains(script, expected) {
			t.Errorf("run.sh is missing %q", expected)
		}
	}
}
