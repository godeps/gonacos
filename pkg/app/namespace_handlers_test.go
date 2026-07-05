package app

import (
	"net/http"
	"net/url"
	"testing"
)

// TestNamespaceListConfigCountPopulated verifies that the namespace list
// endpoint populates ConfigCount from the config service. Before the fix,
// ConfigCount was always 0 regardless of how many configs the namespace
// held.
func TestNamespaceListConfigCountPopulated(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	// Publish two configs in the default public namespace.
	postForm(t, handler, http.MethodPost, "/nacos/v3/admin/cs/config", url.Values{
		"dataId":    {"cfg-a.json"},
		"groupName": {"DEFAULT_GROUP"},
		"content":   {`{"a":1}`},
		"type":      {"json"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/nacos/v3/admin/cs/config", url.Values{
		"dataId":    {"cfg-b.json"},
		"groupName": {"DEFAULT_GROUP"},
		"content":   {`{"b":2}`},
		"type":      {"json"},
	}, http.StatusOK)

	// Create a second namespace with one config.
	postForm(t, handler, http.MethodPost, "/v3/admin/core/namespace", url.Values{
		"namespaceId":   {"ns-count"},
		"namespaceName": {"ns-count"},
		"namespaceDesc": {"for count test"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/nacos/v3/admin/cs/config", url.Values{
		"dataId":      {"cfg-c.json"},
		"groupName":   {"DEFAULT_GROUP"},
		"content":     {`{"c":3}`},
		"type":        {"json"},
		"namespaceId": {"ns-count"},
	}, http.StatusOK)

	// List namespaces via the admin endpoint. The public namespace should
	// report ConfigCount=2; ns-count should report ConfigCount=1.
	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, http.StatusOK)
	arr, ok := body.Data.([]any)
	if !ok {
		t.Fatalf("list data: want []any, got %T", body.Data)
	}
	got := map[string]int{}
	for _, item := range arr {
		m, _ := item.(map[string]any)
		id, _ := m["namespace"].(string)
		// JSON numbers come back as float64.
		if n, ok := m["configCount"].(float64); ok {
			got[id] = int(n)
		} else {
			got[id] = 0
		}
	}
	if got["public"] != 2 {
		t.Errorf("public ConfigCount = %d, want 2", got["public"])
	}
	if got["ns-count"] != 1 {
		t.Errorf("ns-count ConfigCount = %d, want 1", got["ns-count"])
	}
}

// TestNamespaceListConfigCountEmpty verifies that a namespace with zero
// configs reports ConfigCount=0 (no spurious counts from unrelated
// namespaces).
func TestNamespaceListConfigCountEmpty(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/admin/core/namespace", url.Values{
		"namespaceId":   {"ns-empty"},
		"namespaceName": {"ns-empty"},
		"namespaceDesc": {"no configs"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/list", nil, http.StatusOK)
	arr, ok := body.Data.([]any)
	if !ok {
		t.Fatalf("list data: want []any, got %T", body.Data)
	}
	for _, item := range arr {
		m, _ := item.(map[string]any)
		if m["namespace"] == "ns-empty" {
			if n, _ := m["configCount"].(float64); n != 0 {
				t.Errorf("ns-empty ConfigCount = %v, want 0", n)
			}
			return
		}
	}
	t.Fatalf("ns-empty not found in list")
}
