package config

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	DefaultNamespace = "public"
	DefaultType      = "text"
)

var (
	ErrMissingDataID    = errors.New("dataId is required")
	ErrMissingGroup     = errors.New("groupName is required")
	ErrMissingContent   = errors.New("content is required")
	ErrMissingIDs       = errors.New("ids is required")
	ErrMissingHistoryID = errors.New("nid is required")
	ErrMissingConfigID  = errors.New("id is required")
	ErrMissingNamespace = errors.New("namespaceId is required")
	ErrMissingIP        = errors.New("ip is required")
	ErrImportDataEmpty  = errors.New("imported file data is empty")
	ErrMetadataIllegal  = errors.New("imported metadata is invalid")
	ErrNoSelectedConfig = errors.New("no selected config")
	ErrInvalidNamespace = errors.New("invalid namespaceId")
	ErrInvalidPageNo    = errors.New("invalid pageNo")
	ErrInvalidPageSize  = errors.New("invalid pageSize")
	ErrConfigNotFound   = errors.New("config not exist")
	ErrConfigNotInBeta  = errors.New("config is not in beta")
	ErrGrayNotFound     = errors.New("gray config not found")
	ErrMissingGrayName  = errors.New("grayName is required")
	ErrHistoryNotFound  = errors.New("source must not be null")
	ErrAccessDenied     = errors.New("access denied")
)

type Item struct {
	ID               string `json:"id"`
	NamespaceID      string `json:"namespaceId"`
	GroupName        string `json:"groupName"`
	DataID           string `json:"dataId"`
	Content          string `json:"content"`
	MD5              string `json:"md5"`
	Type             string `json:"type"`
	Desc             string `json:"desc"`
	ConfigTags       string `json:"configTags"`
	AppName          string `json:"appName"`
	EncryptedDataKey string `json:"encryptedDataKey"`
	CreateTime       int64  `json:"createTime"`
	ModifyTime       int64  `json:"modifyTime"`
	CreateUser       string `json:"createUser"`
	CreateIP         string `json:"createIp"`
}

type BetaItem struct {
	Item
	GrayName string `json:"grayName"`
	GrayRule string `json:"grayRule"`
}

type BasicInfo struct {
	ID          string `json:"id"`
	NamespaceID string `json:"namespaceId"`
	GroupName   string `json:"groupName"`
	DataID      string `json:"dataId"`
	MD5         string `json:"md5"`
	Type        string `json:"type"`
	AppName     string `json:"appName"`
	CreateTime  int64  `json:"createTime"`
	ModifyTime  int64  `json:"modifyTime"`
}

type Page struct {
	TotalCount     int         `json:"totalCount"`
	PageNumber     int         `json:"pageNumber"`
	PagesAvailable int         `json:"pagesAvailable"`
	PageItems      []BasicInfo `json:"pageItems"`
}

type HistoryItem struct {
	ID               int64  `json:"id"`
	NamespaceID      string `json:"namespaceId"`
	GroupName        string `json:"groupName"`
	DataID           string `json:"dataId"`
	Content          string `json:"content,omitempty"`
	MD5              string `json:"md5"`
	Type             string `json:"type"`
	Desc             string `json:"desc"`
	ConfigTags       string `json:"configTags"`
	AppName          string `json:"appName"`
	EncryptedDataKey string `json:"encryptedDataKey,omitempty"`
	CreateTime       int64  `json:"createTime"`
	ModifyTime       int64  `json:"modifyTime"`
	SrcIP            string `json:"srcIp"`
	SrcUser          string `json:"srcUser"`
	OpType           string `json:"opType"`
	PublishType      string `json:"publishType"`
}

type HistoryPage struct {
	TotalCount     int           `json:"totalCount"`
	PageNumber     int           `json:"pageNumber"`
	PagesAvailable int           `json:"pagesAvailable"`
	PageItems      []HistoryItem `json:"pageItems"`
}

type QueryResponse struct {
	ResultCode       int    `json:"resultCode"`
	ErrorCode        int    `json:"errorCode"`
	Message          string `json:"message"`
	RequestID        string `json:"requestId"`
	Content          string `json:"content,omitempty"`
	EncryptedDataKey string `json:"encryptedDataKey"`
	ContentType      string `json:"contentType,omitempty"`
	MD5              string `json:"md5,omitempty"`
	LastModified     int64  `json:"lastModified,omitempty"`
	Tag              string `json:"tag"`
	Beta             bool   `json:"beta"`
	Success          bool   `json:"success"`
}

type PublishRequest struct {
	NamespaceID      string
	GroupName        string
	DataID           string
	Content          string
	Type             string
	Desc             string
	ConfigTags       string
	AppName          string
	SrcUser          string
	EncryptedDataKey string
	BetaIPs          string
}

type CloneRequest struct {
	SourceID        string
	TargetNamespace string
	TargetDataID    string
	TargetGroupName string
	Policy          string
	SrcUser         string
}

type CloneResult struct {
	SuccCount int `json:"succCount"`
	SkipCount int `json:"skipCount"`
}

type ImportEntry struct {
	NamespaceID string
	GroupName   string
	DataID      string
	Content     string
	Type        string
	Desc        string
	AppName     string
}

type ImportResult struct {
	SuccCount int `json:"succCount"`
	SkipCount int `json:"skipCount"`
	FailCount int `json:"failCount"`
}

type ListenerInfo struct {
	QueryType       string            `json:"queryType"`
	ListenersStatus map[string]string `json:"listenersStatus"`
}

