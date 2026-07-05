// Package cluster - Redis-based sync for multi-node clusters.
//
// In Redis mode, each node publishes data changes (config, instance) to a
// Redis pub/sub channel. Other nodes subscribe and apply the changes to
// their local in-memory state. Member discovery uses a Redis hash with
// per-member TTL keys that are refreshed by a heartbeat goroutine.
package cluster

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// SyncEvent is a data change notification published to Redis.
type SyncEvent struct {
	Type    string `json:"type"`    // "config" | "instance" | "namespace"
	Action  string `json:"action"`  // "register" | "deregister" | "publish" | "delete" | "create"
	NodeID  string `json:"nodeId"`  // the node that originated the change
	Payload []byte `json:"payload"` // JSON-serialized data specific to the type
}

// SyncHandler is called by the subscriber when a remote event arrives.
// The handler must be idempotent: re-applying the same event should be safe.
type SyncHandler func(event SyncEvent) error

const (
	syncChannel     = "gonacos:sync"
	memberKeyPrefix = "gonacos:member:"
	memberSetKey    = "gonacos:members"
	memberTTL       = 10 * time.Second
)

// RedisSync coordinates pub/sub and member discovery via Redis.
type RedisSync struct {
	client      *redis.Client
	ownedClient bool // true when this RedisSync created the client and should close it on Stop
	nodeID      string
	member      Member
	handlers    []SyncHandler
	mu          sync.Mutex
	stopCh      chan struct{}
	stopped     bool
}

// NewRedisSync creates a sync layer connected to the given Redis address.
// The created RedisSync owns the client and closes it on Stop.
func NewRedisSync(addr, nodeID string) (*RedisSync, error) {
	client := redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisSync{
		client:      client,
		ownedClient: true,
		nodeID:      nodeID,
		stopCh:      make(chan struct{}),
	}, nil
}

// NewRedisSyncWithClient creates a sync layer using an existing Redis client.
// The caller retains ownership of the client and is responsible for closing
// it. This is used when the persistence layer and the sync layer should share
// a single client.
func NewRedisSyncWithClient(client *redis.Client, nodeID string) (*RedisSync, error) {
	if client == nil {
		return nil, fmt.Errorf("nil redis client")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis ping: %w", err)
	}
	return &RedisSync{
		client:      client,
		ownedClient: false,
		nodeID:      nodeID,
		stopCh:      make(chan struct{}),
	}, nil
}

// RegisterHandler adds a handler for incoming sync events.
func (r *RedisSync) RegisterHandler(h SyncHandler) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.handlers = append(r.handlers, h)
}

// RegisterMember stores this node's member info in Redis with a TTL.
func (r *RedisSync) RegisterMember(m Member) error {
	r.member = m
	if m.ID == "" {
		m.ID = r.nodeID
	}
	data, err := json.Marshal(m)
	if err != nil {
		return fmt.Errorf("marshal member: %w", err)
	}
	ctx := context.Background()
	key := memberKeyPrefix + m.ID
	pipe := r.client.TxPipeline()
	pipe.Set(ctx, key, data, memberTTL)
	pipe.SAdd(ctx, memberSetKey, m.ID)
	_, err = pipe.Exec(ctx)
	return err
}

// ListMembers returns all members currently registered in Redis.
func (r *RedisSync) ListMembers() ([]Member, error) {
	ctx := context.Background()
	ids, err := r.client.SMembers(ctx, memberSetKey).Result()
	if err != nil {
		return nil, err
	}
	members := make([]Member, 0, len(ids))
	for _, id := range ids {
		data, err := r.client.Get(ctx, memberKeyPrefix+id).Bytes()
		if err == redis.Nil {
			continue // TTL expired, clean up later
		}
		if err != nil {
			continue
		}
		var m Member
		if err := json.Unmarshal(data, &m); err != nil {
			continue
		}
		members = append(members, m)
	}
	return members, nil
}

// Publish sends a sync event to all subscribers.
func (r *RedisSync) Publish(event SyncEvent) error {
	event.NodeID = r.nodeID
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("marshal sync event: %w", err)
	}
	return r.client.Publish(context.Background(), syncChannel, data).Err()
}

// Start begins the subscription goroutine and the heartbeat goroutine.
func (r *RedisSync) Start() error {
	pubsub := r.client.Subscribe(context.Background(), syncChannel)
	go r.subscribeLoop(pubsub)
	go r.heartbeatLoop()
	return nil
}

func (r *RedisSync) subscribeLoop(pubsub *redis.PubSub) {
	ch := pubsub.Channel()
	for {
		select {
		case <-r.stopCh:
			_ = pubsub.Close()
			return
		case msg, ok := <-ch:
			if !ok {
				return
			}
			var event SyncEvent
			if err := json.Unmarshal([]byte(msg.Payload), &event); err != nil {
				continue
			}
			// Don't re-apply our own events.
			if event.NodeID == r.nodeID {
				continue
			}
			r.mu.Lock()
			handlers := make([]SyncHandler, len(r.handlers))
			copy(handlers, r.handlers)
			r.mu.Unlock()
			for _, h := range handlers {
				_ = h(event)
			}
		}
	}
}

func (r *RedisSync) heartbeatLoop() {
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.stopCh:
			return
		case <-ticker.C:
			if r.member.ID != "" {
				_ = r.RegisterMember(r.member)
			}
		}
	}
}

// Stop shuts down the sync goroutines and removes this member from Redis.
// If this RedisSync owns the client (created via NewRedisSync), the client is
// closed; otherwise (NewRedisSyncWithClient) the caller retains ownership.
func (r *RedisSync) Stop() error {
	r.mu.Lock()
	if r.stopped {
		r.mu.Unlock()
		return nil
	}
	r.stopped = true
	close(r.stopCh)
	r.mu.Unlock()
	ctx := context.Background()
	if r.member.ID != "" {
		r.client.Del(ctx, memberKeyPrefix+r.member.ID)
		r.client.SRem(ctx, memberSetKey, r.member.ID)
	}
	if r.ownedClient {
		return r.client.Close()
	}
	return nil
}
