package app

import (
	"encoding/json"
	"fmt"

	"github.com/saker-ai/gonacos/internal/ai"
	configsvc "github.com/saker-ai/gonacos/internal/config"
	"github.com/saker-ai/gonacos/internal/naming"
	grpcsrv "github.com/saker-ai/gonacos/internal/protocol/grpc"
)

// sdkInstanceRequest mirrors the JSON the nacos-sdk-go v2 sends in an
// InstanceRequest body (google.protobuf.Any.Value).
type sdkInstanceRequest struct {
	Namespace   string `json:"namespace"`
	Group       string `json:"groupName"`
	ServiceName string `json:"serviceName"`
	Type        string `json:"type"`
	Instance    struct {
		InstanceID  string            `json:"instanceId"`
		IP          string            `json:"ip"`
		Port        uint64            `json:"port"`
		Weight      float64           `json:"weight"`
		Healthy     bool              `json:"healthy"`
		Enabled     bool              `json:"enabled"`
		Ephemeral   bool              `json:"ephemeral"`
		ClusterName string            `json:"clusterName"`
		ServiceName string            `json:"serviceName"`
		Metadata    map[string]string `json:"metadata"`
	} `json:"instance"`
}

type sdkServiceQueryRequest struct {
	Namespace   string `json:"namespace"`
	Group       string `json:"groupName"`
	ServiceName string `json:"serviceName"`
	Cluster     string `json:"cluster"`
	UDP         int    `json:"udp"`
	HealthyOnly bool   `json:"healthyOnly"`
	Subscribe   bool   `json:"subscribe"`
}

type sdkServiceListRequest struct {
	Namespace   string `json:"namespace"`
	Group       string `json:"groupName"`
	ServiceName string `json:"serviceName"`
	PageNo      int    `json:"pageNo"`
	PageSize    int    `json:"pageSize"`
	Selector    string `json:"selector"`
}

// namingGRPCAdapter exposes the naming service through the gRPC adapter
// interface. The SDK serializes requests as JSON inside google.protobuf.Any.
type namingGRPCAdapter struct {
	service *naming.Service
	push    *PushService
}

func (a namingGRPCAdapter) RegisterInstanceFromGRPC(body []byte) (any, error) {
	var req sdkInstanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid instance request: %w", err)
	}
	if req.ServiceName == "" {
		return nil, fmt.Errorf("serviceName is required")
	}
	inst := naming.Instance{
		NamespaceID: req.Namespace,
		GroupName:   req.Group,
		ServiceName: req.ServiceName,
		IP:          req.Instance.IP,
		Port:        int(req.Instance.Port),
		Weight:      req.Instance.Weight,
		Healthy:     req.Instance.Healthy,
		Enabled:     req.Instance.Enabled,
		Ephemeral:   req.Instance.Ephemeral,
		ClusterName: req.Instance.ClusterName,
		Metadata:    req.Instance.Metadata,
	}
	registered, err := a.service.RegisterInstance(inst)
	if err != nil {
		return nil, err
	}
	return map[string]any{
		"instanceId":   registered.InstanceID,
		"ip":           registered.IP,
		"port":         registered.Port,
		"serviceName":  registered.ServiceName,
		"clusterName":  registered.ClusterName,
		"weight":       registered.Weight,
		"healthy":      registered.Healthy,
		"enabled":      registered.Enabled,
		"ephemeral":    registered.Ephemeral,
	}, nil
}

func (a namingGRPCAdapter) DeregisterInstanceFromGRPC(body []byte) error {
	var req sdkInstanceRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return fmt.Errorf("invalid instance request: %w", err)
	}
	return a.service.DeregisterInstance(
		req.Namespace,
		req.Group,
		req.ServiceName,
		req.Instance.ClusterName,
		req.Instance.IP,
		int(req.Instance.Port),
		req.Instance.InstanceID,
	)
}

func (a namingGRPCAdapter) SubscribeFromGRPC(body []byte, clientIP string) (any, error) {
	var req sdkServiceQueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid subscribe request: %w", err)
	}
	if a.push != nil && clientIP != "" {
		a.push.TrackServiceSubscription(clientIP, req.Namespace, req.Group, req.ServiceName, req.Subscribe)
	}
	instances, err := a.service.ListInstances(req.Namespace, req.Group, req.ServiceName, req.Cluster, req.HealthyOnly)
	if err != nil {
		return nil, err
	}
	hosts := make([]map[string]any, 0, len(instances))
	for _, inst := range instances {
		hosts = append(hosts, map[string]any{
			"instanceId":  inst.InstanceID,
			"ip":          inst.IP,
			"port":        inst.Port,
			"weight":      inst.Weight,
			"healthy":     inst.Healthy,
			"enabled":     inst.Enabled,
			"ephemeral":   inst.Ephemeral,
			"clusterName": inst.ClusterName,
			"serviceName": inst.ServiceName,
			"metadata":    inst.Metadata,
		})
	}
	return map[string]any{
		"name":      req.ServiceName,
		"groupName": req.Group,
		"clusters":  req.Cluster,
		"hosts":     hosts,
		"valid":     true,
		"allIPs":    false,
	}, nil
}