// Capacity tracks the per-group config limits. In standalone mode the limits
// are advisory — the service does not enforce them on publish, but they are
// queryable and updatable so the console can display and manage them.
type Capacity struct {
	NamespaceID    string `json:"namespaceId"`
	GroupName      string `json:"groupName"`
	Quota          int    `json:"quota"`
	MaxSize        int    `json:"maxSize"`
	MaxAggrCount   int    `json:"maxAggrCount"`
	MaxAggrSize    int    `json:"maxAggrSize"`
	Usage          int    `json:"usage"`
	UsageSize      int    `json:"usageSize"`
	MaxAggrCountUsed int  `json:"maxAggrCountUsed"`
	MaxAggrSizeUsed  int  `json:"maxAggrSizeUsed"`
}

type Service struct {
	mu            sync.RWMutex
	nextID        int64
	nextHistoryID int64
	items         map[key]Item
	betaItems     map[grayKey]BetaItem
	history       []HistoryItem
	syncFunc      func(action string, payload []byte) error
	pushFunc      func(namespaceID, groupName, dataID string)
	listeners     *listenerRegistry
	capacity      map[key]Capacity
}

// SetSyncFunc installs a hook that is invoked after local writes. The hook
// receives a JSON-serialized payload. Remote applies bypass the hook.
func (s *Service) SetSyncFunc(f func(action string, payload []byte) error) {
	s.syncFunc = f
}

// SetPushFunc installs a hook invoked after local writes to notify the push
// layer that a config has changed. The push layer uses this to push
// ConfigChangeNotifyRequest frames to subscribed SDK clients. Remote applies
// bypass the hook to avoid echo loops.
func (s *Service) SetPushFunc(f func(namespaceID, groupName, dataID string)) {
	s.pushFunc = f
}

type key struct {
	namespaceID string
	groupName   string
	dataID      string
}

// grayKey extends key with a grayName so multiple gray versions of the same
// config can coexist. The legacy "beta" gray uses grayName="beta".
type grayKey struct {
	namespaceID string
	groupName   string
	dataID      string
	grayName    string
}

func NewService() *Service {
	return &Service{
		nextID:        1,
		nextHistoryID: 1,
		items:         map[key]Item{},
		betaItems:     map[grayKey]BetaItem{},
		listeners:     newListenerRegistry(),
		capacity:      map[key]Capacity{},
	}
}

// Stop releases background resources associated with the service.
func (s *Service) Stop() {
	if s.listeners != nil {
		s.listeners.stop()
	}
}

// TrackListener records that a client at the given IP is listening to the
// specified config with the given md5. Called from the client config query
// path so the listener registry stays fresh as SDK clients poll.
func (s *Service) TrackListener(ip, namespaceID, groupName, dataID, md5 string) {
	s.listeners.track(ip, namespaceID, groupName, dataID, md5)
}

// RemoveListener removes a listener entry, called when a client deregisters.
func (s *Service) RemoveListener(ip, namespaceID, groupName, dataID string) {
	s.listeners.remove(ip, namespaceID, groupName, dataID)
}

// ListenersByConfig returns the ip -> md5 map of live listeners for the given
// config item.
func (s *Service) ListenersByConfig(namespaceID, groupName, dataID string) map[string]string {
	return s.listeners.byConfig(namespaceID, groupName, dataID)
}

// ListenersByIP returns the "groupName dataID" -> md5 map of configs that the
// given IP is listening to. If namespaceID is non-empty, results are filtered
// to that namespace.
func (s *Service) ListenersByIP(ip, namespaceID string) map[string]string {
	return s.listeners.byIP(ip, namespaceID)
}

// BatchListen processes a ConfigBatchListenRequest from the SDK. For each
// context the server compares the client-supplied md5 against the current
// config md5 (visible to that IP, honoring beta). A context is reported as
// changed when:
//   - the client md5 differs from the server md5 (including when the client
//     has no md5 yet and the server has a config), or
//   - the config no longer exists but the client still holds a non-empty md5.
//
// Each context also refreshes the listener registry so /v3/admin/cs/listener
// reports accurate IPs. The return slice is empty when nothing changed.
func (s *Service) BatchListen(ip string, contexts []ConfigListenContext) []ChangedConfig {
	changed := make([]ChangedConfig, 0, len(contexts))
	for _, ctx := range contexts {
		namespaceID := normalizeNamespace(ctx.Tenant)
		groupName := strings.TrimSpace(ctx.Group)
		dataID := strings.TrimSpace(ctx.DataID)
		if groupName == "" || dataID == "" {
			continue
		}
		item, _, err := s.GetForClient(ip, namespaceID, groupName, dataID)
		if err != nil {
			// Config missing on server. Only report a change if the client
			// believed it existed (non-empty md5) so the SDK can drop it.
			if ctx.Md5 != "" {
				changed = append(changed, ChangedConfig{
					Namespace: namespaceID, Group: groupName, DataID: dataID,
				})
			}
			s.listeners.track(ip, namespaceID, groupName, dataID, ctx.Md5)
			continue
		}
		if item.MD5 != ctx.Md5 {
			changed = append(changed, ChangedConfig{
				Namespace: namespaceID, Group: groupName, DataID: dataID,
			})
		}
		s.listeners.track(ip, namespaceID, groupName, dataID, ctx.Md5)
	}
	return changed
}

