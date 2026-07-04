// Package cluster validates the Redis-based three-node cluster.
// It starts three gonacos subprocesses, each with GONACOS_REDIS_ADDR set,
// and verifies that config and naming changes propagate to all nodes.
package cluster

import (
	"context"
	"encoding/json"
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

	"github.com/redis/go-redis/v9"
)

const (
	redisAddr   = "127.0.0.1:6379"
	adminPass   = "nacos"
	bootTimeout = 30 * time.Second
)

type node struct {
	apiAddr  string
	grpcAddr string
	port     int
	token    string
}

var nodes [3]*node

func TestMain(m *testing.M) {
	binary := os.Getenv("GONACOS_BINARY")
	if binary == "" {
		binary = "/tmp/gonacos-test"
	}
	if _, err := os.Stat(binary); err != nil {
		fmt.Fprintf(os.Stderr, "gonacos binary not found at %s: %v\n", binary, err)
		os.Exit(1)
	}

	if !redisReady() {
		fmt.Fprintf(os.Stderr, "redis is not reachable at %s\n", redisAddr)
		os.Exit(1)
	}

	// Clear any stale gonacos:snapshot key from prior runs so each test
	// cycle starts from a clean state.
	if err := flushRedis(); err != nil {
		fmt.Fprintf(os.Stderr, "flush redis: %v\n", err)
		os.Exit(1)
	}

	// Assign ports starting from 19000 to avoid clashing with SDK tests.
	basePort := 19000
	ctxs := make([]context.Context, 3)
	cancels := make([]context.CancelFunc, 3)
	cmds := make([]*exec.Cmd, 3)

	for i := 0; i < 3; i++ {
		port := basePort + i*2
		apiAddr := fmt.Sprintf("127.0.0.1:%d", port)
		grpcAddr := fmt.Sprintf("127.0.0.1:%d", port+1000)

		// Kill any stale process.
		if conn, err := net.Dial("tcp", apiAddr); err == nil {
			conn.Close()
			fmt.Fprintf(os.Stderr, "port %d already in use\n", port)
			os.Exit(1)
		}

		ctx, cancel := context.WithCancel(context.Background())
		cmd := exec.CommandContext(ctx, binary, "serve", apiAddr)
		cmd.Dir = filepath.Dir(binary)
		cmd.Stdout = io.Discard
		cmd.Stderr = io.Discard
		cmd.Cancel = func() error { return cmd.Process.Signal(os.Interrupt) }
		cmd.WaitDelay = 5 * time.Second
		cmd.Env = append(os.Environ(), "GONACOS_REDIS_ADDR="+redisAddr)

		if err := cmd.Start(); err != nil {
			cancel()
			fmt.Fprintf(os.Stderr, "start node %d: %v\n", i, err)
			os.Exit(1)
		}

		ctxs[i] = ctx
		cancels[i] = cancel
		cmds[i] = cmd

		if !waitForServer(apiAddr, bootTimeout) {
			cancel()
			fmt.Fprintf(os.Stderr, "node %d did not become ready\n", i)
			os.Exit(1)
		}

		nodes[i] = &node{apiAddr: apiAddr, grpcAddr: grpcAddr, port: port}
	}

	// Bootstrap admin on all nodes.
	for i, n := range nodes {
		if !bootstrapAdmin(n.apiAddr) {
			fmt.Fprintf(os.Stderr, "bootstrap admin on node %d failed\n", i)
			for _, c := range cancels {
				c()
			}
			os.Exit(1)
		}
	}

	// Login on all nodes to get tokens.
	for i, n := range nodes {
		token, ok := login(n.apiAddr)
		if !ok {
			fmt.Fprintf(os.Stderr, "login on node %d failed\n", i)
			for _, c := range cancels {
				c()
			}
			os.Exit(1)
		}
		n.token = token
	}

	// Give the heartbeat a moment to register all members.
	time.Sleep(2 * time.Second)

	code := m.Run()

	for _, c := range cancels {
		c()
	}
	for _, cmd := range cmds {
		_, _ = cmd.Process.Wait()
	}
	os.Exit(code)
}

func redisReady() bool {
	c, err := net.DialTimeout("tcp", redisAddr, 2*time.Second)
	if err != nil {
		return false
	}
	c.Close()
	return true
}

