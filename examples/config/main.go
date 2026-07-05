// Command config demonstrates the nacos-sdk-go config client against a
// running gonacos server. It exercises publish, get, listen (change
// notification), and delete.
//
// Prerequisites:
//   - gonacos server running on 127.0.0.1:8848
//   - admin user bootstrapped (POST /v3/auth/user/admin?password=nacos)
//
// Run from the repo root:
//
//	GOWORK=off go run ./examples/config
package main

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/nacos-group/nacos-sdk-go/v2/clients"
	"github.com/nacos-group/nacos-sdk-go/v2/common/constant"
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
		LogLevel:            "warn",
	}
	client, err := clients.NewConfigClient(vo.NacosClientParam{
		ClientConfig:  &cc,
		ServerConfigs: sc,
	})
	if err != nil {
		log.Fatalf("create config client: %v", err)
	}

	dataID := fmt.Sprintf("example-config-%d.yaml", time.Now().UnixNano())
	group := "DEFAULT_GROUP"
	v1 := "version: v1\nfeature_a: true\n"
	v2 := "version: v2\nfeature_a: false\n"

	// 1. Publish.
	if ok, err := client.PublishConfig(vo.ConfigParam{
		DataId:  dataID,
		Group:   group,
		Content: v1,
		Type:    "yaml",
	}); err != nil || !ok {
		log.Fatalf("publish v1: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ publish v1 (dataId=%s)\n", dataID)

	// 2. Get back.
	got, err := client.GetConfig(vo.ConfigParam{DataId: dataID, Group: group})
	if err != nil {
		log.Fatalf("get v1: %v", err)
	}
	if got != v1 {
		log.Fatalf("get v1 = %q, want %q", got, v1)
	}
	fmt.Printf("✓ get v1 (%d bytes)\n", len(got))

	// 3. Listen for changes. OnChange fires on the initial delivery and on
	// every subsequent change.
	changed := make(chan string, 4)
	err = client.ListenConfig(vo.ConfigParam{
		DataId: dataID,
		Group:  group,
		OnChange: func(ns, grp, did, data string) {
			fmt.Printf("  ← listener fired: ns=%s group=%s dataId=%s (%d bytes)\n", ns, grp, did, len(data))
			select {
			case changed <- data:
			default:
			}
		},
	})
	if err != nil {
		log.Fatalf("listen: %v", err)
	}
	// Give the listener a moment to register its first batch-listen round.
	time.Sleep(2 * time.Second)

	// 4. Publish v2 → should trigger OnChange.
	if ok, err := client.PublishConfig(vo.ConfigParam{
		DataId:  dataID,
		Group:   group,
		Content: v2,
		Type:    "yaml",
	}); err != nil || !ok {
		log.Fatalf("publish v2: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ publish v2\n")

	select {
	case data := <-changed:
		if data != v2 {
			log.Fatalf("listener received %q, want %q", data, v2)
		}
		fmt.Printf("✓ listener received v2\n")
	case <-time.After(5 * time.Second):
		log.Fatalf("listener did not fire after v2 publish within 5s")
	}

	// 5. Cancel listen, then delete.
	if err := client.CancelListenConfig(vo.ConfigParam{DataId: dataID, Group: group}); err != nil {
		log.Fatalf("cancel listen: %v", err)
	}
	fmt.Printf("✓ cancel listen\n")

	if ok, err := client.DeleteConfig(vo.ConfigParam{DataId: dataID, Group: group}); err != nil || !ok {
		log.Fatalf("delete: ok=%v err=%v", ok, err)
	}
	fmt.Printf("✓ delete\n")

	// 6. Confirm gone.
	if _, err := client.GetConfig(vo.ConfigParam{DataId: dataID, Group: group}); err == nil {
		log.Fatalf("get after delete: expected error, got nil")
	} else {
		fmt.Printf("✓ get after delete returned error (expected): %v\n", err)
	}

	fmt.Println("\nconfig example: ALL STEPS PASSED")
	_ = os.Stdout.Sync()
}