// GetCapacity returns the capacity limits for a namespace/group. If no
// explicit capacity has been set, default values are returned with usage
// computed from the current config count.
func (s *Service) GetCapacity(namespaceID, groupName string) (Capacity, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return Capacity{}, ErrMissingGroup
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	capKey := key{namespaceID: namespaceID, groupName: groupName, dataID: ""}
	cap, ok := s.capacity[capKey]
	if !ok {
		cap = Capacity{
			NamespaceID:  namespaceID,
			GroupName:    groupName,
			Quota:        1000,
			MaxSize:      100 * 1024,
			MaxAggrCount: 1000,
			MaxAggrSize:  100 * 1024,
		}
	}
	usage, usageSize := s.usageLocked(namespaceID, groupName)
	cap.Usage = usage
	cap.UsageSize = usageSize
	return cap, nil
}

// UpdateCapacity sets the capacity limits for a namespace/group.
func (s *Service) UpdateCapacity(namespaceID, groupName string, quota, maxSize, maxAggrCount, maxAggrSize int) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName = strings.TrimSpace(groupName)
	if groupName == "" {
		return ErrMissingGroup
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	capKey := key{namespaceID: namespaceID, groupName: groupName, dataID: ""}
	cap := s.capacity[capKey]
	if cap.GroupName == "" {
		cap.NamespaceID = namespaceID
		cap.GroupName = groupName
		cap.Quota = 1000
		cap.MaxSize = 100 * 1024
		cap.MaxAggrCount = 1000
		cap.MaxAggrSize = 100 * 1024
	}
	if quota > 0 {
		cap.Quota = quota
	}
	if maxSize > 0 {
		cap.MaxSize = maxSize
	}
	if maxAggrCount > 0 {
		cap.MaxAggrCount = maxAggrCount
	}
	if maxAggrSize > 0 {
		cap.MaxAggrSize = maxAggrSize
	}
	s.capacity[capKey] = cap
	return nil
}

// ClientMetrics returns metrics about the configs a given client IP is
// listening to. The data feeds /v3/admin/cs/metrics/ip.
func (s *Service) ClientMetrics(ip, namespaceID, groupName, dataID string) map[string]any {
	ip = strings.TrimSpace(ip)
	if ip == "" {
		return map[string]any{"error": ErrMissingIP.Error()}
	}
	listeners := s.listeners.byIP(ip, namespaceID)
	total := len(listeners)
	s.mu.RLock()
	defer s.mu.RUnlock()
	var md5Count int
	for range listeners {
		md5Count++
	}
	return map[string]any{
		"ip":           ip,
		"namespaceId":  normalizeNamespace(namespaceID),
		"groupName":    groupName,
		"dataId":       dataID,
		"listenerCount": total,
		"listeners":    listeners,
	}
}

// ClusterClientMetrics returns the same shape as ClientMetrics. In standalone
// mode there is only one node, so the cluster view equals the local view.
func (s *Service) ClusterClientMetrics(ip, namespaceID, groupName, dataID string) map[string]any {
	metrics := s.ClientMetrics(ip, namespaceID, groupName, dataID)
	metrics["nodeCount"] = 1
	return metrics
}

func (s *Service) usageLocked(namespaceID, groupName string) (count int, size int) {
	for _, item := range s.items {
		if item.NamespaceID != namespaceID || item.GroupName != groupName {
			continue
		}
		count++
		size += len(item.Content)
	}
	return count, size
}

func (s *Service) Publish(req PublishRequest) error {
	if strings.TrimSpace(req.BetaIPs) != "" {
		return s.PublishBeta(req)
	}
	req.NamespaceID = normalizeNamespace(req.NamespaceID)
	req.GroupName = strings.TrimSpace(req.GroupName)
	req.DataID = strings.TrimSpace(req.DataID)
	if err := validateIdentity(req.NamespaceID, req.GroupName, req.DataID); err != nil {
		return err
	}
	if req.Content == "" {
		return ErrMissingContent
	}
	itemType := normalizeType(req.Type)
	now := time.Now().UnixMilli()
	itemKey := key{namespaceID: req.NamespaceID, groupName: req.GroupName, dataID: req.DataID}

	s.mu.Lock()
	item, ok := s.items[itemKey]
	if !ok {
		item.ID = s.nextIDString()
		item.NamespaceID = req.NamespaceID
		item.GroupName = req.GroupName
		item.DataID = req.DataID
		item.CreateTime = now
		item.CreateUser = defaultString(req.SrcUser, "nacos")
		item.CreateIP = "127.0.0.1"
	} else {
		s.appendHistoryLocked(item, "U", defaultString(req.SrcUser, item.CreateUser), now)
	}
	item.Content = req.Content
	item.MD5 = md5Hex(req.Content)
	item.Type = itemType
	item.Desc = req.Desc
	item.ConfigTags = req.ConfigTags
	item.AppName = req.AppName
	item.EncryptedDataKey = req.EncryptedDataKey
	item.ModifyTime = now
	s.items[itemKey] = item
	if !ok {
		s.appendHistoryLocked(item, "I", defaultString(req.SrcUser, item.CreateUser), now)
	}
	s.mu.Unlock()

	s.notifySync("publish", req)
	s.notifyPush(req.NamespaceID, req.GroupName, req.DataID)
	return nil
}