func (a namingGRPCAdapter) ListInstancesFromGRPC(body []byte) (any, error) {
	var req sdkServiceQueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid service query request: %w", err)
	}
	return a.service.ListInstances(req.Namespace, req.Group, req.ServiceName, req.Cluster, req.HealthyOnly)
}

func (a namingGRPCAdapter) QueryServiceFromGRPC(body []byte) (any, error) {
	var req sdkServiceQueryRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid service query request: %w", err)
	}
	instances, err := a.service.ListInstances(req.Namespace, req.Group, req.ServiceName, req.Cluster, req.HealthyOnly)
	if err != nil {
		return nil, err
	}
	hosts := make([]map[string]any, 0, len(instances))
	for _, inst := range instances {
		hosts = append(hosts, map[string]any{
			"instanceId":  inst.InstanceID,
			"ip":          inst.IP,
			"port":        inst.Port,
			"weight":      inst.Weight,
			"healthy":     inst.Healthy,
			"enabled":     inst.Enabled,
			"ephemeral":   inst.Ephemeral,
			"clusterName": inst.ClusterName,
			"serviceName": inst.ServiceName,
			"metadata":    inst.Metadata,
		})
	}
	return map[string]any{
		"name":      req.ServiceName,
		"groupName": req.Group,
		"clusters":  req.Cluster,
		"hosts":     hosts,
		"valid":     true,
		"allIPs":    false,
	}, nil
}

func (a namingGRPCAdapter) ListServicesFromGRPC(body []byte) (any, error) {
	var req sdkServiceListRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid service list request: %w", err)
	}
	page, err := a.service.ListServices(req.Namespace, req.Group, req.ServiceName, req.PageNo, req.PageSize, true, false)
	if err != nil {
		return nil, err
	}
	services := make([]map[string]any, 0, len(page.Services))
	for _, svc := range page.Services {
		services = append(services, map[string]any{
			"name":      svc.Name,
			"groupName": svc.GroupName,
			"namespace": svc.NamespaceID,
		})
	}
	return map[string]any{
		"count":    page.Count,
		"services": services,
	}, nil
}

// sdkConfigRequest mirrors the JSON the nacos-sdk-go v2 sends in a
// ConfigQueryRequest / ConfigPublishRequest / ConfigRemoveRequest body.
type sdkConfigRequest struct {
	Group      string `json:"group"`
	DataID     string `json:"dataId"`
	Tenant     string `json:"tenant"`
	Content    string `json:"content"`
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	BetaIps    string `json:"betaIps"`
	CasMd5     string `json:"casMd5"`
	SrcUser    string `json:"srcUser"`
}

// configGRPCAdapter exposes the config service through the gRPC adapter.
// The SDK serializes requests as JSON inside google.protobuf.Any.
type configGRPCAdapter struct {
	service *configsvc.Service
	push    *PushService
}

func (c configGRPCAdapter) PublishFromGRPC(body []byte) (any, error) {
	var req sdkConfigRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid config publish request: %w", err)
	}
	if c.service == nil {
		return nil, errConfigBridgeNotReady
	}
	err := c.service.Publish(configsvc.PublishRequest{
		NamespaceID: req.Tenant,
		GroupName:   req.Group,
		DataID:      req.DataID,
		Content:     req.Content,
		Type:        req.Type,
		BetaIPs:     req.BetaIps,
		SrcUser:     req.SrcUser,
	})
	if err != nil {
		return nil, err
	}
	return map[string]any{"published": true}, nil
}

func (c configGRPCAdapter) QueryFromGRPC(body []byte) (any, error) {
	var req sdkConfigRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid config query request: %w", err)
	}
	if c.service == nil {
		return nil, errConfigBridgeNotReady
	}
	item, err := c.service.Get(req.Tenant, req.Group, req.DataID)
	if err != nil {
		// SDK expects a specific response shape for not-found: success=false
		// with a particular error code rather than a 500.
		return map[string]any{
			"resultCode": 500,
			"success":    false,
			"message":    err.Error(),
			"content":    "",
			"md5":        "",
		}, nil
	}
	return map[string]any{
		"resultCode":       200,
		"success":          true,
		"message":          "ok",
		"content":          item.Content,
		"md5":              item.MD5,
		"lastModified":     item.ModifyTime,
		"contentType":      item.Type,
		"encryptedDataKey": item.EncryptedDataKey,
		"isBeta":           false,
		"tag":              false,
	}, nil
}

