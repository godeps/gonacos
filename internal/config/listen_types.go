package config

// ConfigListenContext mirrors the nacos-sdk-go v2 ConfigListenContext that
// the SDK sends in a ConfigBatchListenRequest. Field names match the SDK's
// JSON tags so the gRPC adapter can unmarshal directly.
type ConfigListenContext struct {
	Group  string `json:"group"`
	Md5    string `json:"md5"`
	DataID string `json:"dataId"`
	Tenant string `json:"tenant"`
}

// ChangedConfig is one entry in the ConfigChangeBatchListenResponse. The SDK
// refreshes content for each config the server reports as changed.
type ChangedConfig struct {
	Namespace string `json:"tenant"`
	Group     string `json:"group"`
	DataID    string `json:"dataId"`
}