// flushRedis clears the gonacos snapshot key so cluster tests start from a
// known-empty state. Without this, a snapshot written by a prior test run
// would be loaded on startup and pollute the test assertions.
func flushRedis() error {
	c := redisClient()
	defer c.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return c.Del(ctx, "gonacos:snapshot").Err()
}

// redisClient builds a short-lived client for setup/teardown operations.
func redisClient() *redis.Client {
	return redis.NewClient(&redis.Options{Addr: redisAddr})
}

func waitForServer(addr string, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 500*time.Millisecond)
		if err == nil {
			conn.Close()
			return true
		}
		time.Sleep(100 * time.Millisecond)
	}
	return false
}

func bootstrapAdmin(addr string) bool {
	url := fmt.Sprintf("http://%s/v3/auth/user/admin", addr)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader("password="+adminPass))
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

func login(addr string) (string, bool) {
	url := fmt.Sprintf("http://%s/v3/auth/user/login", addr)
	resp, err := http.Post(url, "application/x-www-form-urlencoded", strings.NewReader("username=nacos&password="+adminPass))
	if err != nil {
		return "", false
	}
	defer resp.Body.Close()
	var body struct {
		Data struct {
			AccessToken string `json:"accessToken"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", false
	}
	if body.Data.AccessToken == "" {
		return "", false
	}
	return body.Data.AccessToken, true
}

func apiURL(addr, path string) string {
	return fmt.Sprintf("http://%s%s", addr, path)
}

func publishConfig(n *node, dataID, content string) error {
	form := strings.NewReader(fmt.Sprintf("dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public&content=%s&type=json", dataID, content))
	req, _ := http.NewRequest("POST", apiURL(n.apiAddr, "/v3/admin/cs/config"), form)
	req.Header.Set("Authorization", "Bearer "+n.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("publish config: status %d", resp.StatusCode)
	}
	return nil
}

func getConfig(n *node, dataID string) (string, error) {
	url := apiURL(n.apiAddr, fmt.Sprintf("/v3/admin/cs/config?dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public", dataID))
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("get config: status %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Data.Content, nil
}

func registerInstance(n *node, serviceName, ip string, port int) error {
	form := strings.NewReader(fmt.Sprintf("serviceName=%s&groupName=DEFAULT_GROUP&namespaceId=public&ip=%s&port=%d&clusterName=DEFAULT&ephemeral=false&weight=1&enabled=true&healthy=true", serviceName, ip, port))
	req, _ := http.NewRequest("POST", apiURL(n.apiAddr, "/v3/admin/ns/instance"), form)
	req.Header.Set("Authorization", "Bearer "+n.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("register instance: status %d", resp.StatusCode)
	}
	return nil
}

func listInstances(n *node, serviceName string) ([]map[string]any, error) {
	url := apiURL(n.apiAddr, fmt.Sprintf("/v3/admin/ns/instance/list?serviceName=%s&groupName=DEFAULT_GROUP&namespaceId=public", serviceName))
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("list instances: status %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			List []map[string]any `json:"list"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil, err
	}
	return body.Data.List, nil
}

func deleteConfig(n *node, dataID string) error {
	url := apiURL(n.apiAddr, fmt.Sprintf("/v3/admin/cs/config?dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public", dataID))
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func TestConfigPropagatesToAllNodes(t *testing.T) {
	dataID := "cluster-cfg-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	content := `{"cluster":"redis"}`

	// Publish on node 0.
	if err := publishConfig(nodes[0], dataID, content); err != nil {
		t.Fatalf("publish on node 0: %v", err)
	}

	// Verify on all nodes.
	for i, n := range nodes {
		var got string
		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			got, err = getConfig(n, dataID)
			if err == nil && got == content {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("node %d: get config: %v", i, err)
		}
		if got != content {
			t.Fatalf("node %d: got %q, want %q", i, got, content)
		}
	}
}

func TestInstancePropagatesToAllNodes(t *testing.T) {
	serviceName := "cluster-svc-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	ip := "10.200.0.1"
	port := 8080

	// Register on node 1.
	if err := registerInstance(nodes[1], serviceName, ip, port); err != nil {
		t.Fatalf("register on node 1: %v", err)
	}

	// Verify on all nodes.
	for i, n := range nodes {
		var instances []map[string]any
		var err error
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			instances, err = listInstances(n, serviceName)
			if err == nil && len(instances) > 0 {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if err != nil {
			t.Fatalf("node %d: list instances: %v", i, err)
		}
		found := false
		for _, inst := range instances {
			if fmt.Sprintf("%v", inst["ip"]) == ip {
				p, _ := strconv.Atoi(fmt.Sprintf("%v", inst["port"]))
				if p == port {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("node %d: instance %s:%d not found in %v", i, ip, port, instances)
		}
	}
}

func TestConfigDeletePropagates(t *testing.T) {
	dataID := "cluster-del-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	content := `{"toDelete":true}`

	// Publish on node 0.
	if err := publishConfig(nodes[0], dataID, content); err != nil {
		t.Fatalf("publish: %v", err)
	}

	// Wait for propagation.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if got, _ := getConfig(nodes[2], dataID); got == content {
			break
		}
		time.Sleep(200 * time.Millisecond)
	}

	// Delete on node 2.
	if err := deleteConfig(nodes[2], dataID); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// Verify deleted on all nodes.
	for i, n := range nodes {
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			got, _ := getConfig(n, dataID)
			if got == "" {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		got, _ := getConfig(n, dataID)
		if got != "" {
			t.Fatalf("node %d: config still exists after delete: %q", i, got)
		}
	}
}

func TestClusterMemberDiscovery(t *testing.T) {
	for i, n := range nodes {
		url := apiURL(n.apiAddr, "/v3/admin/core/cluster/node/list")
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("Authorization", "Bearer "+n.token)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("node %d: list members: %v", i, err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		// In Redis mode, members are registered with a TTL. After the
		// heartbeat runs, all 3 nodes should be visible.
		if !strings.Contains(string(body), "127.0.0.1") {
			t.Fatalf("node %d: member list does not contain 127.0.0.1: %s", i, string(body))
		}
	}
}

func publishBetaConfig(n *node, dataID, content, betaIPs string) error {
	form := strings.NewReader(fmt.Sprintf("dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public&content=%s&type=json", dataID, content))
	req, _ := http.NewRequest("POST", apiURL(n.apiAddr, "/v3/admin/cs/config"), form)
	req.Header.Set("Authorization", "Bearer "+n.token)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("betaIps", betaIPs)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("publish beta config: status %d", resp.StatusCode)
	}
	return nil
}

func getBetaConfig(n *node, dataID string) (string, error) {
	url := apiURL(n.apiAddr, fmt.Sprintf("/v3/admin/cs/config/beta?dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public", dataID))
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return "", nil
	}
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("get beta config: status %d", resp.StatusCode)
	}
	var body struct {
		Data struct {
			Content  string `json:"content"`
			GrayName string `json:"grayName"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return "", err
	}
	return body.Data.Content, nil
}

func deleteBetaConfig(n *node, dataID string) error {
	url := apiURL(n.apiAddr, fmt.Sprintf("/v3/admin/cs/config/beta?dataId=%s&groupName=DEFAULT_GROUP&namespaceId=public", dataID))
	req, _ := http.NewRequest("DELETE", url, nil)
	req.Header.Set("Authorization", "Bearer "+n.token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func TestBetaConfigPropagates(t *testing.T) {
	dataID := "cluster-beta-" + strconv.FormatInt(time.Now().UnixNano(), 10)
	content := `{"beta":true}`
	betaIPs := "10.200.0.1"

	// Publish beta on node 0.
	if err := publishBetaConfig(nodes[0], dataID, content, betaIPs); err != nil {
		t.Fatalf("publish beta on node 0: %v", err)
	}

	// Verify beta on all nodes.
	for i, n := range nodes {
		var got string
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			got, _ = getBetaConfig(n, dataID)
			if got == content {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if got != content {
			t.Fatalf("node %d: beta content = %q, want %q", i, got, content)
		}
	}

	// Delete beta on node 2.
	if err := deleteBetaConfig(nodes[2], dataID); err != nil {
		t.Fatalf("delete beta: %v", err)
	}

	// Verify beta deleted on all nodes.
	for i, n := range nodes {
		deadline := time.Now().Add(5 * time.Second)
		var got string
		for time.Now().Before(deadline) {
			got, _ = getBetaConfig(n, dataID)
			if got == "" {
				break
			}
			time.Sleep(200 * time.Millisecond)
		}
		if got != "" {
			t.Fatalf("node %d: beta still exists after delete: %q", i, got)
		}
	}
}
