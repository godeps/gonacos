// Package sdkcompat validates gonacos against the real nacos-sdk-go v2.
// These integration tests start a gonacos subprocess, bootstrap the admin
// user, and exercise config + naming operations through the SDK.
package sdkcompat

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/config_client"
	"github.com/nacos-group/nacos-sdk-go/v2/clients/naming_client"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

const (
	serverPort    = 18848
	grpcPort      = 19848
	serverHost    = "127.0.0.1"
	adminPassword = "nacos"
)

var (
	configClient config_client.IConfigClient
	namingClient naming_client.INamingClient
)

func TestMain(m *testing.M) {
	binary := os.Getenv("GONACOS_BINARY")
	if binary == "" {
		binary = "/tmp/gonacos-test"
	}
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintf(os.Stderr, "gonacos binary not found at %s: %v\n", binary, err)
		os.Exit(1)
	}

	// Remove any stale snapshot dump so each test run starts from a clean
	// state. The server writes its dump to <cmd.Dir>/.gonacos/data/.
	dumpDir := filepath.Join(filepath.Dir(binary), ".gonacos")
	if err := os.RemoveAll(dumpDir); err != nil {
		fmt.Fprintf(os.Stderr, "warn: remove dump dir: %v\n", err)
	}

	// Kill any stale process on the port.
	if conn, err := net.Dial("tcp", fmt.Sprintf("%s:%d", serverHost, serverPort)); err == nil {
		conn.Close()
		fmt.Fprintf(os.Stderr, "port %d already in use; cannot start gonacos\n", serverPort)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cmd := exec.CommandContext(ctx, binary, "serve", fmt.Sprintf("%s:%d", serverHost, serverPort))
	cmd.Dir = filepath.Dir(binary)
	// Discard server output to avoid fd sharing with the test process,
	// which causes "Test I/O incomplete" on exit.
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	// Give the server time to flush I/O after SIGTERM before SIGKILL.
	cmd.Cancel = func() error {
		return cmd.Process.Signal(os.Interrupt)
	}
	cmd.WaitDelay = 5 * time.Second
	if err := cmd.Start(); err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "start gonacos: %v\n", err)
		os.Exit(1)
	}

	if !waitForServer(serverHost, serverPort, 10*time.Second) {
		cancel()
		fmt.Fprintf(os.Stderr, "gonacos did not become ready\n")
		os.Exit(1)
	}

	if !bootstrapAdmin() {
		cancel()
		os.Exit(1)
	}

	if !initSDK() {
		cancel()
		os.Exit(1)
	}

	exitCode := m.Run()
	cancel()
	_, _ = cmd.Process.Wait()
	os.Exit(exitCode)
}

func waitForServer(host string, port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", fmt.Sprintf("%s:%d", host, port), 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func bootstrapAdmin() bool {
	url := fmt.Sprintf("http://%s:%d/v3/auth/user/admin", serverHost, serverPort)
	body := "password=" + adminPassword
	resp, err := sendForm(url, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "bootstrap admin: %v\n", err)
		return false
	}
	fmt.Printf("bootstrap admin response: %s\n", resp)
	return true
}

func initSDK() bool {
	sc := []constant.ServerConfig{
		{
			IpAddr: serverHost,
			Port:   uint64(serverPort),
		},
	}
	cc := constant.ClientConfig{
		Username:    "nacos",
		Password:    adminPassword,
		NamespaceId: "public",
		TimeoutMs:   5000,
		LogLevel:    "debug",
		AppendToStdout: true,
		NotLoadCacheAtStart: true,
	}

	cp := vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: sc,
	}

	cfg, err := clients.NewConfigClient(cp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create config client: %v\n", err)
		return false
	}
	configClient = cfg

	naming, err := clients.NewNamingClient(cp)
	if err != nil {
		fmt.Fprintf(os.Stderr, "create naming client: %v\n", err)
		return false
	}
	namingClient = naming
	return true
}

func sendForm(url, body string) (string, error) {
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func TestConfigPublishAndGet(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	dataID := "test-config-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	group := "DEFAULT_GROUP"
	content := `{"key":"value"}`

	ok, err := configClient.PublishConfig(vo.ConfigParam{
		DataId:  dataID,
		Group:   group,
		Content: content,
		Type:    "json",
	})
	if err != nil {
		t.Fatalf("publish config: %v", err)
	}
	if !ok {
		t.Fatal("publish returned false")
	}

	got, err := configClient.GetConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
	})
	if err != nil {
		t.Fatalf("get config: %v", err)
	}
	if got != content {
		t.Fatalf("got %q, want %q", got, content)
	}
	_ = ctx
}

func TestConfigDelete(t *testing.T) {
	dataID := "test-delete-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	group := "DEFAULT_GROUP"
	content := "to-be-deleted"

	_, err := configClient.PublishConfig(vo.ConfigParam{
		DataId:  dataID,
		Group:   group,
		Content: content,
	})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	ok, err := configClient.DeleteConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
	})
	if err != nil {
		t.Fatalf("delete: %v", err)
	}
	if !ok {
		t.Fatal("delete returned false")
	}
}

func TestNamingRegisterAndDiscover(t *testing.T) {
	serviceName := "test-service-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	group := "DEFAULT_GROUP"
	ip := "10.0.0.1"
	port := uint64(8080)

	ok, err := namingClient.RegisterInstance(vo.RegisterInstanceParam{
		Ip:          ip,
		Port:        port,
		ServiceName: serviceName,
		GroupName:   group,
		Weight:      1.0,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		ClusterName: "DEFAULT",
	})
	if err != nil {
		t.Fatalf("register instance: %v", err)
	}
	if !ok {
		t.Fatal("register returned false")
	}

	instances, err := namingClient.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   group,
		Clusters:    []string{"DEFAULT"},
		HealthyOnly: true,
	})
	if err != nil {
		t.Fatalf("select instances: %v", err)
	}
	if len(instances) == 0 {
		t.Fatal("no instances discovered")
	}
	found := false
	for _, inst := range instances {
		if inst.Ip == ip && inst.Port == port {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("instance %s:%d not found in %v", ip, port, instances)
	}
}

func TestNamingDeregister(t *testing.T) {
	serviceName := "test-deregister-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	group := "DEFAULT_GROUP"
	ip := "10.0.0.2"
	port := uint64(9090)

	_, err := namingClient.RegisterInstance(vo.RegisterInstanceParam{
		Ip:          ip,
		Port:        port,
		ServiceName: serviceName,
		GroupName:   group,
		Ephemeral:   true,
		ClusterName: "DEFAULT",
	})
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	ok, err := namingClient.DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          ip,
		Port:        port,
		ServiceName: serviceName,
		GroupName:   group,
		Cluster:     "DEFAULT",
		Ephemeral:   true,
	})
	if err != nil {
		t.Fatalf("deregister: %v", err)
	}
	if !ok {
		t.Fatal("deregister returned false")
	}
}
