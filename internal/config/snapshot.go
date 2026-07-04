package config

import (
	"fmt"
	"sort"
)

// configSnapshot captures all config items, beta items, history, and ID
// counters so a restore reproduces the exact pre-backup state.
type configSnapshot struct {
	Items          []Item     `json:"items"`
	BetaItems      []BetaItem `json:"betaItems"`
	History        []HistoryItem `json:"history"`
	NextID         int64      `json:"nextId"`
	NextHistoryID  int64      `json:"nextHistoryId"`
}

// SnapshotKey identifies the config service in backup envelopes.
func (s *Service) SnapshotKey() string { return "config" }

// Snapshot returns the full config state for backup.
func (s *Service) Snapshot() (any, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	snap := configSnapshot{
		NextID:        s.nextID,
		NextHistoryID: s.nextHistoryID,
	}
	for _, item := range s.items {
		snap.Items = append(snap.Items, item)
	}
	sort.Slice(snap.Items, func(i, j int) bool {
		return itemLess(snap.Items[i], snap.Items[j])
	})
	for _, item := range s.betaItems {
		snap.BetaItems = append(snap.BetaItems, item)
	}
	sort.Slice(snap.BetaItems, func(i, j int) bool {
		return itemLess(snap.BetaItems[i].Item, snap.BetaItems[j].Item)
	})
	snap.History = append(snap.History, s.history...)
	sort.Slice(snap.History, func(i, j int) bool {
		if snap.History[i].ID != snap.History[j].ID {
			return snap.History[i].ID < snap.History[j].ID
		}
		return snap.History[i].ModifyTime < snap.History[j].ModifyTime
	})
	return snap, nil
}

// Restore replaces all config state from the decoded snapshot.
func (s *Service) Restore(data any) error {
	snap, ok := data.(map[string]any)
	if !ok {
		return errConfigSnapshotShape
	}
	items, err := decodeConfigItems(snap["items"])
	if err != nil {
		return err
	}
	beta, err := decodeConfigBetaItems(snap["betaItems"])
	if err != nil {
		return err
	}
	history, err := decodeConfigHistory(snap["history"])
	if err != nil {
		return err
	}
	nextID, err := decodeInt(snap["nextId"])
	if err != nil {
		return err
	}
	nextHist, err := decodeInt(snap["nextHistoryId"])
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.items = map[key]Item{}
	s.betaItems = map[grayKey]BetaItem{}
	s.history = history
	s.nextID = nextID
	s.nextHistoryID = nextHist
	for _, item := range items {
		k := key{namespaceID: item.NamespaceID, groupName: item.GroupName, dataID: item.DataID}
		s.items[k] = item
	}
	for _, item := range beta {
		k := grayKey{namespaceID: item.NamespaceID, groupName: item.GroupName, dataID: item.DataID, grayName: item.GrayName}
		s.betaItems[k] = item
	}
	return nil
}

func itemLess(a, b Item) bool {
	if a.NamespaceID != b.NamespaceID {
		return a.NamespaceID < b.NamespaceID
	}
	if a.GroupName != b.GroupName {
		return a.GroupName < b.GroupName
	}
	return a.DataID < b.DataID
}

func decodeConfigItems(raw any) ([]Item, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errConfigSnapshotShape
	}
	out := make([]Item, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errConfigSnapshotShape
		}
		out = append(out, decodeItem(m))
	}
	return out, nil
}

func decodeConfigBetaItems(raw any) ([]BetaItem, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errConfigSnapshotShape
	}
	out := make([]BetaItem, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errConfigSnapshotShape
		}
		b := BetaItem{Item: decodeItem(m)}
		if v, ok := m["grayName"].(string); ok {
			b.GrayName = v
		}
		if v, ok := m["grayRule"].(string); ok {
			b.GrayRule = v
		}
		out = append(out, b)
	}
	return out, nil
}