// ApplyRemotePublish stores a config received from another node without
// re-publishing the change (avoids infinite sync loops).
func (s *Service) ApplyRemotePublish(req PublishRequest) error {
	req.NamespaceID = normalizeNamespace(req.NamespaceID)
	req.GroupName = strings.TrimSpace(req.GroupName)
	req.DataID = strings.TrimSpace(req.DataID)
	if err := validateIdentity(req.NamespaceID, req.GroupName, req.DataID); err != nil {
		return err
	}
	if req.Content == "" {
		return ErrMissingContent
	}
	itemType := normalizeType(req.Type)
	now := time.Now().UnixMilli()
	itemKey := key{namespaceID: req.NamespaceID, groupName: req.GroupName, dataID: req.DataID}

	s.mu.Lock()
	defer s.mu.Unlock()
	item, ok := s.items[itemKey]
	if !ok {
		item.ID = s.nextIDString()
		item.NamespaceID = req.NamespaceID
		item.GroupName = req.GroupName
		item.DataID = req.DataID
		item.CreateTime = now
	} else {
		s.appendHistoryLocked(item, "U", "remote", now)
	}
	item.Content = req.Content
	item.MD5 = md5Hex(req.Content)
	item.Type = itemType
	item.Desc = req.Desc
	item.ConfigTags = req.ConfigTags
	item.AppName = req.AppName
	item.EncryptedDataKey = req.EncryptedDataKey
	item.ModifyTime = now
	s.items[itemKey] = item
	if !ok {
		s.appendHistoryLocked(item, "I", "remote", now)
	}
	return nil
}

func (s *Service) notifySync(action string, payload any) {
	if s.syncFunc == nil {
		return
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return
	}
	_ = s.syncFunc(action, data)
}

// notifyPush fires the push callback for a config change. Called after
// local writes (publish, delete, gray) so the push layer can fan out
// ConfigChangeNotifyRequest frames to subscribed SDK clients. Remote
// applies call this too — the push layer skips the originating connection
// by checking the event's NodeID.
func (s *Service) notifyPush(namespaceID, groupName, dataID string) {
	if s.pushFunc == nil {
		return
	}
	s.pushFunc(namespaceID, groupName, dataID)
}

func (s *Service) PublishBeta(req PublishRequest) error {
	return s.PublishGray(GrayRequest{
		PublishRequest: req,
		GrayName:       "beta",
		GrayRule:       buildBetaRule(req.BetaIPs),
	})
}

// GrayRequest carries the fields needed to publish a named gray version of a
// config. GrayRule is the JSON match expression (e.g. IP list or label rule)
// that decides which clients receive the gray content.
type GrayRequest struct {
	PublishRequest
	GrayName     string
	GrayRule     string
	GrayType     string
	GrayVersion  string
	GrayPriority int
}

// PublishGray stores a named gray version of a config. Clients matching the
// gray rule receive the gray content instead of the regular config. The
// legacy beta publish is a special case with GrayName="beta".
func (s *Service) PublishGray(req GrayRequest) error {
	req.NamespaceID = normalizeNamespace(req.NamespaceID)
	req.GroupName = strings.TrimSpace(req.GroupName)
	req.DataID = strings.TrimSpace(req.DataID)
	req.GrayName = strings.TrimSpace(req.GrayName)
	if err := validateIdentity(req.NamespaceID, req.GroupName, req.DataID); err != nil {
		return err
	}
	if req.GrayName == "" {
		return ErrMissingGrayName
	}
	if req.Content == "" {
		return ErrMissingContent
	}
	if req.GrayRule == "" {
		req.GrayRule = buildBetaRule(req.BetaIPs)
	}
	now := time.Now().UnixMilli()
	item := Item{
		NamespaceID:      req.NamespaceID,
		GroupName:        req.GroupName,
		DataID:           req.DataID,
		Content:          req.Content,
		MD5:              md5Hex(req.Content),
		Type:             normalizeType(req.Type),
		Desc:             req.Desc,
		ConfigTags:       req.ConfigTags,
		AppName:          req.AppName,
		EncryptedDataKey: req.EncryptedDataKey,
		CreateTime:       now,
		ModifyTime:       now,
		CreateUser:       defaultString(req.SrcUser, "nacos"),
		CreateIP:         "127.0.0.1",
	}

	s.mu.Lock()
	item.ID = s.nextIDString()
	gk := grayKey{namespaceID: item.NamespaceID, groupName: item.GroupName, dataID: item.DataID, grayName: req.GrayName}
	s.betaItems[gk] = BetaItem{
		Item:     item,
		GrayName: req.GrayName,
		GrayRule: req.GrayRule,
	}
	s.mu.Unlock()

	s.notifySync("publishGray", req)
	s.notifyPush(req.NamespaceID, req.GroupName, req.DataID)
	return nil
}

func (s *Service) GetBeta(namespaceID, groupName, dataID string) (BetaItem, error) {
	item, err := s.GetGray(namespaceID, groupName, dataID, "beta")
	if errors.Is(err, ErrGrayNotFound) {
		return BetaItem{}, ErrConfigNotInBeta
	}
	return item, err
}

// GetGray returns the named gray config for the given config key.
func (s *Service) GetGray(namespaceID, groupName, dataID, grayName string) (BetaItem, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	grayName = strings.TrimSpace(grayName)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return BetaItem{}, err
	}
	if grayName == "" {
		return BetaItem{}, ErrMissingGrayName
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.betaItems[grayKey{namespaceID: namespaceID, groupName: groupName, dataID: dataID, grayName: grayName}]
	if !ok {
		return BetaItem{}, ErrGrayNotFound
	}
	return item, nil
}

func (s *Service) DeleteBeta(namespaceID, groupName, dataID string) error {
	return s.DeleteGray(namespaceID, groupName, dataID, "beta")
}

