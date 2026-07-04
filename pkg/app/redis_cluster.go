package app

import (
	"encoding/json"
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
	"github.com/godeps/gonacos/pkg/cluster"
	configsvc "github.com/godeps/gonacos/pkg/config"
	namingsvc "github.com/godeps/gonacos/pkg/naming"
)

// SetupRedisSync creates a RedisSync wired to the provided Redis client,
// configures it into the config and naming services, and starts the
// subscription + heartbeat goroutines. Returns nil if client is nil.
//
// The caller retains ownership of the client; RedisSync.Stop will not close
// it. This lets the persistence layer and the sync layer share one client.
func SetupRedisSync(client *redis.Client, nodeID string, member cluster.Member, configSvc *configsvc.Service, namingSvc *namingsvc.Service) (*cluster.RedisSync, error) {
	if client == nil {
		return nil, nil
	}
	sync, err := cluster.NewRedisSyncWithClient(client, nodeID)
	if err != nil {
		return nil, fmt.Errorf("redis sync: %w", err)
	}

	// Config sync: publish local writes, apply remote writes.
	configSvc.SetSyncFunc(func(action string, payload []byte) error {
		return sync.Publish(cluster.SyncEvent{Type: "config", Action: action, Payload: payload})
	})

	// Naming sync: publish local writes, apply remote writes.
	namingSvc.SetSyncFunc(func(action string, payload []byte) error {
		return sync.Publish(cluster.SyncEvent{Type: "instance", Action: action, Payload: payload})
	})

	// Register handlers for incoming remote events.
	sync.RegisterHandler(func(event cluster.SyncEvent) error {
		switch event.Type {
		case "config":
			return handleRemoteConfigEvent(event, configSvc)
		case "instance":
			return handleRemoteInstanceEvent(event, namingSvc)
		}
		return nil
	})

	if err := sync.RegisterMember(member); err != nil {
		return nil, fmt.Errorf("register member: %w", err)
	}
	if err := sync.Start(); err != nil {
		return nil, fmt.Errorf("start sync: %w", err)
	}
	return sync, nil
}

func handleRemoteConfigEvent(event cluster.SyncEvent, svc *configsvc.Service) error {
	switch event.Action {
	case "publish":
		var req configsvc.PublishRequest
		if err := json.Unmarshal(event.Payload, &req); err != nil {
			return err
		}
		return svc.ApplyRemotePublish(req)
	case "delete":
		var key struct {
			NamespaceID string `json:"namespaceId"`
			GroupName   string `json:"groupName"`
			DataID      string `json:"dataId"`
		}
		if err := json.Unmarshal(event.Payload, &key); err != nil {
			return err
		}
		return svc.ApplyRemoteDelete(key.NamespaceID, key.GroupName, key.DataID)
	case "publishBeta":
		var req configsvc.PublishRequest
		if err := json.Unmarshal(event.Payload, &req); err != nil {
			return err
		}
		return svc.ApplyRemotePublishBeta(req)
	case "deleteBeta":
		var key struct {
			NamespaceID string `json:"namespaceId"`
			GroupName   string `json:"groupName"`
			DataID      string `json:"dataId"`
		}
		if err := json.Unmarshal(event.Payload, &key); err != nil {
			return err
		}
		return svc.ApplyRemoteDeleteBeta(key.NamespaceID, key.GroupName, key.DataID)
	case "publishGray":
		var req configsvc.GrayRequest
		if err := json.Unmarshal(event.Payload, &req); err != nil {
			return err
		}
		return svc.ApplyRemotePublishGray(req)
	case "deleteGray":
		var key struct {
			NamespaceID string `json:"namespaceId"`
			GroupName   string `json:"groupName"`
			DataID      string `json:"dataId"`
			GrayName   string `json:"grayName"`
		}
		if err := json.Unmarshal(event.Payload, &key); err != nil {
			return err
		}
		return svc.ApplyRemoteDeleteGray(key.NamespaceID, key.GroupName, key.DataID, key.GrayName)
	}
	return nil
}

func handleRemoteInstanceEvent(event cluster.SyncEvent, svc *namingsvc.Service) error {
	switch event.Action {
	case "register":
		var inst namingsvc.Instance
		if err := json.Unmarshal(event.Payload, &inst); err != nil {
			return err
		}
		_, err := svc.ApplyRemoteRegister(inst)
		return err
	case "deregister":
		var key struct {
			NamespaceID string `json:"namespaceId"`
			GroupName   string `json:"groupName"`
			ServiceName string `json:"serviceName"`
			ClusterName string `json:"clusterName"`
			IP          string `json:"ip"`
			Port        string `json:"port"`
			InstanceID  string `json:"instanceId"`
		}
		if err := json.Unmarshal(event.Payload, &key); err != nil {
			return err
		}
		port, _ := strconv.Atoi(key.Port)
		return svc.ApplyRemoteDeregister(key.NamespaceID, key.GroupName, key.ServiceName, key.ClusterName, key.IP, port, key.InstanceID)
	}
	return nil
}