func decodeConfigHistory(raw any) ([]HistoryItem, error) {
	if raw == nil {
		return nil, nil
	}
	items, ok := raw.([]any)
	if !ok {
		return nil, errConfigSnapshotShape
	}
	out := make([]HistoryItem, 0, len(items))
	for _, entry := range items {
		m, ok := entry.(map[string]any)
		if !ok {
			return nil, errConfigSnapshotShape
		}
		out = append(out, decodeHistory(m))
	}
	return out, nil
}

func decodeItem(m map[string]any) Item {
	item := Item{}
	if v, ok := m["id"].(string); ok {
		item.ID = v
	}
	if v, ok := m["namespaceId"].(string); ok {
		item.NamespaceID = v
	}
	if v, ok := m["groupName"].(string); ok {
		item.GroupName = v
	}
	if v, ok := m["dataId"].(string); ok {
		item.DataID = v
	}
	if v, ok := m["content"].(string); ok {
		item.Content = v
	}
	if v, ok := m["md5"].(string); ok {
		item.MD5 = v
	}
	if v, ok := m["type"].(string); ok {
		item.Type = v
	}
	if v, ok := m["desc"].(string); ok {
		item.Desc = v
	}
	if v, ok := m["configTags"].(string); ok {
		item.ConfigTags = v
	}
	if v, ok := m["appName"].(string); ok {
		item.AppName = v
	}
	if v, ok := m["encryptedDataKey"].(string); ok {
		item.EncryptedDataKey = v
	}
	if n, err := decodeInt(m["createTime"]); err == nil {
		item.CreateTime = n
	}
	if n, err := decodeInt(m["modifyTime"]); err == nil {
		item.ModifyTime = n
	}
	if v, ok := m["createUser"].(string); ok {
		item.CreateUser = v
	}
	if v, ok := m["createIp"].(string); ok {
		item.CreateIP = v
	}
	return item
}

func decodeHistory(m map[string]any) HistoryItem {
	h := HistoryItem{}
	if n, err := decodeInt(m["id"]); err == nil {
		h.ID = n
	}
	if v, ok := m["namespaceId"].(string); ok {
		h.NamespaceID = v
	}
	if v, ok := m["groupName"].(string); ok {
		h.GroupName = v
	}
	if v, ok := m["dataId"].(string); ok {
		h.DataID = v
	}
	if v, ok := m["content"].(string); ok {
		h.Content = v
	}
	if v, ok := m["md5"].(string); ok {
		h.MD5 = v
	}
	if v, ok := m["type"].(string); ok {
		h.Type = v
	}
	if v, ok := m["desc"].(string); ok {
		h.Desc = v
	}
	if v, ok := m["configTags"].(string); ok {
		h.ConfigTags = v
	}
	if v, ok := m["appName"].(string); ok {
		h.AppName = v
	}
	if v, ok := m["encryptedDataKey"].(string); ok {
		h.EncryptedDataKey = v
	}
	if n, err := decodeInt(m["createTime"]); err == nil {
		h.CreateTime = n
	}
	if n, err := decodeInt(m["modifyTime"]); err == nil {
		h.ModifyTime = n
	}
	if v, ok := m["srcIp"].(string); ok {
		h.SrcIP = v
	}
	if v, ok := m["srcUser"].(string); ok {
		h.SrcUser = v
	}
	if v, ok := m["opType"].(string); ok {
		h.OpType = v
	}
	if v, ok := m["publishType"].(string); ok {
		h.PublishType = v
	}
	return h
}

func decodeInt(raw any) (int64, error) {
	switch v := raw.(type) {
	case float64:
		return int64(v), nil
	case int:
		return int64(v), nil
	case int64:
		return v, nil
	default:
		return 0, fmt.Errorf("not a number")
	}
}

var errConfigSnapshotShape = snapshotShapeError("config snapshot shape mismatch")

type snapshotShapeError string

func (e snapshotShapeError) Error() string { return string(e) }