// DeleteGray removes a named gray config.
func (s *Service) DeleteGray(namespaceID, groupName, dataID, grayName string) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	grayName = strings.TrimSpace(grayName)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return err
	}
	if grayName == "" {
		return ErrMissingGrayName
	}

	s.mu.Lock()
	delete(s.betaItems, grayKey{namespaceID: namespaceID, groupName: groupName, dataID: dataID, grayName: grayName})
	s.mu.Unlock()

	s.notifySync("deleteGray", map[string]string{
		"namespaceId": namespaceID,
		"groupName":   groupName,
		"dataId":      dataID,
		"grayName":    grayName,
	})
	s.notifyPush(namespaceID, groupName, dataID)
	return nil
}

// ListGray returns all gray configs (any grayName) for the given config. If
// grayName is non-empty, results are filtered to that gray name.
func (s *Service) ListGray(namespaceID, groupName, dataID, grayName string) []BetaItem {
	namespaceID = normalizeNamespace(namespaceID)
	groupName = strings.TrimSpace(groupName)
	dataID = strings.TrimSpace(dataID)
	grayName = strings.TrimSpace(grayName)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BetaItem, 0)
	for _, item := range s.betaItems {
		if namespaceID != "" && item.NamespaceID != namespaceID {
			continue
		}
		if groupName != "" && item.GroupName != groupName {
			continue
		}
		if dataID != "" && item.DataID != dataID {
			continue
		}
		if grayName != "" && item.GrayName != grayName {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupName == out[j].GroupName {
			return out[i].DataID < out[j].DataID
		}
		return out[i].GroupName < out[j].GroupName
	})
	return out
}

// ListBeta returns all beta configs (grayName="beta") in the given namespace.
// If namespaceID is empty, all beta configs across all namespaces are returned.
func (s *Service) ListBeta(namespaceID string) []BetaItem {
	namespaceID = normalizeNamespace(namespaceID)
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]BetaItem, 0)
	for _, item := range s.betaItems {
		if item.GrayName != "beta" {
			continue
		}
		if namespaceID != "" && item.NamespaceID != namespaceID {
			continue
		}
		out = append(out, item)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].GroupName == out[j].GroupName {
			return out[i].DataID < out[j].DataID
		}
		return out[i].GroupName < out[j].GroupName
	})
	return out
}

// GetForClient returns the config item a client at the given IP should see. If
// any gray version matches the client (by IP rule), the gray content is
// returned. Otherwise the regular config is returned.
func (s *Service) GetForClient(ip, namespaceID, groupName, dataID string) (Item, bool, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return Item{}, false, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	itemKey := key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}
	for _, beta := range s.betaItems {
		if beta.NamespaceID != namespaceID || beta.GroupName != groupName || beta.DataID != dataID {
			continue
		}
		if ipInBetaIPs(ip, beta.GrayRule) {
			return beta.Item, true, nil
		}
	}
	item, ok := s.items[itemKey]
	if !ok {
		return Item{}, false, ErrConfigNotFound
	}
	return item, false, nil
}

// ApplyRemotePublishBeta stores a beta config received from another node.
func (s *Service) ApplyRemotePublishBeta(req PublishRequest) error {
	return s.ApplyRemotePublishGray(GrayRequest{
		PublishRequest: req,
		GrayName:       "beta",
		GrayRule:       buildBetaRule(req.BetaIPs),
	})
}

// ApplyRemotePublishGray stores a gray config received from another node
// without re-publishing the change (avoids infinite sync loops).
func (s *Service) ApplyRemotePublishGray(req GrayRequest) error {
	req.NamespaceID = normalizeNamespace(req.NamespaceID)
	req.GroupName = strings.TrimSpace(req.GroupName)
	req.DataID = strings.TrimSpace(req.DataID)
	req.GrayName = strings.TrimSpace(req.GrayName)
	if err := validateIdentity(req.NamespaceID, req.GroupName, req.DataID); err != nil {
		return err
	}
	if req.GrayName == "" {
		return ErrMissingGrayName
	}
	if req.Content == "" {
		return ErrMissingContent
	}
	if req.GrayRule == "" {
		req.GrayRule = buildBetaRule(req.BetaIPs)
	}
	now := time.Now().UnixMilli()
	item := Item{
		NamespaceID:      req.NamespaceID,
		GroupName:        req.GroupName,
		DataID:           req.DataID,
		Content:          req.Content,
		MD5:              md5Hex(req.Content),
		Type:             normalizeType(req.Type),
		Desc:             req.Desc,
		ConfigTags:       req.ConfigTags,
		AppName:          req.AppName,
		EncryptedDataKey: req.EncryptedDataKey,
		CreateTime:       now,
		ModifyTime:       now,
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	item.ID = s.nextIDString()
	gk := grayKey{namespaceID: item.NamespaceID, groupName: item.GroupName, dataID: item.DataID, grayName: req.GrayName}
	s.betaItems[gk] = BetaItem{
		Item:     item,
		GrayName: req.GrayName,
		GrayRule: req.GrayRule,
	}
	return nil
}

// ApplyRemoteDeleteBeta removes a beta config received from another node.
func (s *Service) ApplyRemoteDeleteBeta(namespaceID, groupName, dataID string) error {
	return s.ApplyRemoteDeleteGray(namespaceID, groupName, dataID, "beta")
}

// ApplyRemoteDeleteGray removes a named gray config received from another node.
func (s *Service) ApplyRemoteDeleteGray(namespaceID, groupName, dataID, grayName string) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	grayName = strings.TrimSpace(grayName)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return err
	}
	if grayName == "" {
		return ErrMissingGrayName
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.betaItems, grayKey{namespaceID: namespaceID, groupName: groupName, dataID: dataID, grayName: grayName})
	return nil
}

