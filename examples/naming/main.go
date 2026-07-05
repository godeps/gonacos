// Command naming demonstrates the nacos-sdk-go naming client against a
// running gonacos server. It exercises register, discover, subscribe (push
// notification), and deregister.
//
// Prerequisites:
//   - gonacos server running on 127.0.0.1:8848
//   - admin user bootstrapped (POST /v3/auth/user/admin?password=nacos)
//
// Run from the repo root:
//
//	GOWORK=off go run ./examples/naming
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
	"github.com/nacos-group/nacos-sdk-go/v2/model"
	"github.com/nacos-group/nacos-sdk-go/v2/vo"
)

const (
	serverHost = "127.0.0.1"
	serverPort = 8848
	username   = "nacos"
	password   = "nacos"
	namespace  = "public"
)

func main() {
	sc := []constant.ServerConfig{{
		IpAddr: serverHost,
		Port:   uint64(serverPort),
	}}
	cc := constant.ClientConfig{
		Username:            username,
		Password:            password,
		NamespaceId:         namespace,
		TimeoutMs:           5000,
		NotLoadCacheAtStart: true,
		LogLevel:            "debug",
	}
	client, err := clients.NewNamingClient(vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: sc,
	})
	if err != nil {
		log.Fatalf("create naming client: %v", err)
	}

	serviceName := fmt.Sprintf("example-service-%d", time.Now().UnixNano())
	group := "DEFAULT_GROUP"
	cluster := "DEFAULT"

	// 1. Register instance #1.
	inst1 := vo.RegisterInstanceParam{
		Ip:          "10.0.0.11",
		Port:        uint64(8080),
		ServiceName: serviceName,
		GroupName:   group,
		Weight:      1.0,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		ClusterName: cluster,
	}
	if ok, err := client.RegisterInstance(inst1); err != nil || !ok {
		log.Fatalf("register inst1: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ register instance #1 (10.0.0.11:8080, service=%s)\n", serviceName)

	// 2. Subscribe — push callback fires on the initial delivery and on
	// every cluster change. Subscribe before any SelectInstances call so the
	// SDK's internal cache is empty and the RPC actually reaches the server
	// (otherwise SelectInstances populates the cache and Subscribe skips the
	// RPC, leaving the callback un-fired).
	push := make(chan []model.Instance, 4)
	if err := client.Subscribe(&vo.SubscribeParam{
		ServiceName: serviceName,
		GroupName:   group,
		Clusters:    []string{cluster},
		SubscribeCallback: func(services []model.Instance, err error) {
			if err != nil {
				log.Printf("  ← subscribe error: %v", err)
				return
			}
			ips := make([]string, 0, len(services))
			for _, s := range services {
				ips = append(ips, fmt.Sprintf("%s:%d", s.Ip, s.Port))
			}
			fmt.Printf("  ← subscribe push: %d instances %v\n", len(services), ips)
			select {
			case push <- services:
			default:
			}
		},
	}); err != nil {
		log.Fatalf("subscribe: %v", err)
	}
	// Wait for the initial push.
	select {
	case <-push:
		fmt.Printf("✓ subscribe initial push received\n")
	case <-time.After(5 * time.Second):
		log.Fatalf("subscribe did not push within 5s")
	}

	// 3. Discover — should see inst1.
	if err := assertInstances(client, serviceName, group, []string{"10.0.0.11"}); err != nil {
		log.Fatalf("discover after inst1: %v", err)
	}
	fmt.Printf("✓ discover instance #1\n")

	// 4. Register instance #2 → should trigger a push with 2 instances.
	inst2 := vo.RegisterInstanceParam{
		Ip:          "10.0.0.12",
		Port:        uint64(8080),
		ServiceName: serviceName,
		GroupName:   group,
		Weight:      1.0,
		Enable:      true,
		Healthy:     true,
		Ephemeral:   true,
		ClusterName: cluster,
	}
	if ok, err := client.RegisterInstance(inst2); err != nil || !ok {
		log.Fatalf("register inst2: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ register instance #2 (10.0.0.12:8080)\n")

	// Wait for the push that carries 2 instances.
	if err := waitForPushCount(push, 2, 5*time.Second); err != nil {
		log.Fatalf("after inst2: %v", err)
	}
	fmt.Printf("✓ subscribe push now carries 2 instances\n")

	// 5. Discover again — should see both.
	if err := assertInstances(client, serviceName, group, []string{"10.0.0.11", "10.0.0.12"}); err != nil {
		log.Fatalf("discover after inst2: %v", err)
	}
	fmt.Printf("✓ discover both instances\n")

	// 6. Deregister inst2 → push should drop back to 1.
	if ok, err := client.DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          inst2.Ip,
		Port:        inst2.Port,
		ServiceName: serviceName,
		GroupName:   group,
		Cluster:     cluster,
		Ephemeral:   true,
	}); err != nil || !ok {
		log.Fatalf("deregister inst2: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ deregister instance #2\n")

	if err := waitForPushCount(push, 1, 5*time.Second); err != nil {
		log.Fatalf("after deregister inst2: %v", err)
	}
	fmt.Printf("✓ subscribe push back to 1 instance\n")

	// 7. Unsubscribe + deregister inst1.
	if err := client.Unsubscribe(&vo.SubscribeParam{
		ServiceName: serviceName,
		GroupName:   group,
		Clusters:    []string{cluster},
	}); err != nil {
		log.Fatalf("unsubscribe: %v", err)
	}
	fmt.Printf("✓ unsubscribe\n")

	if ok, err := client.DeregisterInstance(vo.DeregisterInstanceParam{
		Ip:          inst1.Ip,
		Port:        inst1.Port,
		ServiceName: serviceName,
		GroupName:   group,
		Cluster:     cluster,
		Ephemeral:   true,
	}); err != nil || !ok {
		log.Fatalf("deregister inst1: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ deregister instance #1\n")

	fmt.Println("\nnaming example: ALL STEPS PASSED")
}

// assertInstances queries the server and verifies the registered IPs match
// (in any order).
func assertInstances(client interface {
	SelectInstances(param vo.SelectInstancesParam) ([]model.Instance, error)
}, serviceName, group string, wantIPs []string) error {
	got, err := client.SelectInstances(vo.SelectInstancesParam{
		ServiceName: serviceName,
		GroupName:   group,
		Clusters:    []string{"DEFAULT"},
		HealthyOnly: true,
	})
	if err != nil {
		return fmt.Errorf("select instances: %w", err)
	}
	if len(got) != len(wantIPs) {
		return fmt.Errorf("got %d instances, want %d", len(got), len(wantIPs))
	}
	gotSet := map[string]bool{}
	for _, inst := range got {
		gotSet[inst.Ip] = true
	}
	for _, ip := range wantIPs {
		if !gotSet[ip] {
			return fmt.Errorf("instance %s not in result %v", ip, gotSet)
		}
	}
	return nil
}

// waitForPushCount blocks until a push arrives with exactly n instances.
func waitForPushCount(push <-chan []model.Instance, n int, timeout time.Duration) error {
	deadline := time.After(timeout)
	for {
		select {
		case services := <-push:
			if len(services) == n {
				return nil
			}
		case <-deadline:
			return fmt.Errorf("no push with %d instances within %s", n, timeout)
		}
	}
}