func (c configGRPCAdapter) RemoveFromGRPC(body []byte) (any, error) {
	var req sdkConfigRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid config remove request: %w", err)
	}
	if c.service == nil {
		return nil, errConfigBridgeNotReady
	}
	if err := c.service.Delete(req.Tenant, req.Group, req.DataID); err != nil {
		return nil, err
	}
	return map[string]any{"removed": true}, nil
}

func (c configGRPCAdapter) BatchPublishFromGRPC(body []byte) (any, error) {
	// The batch publish request wraps an array of configs. We parse and
	// publish each individually.
	var raw struct {
		Group  string `json:"group"`
		DataID string `json:"dataId"`
		Tenant string `json:"tenant"`
		Items  []struct {
			DataID  string `json:"dataId"`
			Group   string `json:"group"`
			Content string `json:"content"`
			Type    string `json:"type"`
		} `json:"configs"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("invalid batch config request: %w", err)
	}
	for _, item := range raw.Items {
		req := sdkConfigRequest{
			Group:   item.Group,
			DataID:  item.DataID,
			Tenant:  raw.Tenant,
			Content: item.Content,
			Type:    item.Type,
		}
		if _, err := c.PublishFromGRPC(marshalSDKConfigRequest(req)); err != nil {
			return nil, err
		}
	}
	return map[string]any{"published": len(raw.Items)}, nil
}

func (c configGRPCAdapter) BatchListenFromGRPC(body []byte, ip string) (any, error) {
	if c.service == nil {
		return nil, errConfigBridgeNotReady
	}
	var req struct {
		Listen               bool `json:"listen"`
		ConfigListenContexts []struct {
			Group  string `json:"group"`
			Md5    string `json:"md5"`
			DataID string `json:"dataId"`
			Tenant string `json:"tenant"`
		} `json:"configListenContexts"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid batch listen request: %w", err)
	}
	contexts := make([]configsvc.ConfigListenContext, 0, len(req.ConfigListenContexts))
	for _, ctx := range req.ConfigListenContexts {
		contexts = append(contexts, configsvc.ConfigListenContext{
			Group:  ctx.Group,
			Md5:    ctx.Md5,
			DataID: ctx.DataID,
			Tenant: ctx.Tenant,
		})
		if c.push != nil && ip != "" {
			c.push.TrackConfigSubscription(ip, ctx.Tenant, ctx.Group, ctx.DataID, req.Listen)
		}
	}
	changed := c.service.BatchListen(ip, contexts)
	out := make([]map[string]string, 0, len(changed))
	for _, cfg := range changed {
		out = append(out, map[string]string{
			"group":  cfg.Group,
			"dataId": cfg.DataID,
			"tenant": cfg.Namespace,
		})
	}
	return map[string]any{"changedConfigs": out}, nil
}

// marshalSDKConfigRequest encodes a sdkConfigRequest to JSON. Used by
// BatchPublishFromGRPC to reuse PublishFromGRPC.
func marshalSDKConfigRequest(req sdkConfigRequest) []byte {
	b, err := json.Marshal(req)
	if err != nil {
		return []byte("{}")
	}
	return b
}

// publishConfigFromGRPC / queryConfigFromGRPC / removeConfigFromGRPC are
// implemented in grpc_config_bridge.go to avoid an import cycle. They route
// JSON requests into the real config service.

// aiGRPCAdapter exposes the AI service through the gRPC adapter.
type aiGRPCAdapter struct{ service *ai.Service }

func (a aiGRPCAdapter) QueryPromptFromGRPC(body []byte) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid prompt request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return a.service.QueryClientPrompt(req.ID)
}

func (a aiGRPCAdapter) QuerySkillFromGRPC(body []byte) (any, error) {
	var req struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		return nil, fmt.Errorf("invalid skill request: %w", err)
	}
	if req.ID == "" {
		return nil, fmt.Errorf("id is required")
	}
	return a.service.QueryClientSkill(req.ID)
}

// extractProtoString reads a length-delimited string field by number from a
// protobuf message using the gRPC codec's reader. Retained for compatibility.
func extractProtoString(body []byte, field int) string {
	r := grpcsrv.NewReader(body)
	for !r.Done() {
		f, wire, err := r.ReadTag()
		if err != nil {
			return ""
		}
		if f == field && wire == 2 {
			s, err := r.ReadString()
			if err != nil {
				return ""
			}
			return s
		}
		if err := r.Skip(wire); err != nil {
			return ""
		}
	}
	return ""
}

var _ = extractProtoString