func (s *Service) UpdateMetadata(namespaceID, groupName, dataID, desc, tags string) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return err
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	itemKey := key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}
	item, ok := s.items[itemKey]
	if !ok {
		return ErrConfigNotFound
	}
	s.appendHistoryLocked(item, "U", item.CreateUser, time.Now().UnixMilli())
	item.Desc = desc
	item.ConfigTags = tags
	item.ModifyTime = time.Now().UnixMilli()
	s.items[itemKey] = item
	return nil
}

func (s *Service) Get(namespaceID, groupName, dataID string) (Item, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return Item{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	item, ok := s.items[key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}]
	if !ok {
		return Item{}, ErrConfigNotFound
	}
	return item, nil
}

func (s *Service) Delete(namespaceID, groupName, dataID string) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return err
	}

	s.mu.Lock()
	itemKey := key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}
	if item, ok := s.items[itemKey]; ok {
		s.appendHistoryLocked(item, "D", item.CreateUser, time.Now().UnixMilli())
	}
	delete(s.items, itemKey)
	s.mu.Unlock()

	s.notifySync("delete", map[string]string{
		"namespaceId": namespaceID,
		"groupName":   groupName,
		"dataId":      dataID,
	})
	s.notifyPush(namespaceID, groupName, dataID)
	return nil
}

// ApplyRemoteDelete removes a config received from another node without
// re-publishing the change.
func (s *Service) ApplyRemoteDelete(namespaceID, groupName, dataID string) error {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	itemKey := key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}
	delete(s.items, itemKey)
	return nil
}

func (s *Service) DeleteByIDs(ids []string) error {
	if len(ids) == 0 {
		return ErrMissingIDs
	}

	idSet := map[string]struct{}{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id != "" {
			idSet[id] = struct{}{}
		}
	}
	if len(idSet) == 0 {
		return ErrMissingIDs
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	for itemKey, item := range s.items {
		if _, ok := idSet[item.ID]; ok {
			s.appendHistoryLocked(item, "D", item.CreateUser, time.Now().UnixMilli())
			delete(s.items, itemKey)
		}
	}
	return nil
}

func (s *Service) Clone(requests []CloneRequest) (CloneResult, error) {
	if len(requests) == 0 {
		return CloneResult{}, ErrNoSelectedConfig
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	var result CloneResult
	now := time.Now().UnixMilli()
	for _, req := range requests {
		targetNamespace := normalizeNamespace(req.TargetNamespace)
		if !validNamespace(targetNamespace) {
			return result, ErrInvalidNamespace
		}
		source, ok := s.findByIDLocked(strings.TrimSpace(req.SourceID))
		if !ok {
			return result, ErrConfigNotFound
		}

		targetDataID := strings.TrimSpace(req.TargetDataID)
		if targetDataID == "" {
			targetDataID = source.DataID
		}
		targetGroup := strings.TrimSpace(req.TargetGroupName)
		if targetGroup == "" {
			targetGroup = source.GroupName
		}
		if err := validateIdentity(targetNamespace, targetGroup, targetDataID); err != nil {
			return result, err
		}

		itemKey := key{namespaceID: targetNamespace, groupName: targetGroup, dataID: targetDataID}
		existing, exists := s.items[itemKey]
		if exists {
			if strings.EqualFold(req.Policy, "SKIP") {
				result.SkipCount++
				continue
			}
			s.appendHistoryLocked(existing, "U", defaultString(req.SrcUser, existing.CreateUser), now)
		}

		clone := source
		clone.ID = s.nextIDString()
		clone.NamespaceID = targetNamespace
		clone.GroupName = targetGroup
		clone.DataID = targetDataID
		clone.CreateTime = now
		clone.ModifyTime = now
		clone.CreateUser = defaultString(req.SrcUser, source.CreateUser)
		s.items[itemKey] = clone
		if !exists {
			s.appendHistoryLocked(clone, "I", clone.CreateUser, now)
		}
		result.SuccCount++
	}
	return result, nil
}

func (s *Service) List(namespaceID, groupName, dataID, search string, pageNo, pageSize int) (Page, error) {
	namespaceID = normalizeNamespace(namespaceID)
	if !validNamespace(namespaceID) {
		return Page{}, ErrInvalidNamespace
	}
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []BasicInfo
	for _, item := range s.items {
		if item.NamespaceID != namespaceID {
			continue
		}
		if !matchField(item.GroupName, groupName, search) || !matchField(item.DataID, dataID, search) {
			continue
		}
		items = append(items, BasicInfo{
			ID:          item.ID,
			NamespaceID: item.NamespaceID,
			GroupName:   item.GroupName,
			DataID:      item.DataID,
			MD5:         item.MD5,
			Type:        item.Type,
			AppName:     item.AppName,
			CreateTime:  item.CreateTime,
			ModifyTime:  item.ModifyTime,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DataID == items[j].DataID {
			return items[i].GroupName < items[j].GroupName
		}
		return items[i].DataID < items[j].DataID
	})

	total := len(items)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	return Page{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      items[start:end],
	}, nil
}

// SearchByContent returns configs whose content contains the given
// substring. The search is case-insensitive. Results exclude the content
// itself (BasicInfo only) to keep responses small.
func (s *Service) SearchByContent(namespaceID, content string, pageNo, pageSize int) (Page, error) {
	namespaceID = normalizeNamespace(namespaceID)
	if !validNamespace(namespaceID) {
		return Page{}, ErrInvalidNamespace
	}
	needle := strings.ToLower(strings.TrimSpace(content))
	if needle == "" {
		return Page{}, ErrMissingContent
	}
	if pageNo <= 0 {
		pageNo = 1
	}
	if pageSize <= 0 {
		pageSize = 100
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	var items []BasicInfo
	for _, item := range s.items {
		if item.NamespaceID != namespaceID {
			continue
		}
		if strings.Contains(strings.ToLower(item.Content), needle) {
			items = append(items, BasicInfo{
				ID:          item.ID,
				NamespaceID: item.NamespaceID,
				GroupName:   item.GroupName,
				DataID:      item.DataID,
				MD5:         item.MD5,
				Type:        item.Type,
				AppName:     item.AppName,
				CreateTime:  item.CreateTime,
				ModifyTime:  item.ModifyTime,
			})
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GroupName == items[j].GroupName {
			return items[i].DataID < items[j].DataID
		}
		return items[i].GroupName < items[j].GroupName
	})

	total := len(items)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	return Page{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      items[start:end],
	}, nil
}

func (s *Service) GetByIDs(namespaceID string, ids []string) ([]Item, error) {
	namespaceID = normalizeNamespace(namespaceID)
	if !validNamespace(namespaceID) {
		return nil, ErrInvalidNamespace
	}
	idSet := normalizeIDSet(ids)
	if len(idSet) == 0 {
		return nil, ErrMissingIDs
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]Item, 0, len(idSet))
	for _, item := range s.items {
		if item.NamespaceID != namespaceID {
			continue
		}
		if _, ok := idSet[item.ID]; ok {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].GroupName == items[j].GroupName {
			return items[i].DataID < items[j].DataID
		}
		return items[i].GroupName < items[j].GroupName
	})
	return items, nil
}

func (s *Service) Import(entries []ImportEntry, policy string) (ImportResult, error) {
	if len(entries) == 0 {
		return ImportResult{}, ErrImportDataEmpty
	}

	var result ImportResult
	for _, entry := range entries {
		req := PublishRequest{
			NamespaceID: entry.NamespaceID,
			GroupName:   entry.GroupName,
			DataID:      entry.DataID,
			Content:     entry.Content,
			Type:        entry.Type,
			Desc:        entry.Desc,
			AppName:     entry.AppName,
		}
		if strings.EqualFold(policy, "ABORT") {
			if _, err := s.Get(req.NamespaceID, req.GroupName, req.DataID); err == nil {
				result.SkipCount++
				continue
			} else if !errors.Is(err, ErrConfigNotFound) {
				return result, err
			}
		}
		if err := s.Publish(req); err != nil {
			return result, err
		}
		result.SuccCount++
	}
	return result, nil
}

func (s *Service) HistoryList(namespaceID, groupName, dataID string, pageNo, pageSize int) (HistoryPage, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return HistoryPage{}, err
	}
	if pageNo <= 0 || pageSize <= 0 {
		if pageNo <= 0 {
			return HistoryPage{}, ErrInvalidPageNo
		}
		return HistoryPage{}, ErrInvalidPageSize
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]HistoryItem, 0)
	for _, item := range s.history {
		if item.NamespaceID == namespaceID && item.GroupName == groupName && item.DataID == dataID {
			items = append(items, item)
		}
	}
	sort.Slice(items, func(i, j int) bool {
		return items[i].ID > items[j].ID
	})
	return paginateHistory(items, pageNo, pageSize), nil
}

func (s *Service) HistoryDetail(namespaceID, groupName, dataID, historyID string) (HistoryItem, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return HistoryItem{}, err
	}
	id, err := parseHistoryID(historyID)
	if err != nil {
		return HistoryItem{}, err
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	for _, item := range s.history {
		if item.ID != id {
			continue
		}
		if item.NamespaceID != namespaceID || item.GroupName != groupName || item.DataID != dataID {
			return HistoryItem{}, ErrAccessDenied
		}
		return item, nil
	}
	return HistoryItem{}, ErrHistoryNotFound
}

func (s *Service) PreviousHistory(namespaceID, groupName, dataID, configID string) (HistoryItem, error) {
	namespaceID = normalizeNamespace(namespaceID)
	groupName, dataID = strings.TrimSpace(groupName), strings.TrimSpace(dataID)
	if err := validateIdentity(namespaceID, groupName, dataID); err != nil {
		return HistoryItem{}, err
	}
	configID = strings.TrimSpace(configID)
	if configID == "" {
		return HistoryItem{}, ErrMissingConfigID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	current, ok := s.items[key{namespaceID: namespaceID, groupName: groupName, dataID: dataID}]
	if !ok || current.ID != configID {
		return HistoryItem{}, ErrHistoryNotFound
	}
	for i := len(s.history) - 1; i >= 0; i-- {
		item := s.history[i]
		if item.NamespaceID == namespaceID && item.GroupName == groupName && item.DataID == dataID {
			return item, nil
		}
	}
	return HistoryItem{}, ErrHistoryNotFound
}

func (s *Service) ConfigsByNamespace(namespaceID string) ([]BasicInfo, error) {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID == "" {
		return nil, ErrMissingNamespace
	}
	if !validNamespace(namespaceID) {
		return nil, ErrInvalidNamespace
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	items := make([]BasicInfo, 0)
	for _, item := range s.items {
		if item.NamespaceID != namespaceID {
			continue
		}
		items = append(items, BasicInfo{
			ID:          item.ID,
			NamespaceID: item.NamespaceID,
			GroupName:   item.GroupName,
			DataID:      item.DataID,
			MD5:         item.MD5,
			Type:        item.Type,
			AppName:     item.AppName,
			CreateTime:  item.CreateTime,
			ModifyTime:  item.ModifyTime,
		})
	}
	sort.Slice(items, func(i, j int) bool {
		if items[i].DataID == items[j].DataID {
			return items[i].GroupName < items[j].GroupName
		}
		return items[i].DataID < items[j].DataID
	})
	return items, nil
}

func (s *Service) findByIDLocked(id string) (Item, bool) {
	if id == "" {
		return Item{}, false
	}
	for _, item := range s.items {
		if item.ID == id {
			return item, true
		}
	}
	return Item{}, false
}

func normalizeIDSet(ids []string) map[string]struct{} {
	idSet := map[string]struct{}{}
	for _, id := range ids {
		for _, part := range strings.Split(id, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				idSet[part] = struct{}{}
			}
		}
	}
	return idSet
}

func (s *Service) appendHistoryLocked(item Item, opType, srcUser string, now int64) {
	historyID := s.nextHistoryID
	s.nextHistoryID++
	s.history = append(s.history, HistoryItem{
		ID:               historyID,
		NamespaceID:      item.NamespaceID,
		GroupName:        item.GroupName,
		DataID:           item.DataID,
		Content:          item.Content,
		MD5:              item.MD5,
		Type:             item.Type,
		Desc:             item.Desc,
		ConfigTags:       item.ConfigTags,
		AppName:          item.AppName,
		EncryptedDataKey: item.EncryptedDataKey,
		CreateTime:       item.CreateTime,
		ModifyTime:       now,
		SrcIP:            defaultString(item.CreateIP, "127.0.0.1"),
		SrcUser:          defaultString(srcUser, "nacos"),
		OpType:           opType,
		PublishType:      "formal",
	})
}

func paginateHistory(items []HistoryItem, pageNo, pageSize int) HistoryPage {
	total := len(items)
	start := (pageNo - 1) * pageSize
	if start > total {
		start = total
	}
	end := start + pageSize
	if end > total {
		end = total
	}
	pages := 0
	if total > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	return HistoryPage{
		TotalCount:     total,
		PageNumber:     pageNo,
		PagesAvailable: pages,
		PageItems:      items[start:end],
	}
}

func parseHistoryID(value string) (int64, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0, ErrMissingHistoryID
	}
	id, err := strconv.ParseInt(value, 10, 64)
	if err != nil || id <= 0 {
		return 0, ErrHistoryNotFound
	}
	return id, nil
}

func ToQueryResponse(item Item) QueryResponse {
	return QueryResponse{
		ResultCode:       200,
		ErrorCode:        0,
		Content:          item.Content,
		EncryptedDataKey: item.EncryptedDataKey,
		ContentType:      item.Type,
		MD5:              item.MD5,
		LastModified:     item.ModifyTime,
		Beta:             false,
		Success:          true,
	}
}

func NotFoundQueryResponse() QueryResponse {
	return QueryResponse{
		ResultCode: 404,
		ErrorCode:  404,
		Message:    ErrConfigNotFound.Error(),
		Success:    false,
	}
}

func (s *Service) nextIDString() string {
	id := s.nextID
	s.nextID++
	return strconv.FormatInt(id, 10)
}

func validateIdentity(namespaceID, groupName, dataID string) error {
	if !validNamespace(namespaceID) {
		return ErrInvalidNamespace
	}
	if dataID == "" {
		return ErrMissingDataID
	}
	if groupName == "" {
		return ErrMissingGroup
	}
	return nil
}

func normalizeNamespace(namespaceID string) string {
	namespaceID = strings.TrimSpace(namespaceID)
	if namespaceID == "" {
		return DefaultNamespace
	}
	return namespaceID
}

func validNamespace(namespaceID string) bool {
	return namespaceID != "" && !strings.ContainsAny(namespaceID, " \t\r\n")
}

func normalizeType(value string) string {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "text", "json", "xml", "yaml", "html", "properties":
		return strings.ToLower(strings.TrimSpace(value))
	default:
		return DefaultType
	}
}

func matchField(actual, filter, search string) bool {
	filter = strings.TrimSpace(filter)
	if filter == "" {
		return true
	}
	if strings.EqualFold(search, "accurate") {
		return actual == filter
	}
	return strings.Contains(actual, filter)
}

func md5Hex(value string) string {
	sum := md5.Sum([]byte(value))
	return hex.EncodeToString(sum[:])
}

func defaultString(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

func buildBetaRule(betaIPs string) string {
	return `{"type":"beta","betaIps":"` + strings.TrimSpace(betaIPs) + `"}`
}

// ipInBetaIPs reports whether ip appears in the betaIps field of a gray rule
// JSON string. Returns false if the rule is empty or malformed.
func ipInBetaIPs(ip, grayRule string) bool {
	ip = strings.TrimSpace(ip)
	if ip == "" || grayRule == "" {
		return false
	}
	// The rule is stored as JSON with a betaIps field that contains a
	// comma-separated list of IPs. Parse without full JSON decoding to keep
	// the helper lightweight.
	var rule struct {
		BetaIPs string `json:"betaIps"`
	}
	if err := json.Unmarshal([]byte(grayRule), &rule); err != nil {
		return false
	}
	for _, candidate := range strings.Split(rule.BetaIPs, ",") {
		if strings.TrimSpace(candidate) == ip {
			return true
		}
	}
	return false
}
