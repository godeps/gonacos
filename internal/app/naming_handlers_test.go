package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
)

func TestNamingServiceCreateViaConsole(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ns/service", url.Values{
		"namespaceId":      {"ns1"},
		"groupName":        {"DEFAULT_GROUP"},
		"serviceName":      {"svc1"},
		"ephemeral":        {"true"},
		"protectThreshold": {"0.5"},
		"metadata":         {"k=v"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service?namespaceId=ns1&groupName=DEFAULT_GROUP&serviceName=svc1", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal info: %v", err)
	}
	if info["name"] != "svc1" || info["ephemeral"] != true {
		t.Fatalf("info = %+v", info)
	}
}

func TestNamingServiceCreateDuplicateReturns409(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	form := url.Values{
		"namespaceId": {"ns2"}, "groupName": {"g"}, "serviceName": {"dup"},
	}
	postForm(t, handler, http.MethodPost, "/v3/console/ns/service", form, http.StatusOK)
	result := postForm(t, handler, http.MethodPost, "/v3/console/ns/service", form, http.StatusBadRequest)
	if result.Code != 409 {
		t.Fatalf("code = %d, want 409", result.Code)
	}
}

func TestNamingInstanceRegisterAndList(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/client/ns/instance", url.Values{
		"namespaceId": {"ns3"},
		"groupName":   {"g"},
		"serviceName": {"svc"},
		"clusterName": {"DEFAULT"},
		"ip":          {"10.0.0.1"},
		"port":        {"8080"},
		"weight":      {"1"},
		"healthy":     {"true"},
		"enabled":     {"true"},
		"ephemeral":   {"true"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/client/ns/instance/list?namespaceId=ns3&groupName=g&serviceName=svc&pageNo=1&pageSize=10", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		Count int `json:"count"`
		List  []struct {
			IP   string `json:"ip"`
			Port int    `json:"port"`
		} `json:"list"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal page: %v (data=%s)", err, data)
	}
	if page.Count != 1 || page.List[0].IP != "10.0.0.1" || page.List[0].Port != 8080 {
		t.Fatalf("page = %+v", page)
	}
}

func TestNamingInstanceRegisterMissingIPReturns400(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	result := postForm(t, handler, http.MethodPost, "/v3/client/ns/instance", url.Values{
		"namespaceId": {"ns4"},
		"groupName":   {"g"},
		"serviceName": {"svc"},
		"port":        {"8080"},
	}, http.StatusBadRequest)
	if result.Code != 10000 {
		t.Fatalf("code = %d, want 10000 (parameter missing)", result.Code)
	}
}

func TestNamingInstanceDeregister(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/client/ns/instance", url.Values{
		"namespaceId": {"ns5"}, "groupName": {"g"}, "serviceName": {"svc"},
		"ip": {"10.0.0.1"}, "port": {"8080"}, "ephemeral": {"true"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodDelete, "/v3/client/ns/instance", url.Values{
		"namespaceId": {"ns5"}, "groupName": {"g"}, "serviceName": {"svc"},
		"ip": {"10.0.0.1"}, "port": {"8080"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/client/ns/instance/list?namespaceId=ns5&groupName=g&serviceName=svc", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		Count int `json:"count"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.Count != 0 {
		t.Fatalf("count after deregister = %d, want 0", page.Count)
	}
}

func TestNamingServiceList(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	for _, name := range []string{"alpha", "beta"} {
		postForm(t, handler, http.MethodPost, "/v3/console/ns/service", url.Values{
			"namespaceId": {"ns6"}, "groupName": {"g"}, "serviceName": {name},
		}, http.StatusOK)
	}
	body := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service/list?namespaceId=ns6&pageNo=1&pageSize=10", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		Count       int `json:"count"`
		ServiceList []struct {
			Name string `json:"name"`
		} `json:"serviceList"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal: %v (data=%s)", err, data)
	}
	if page.Count != 2 {
		t.Fatalf("count = %d, want 2", page.Count)
	}
}

func TestNamingSelectorTypes(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service/selector/types", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var types []map[string]string
	if err := json.Unmarshal(data, &types); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(types) == 0 || types[0]["type"] == "" {
		t.Fatalf("selector types empty: %+v", types)
	}
}

func TestNamingClusterUpdate(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ns/service", url.Values{
		"namespaceId": {"ns7"}, "groupName": {"g"}, "serviceName": {"svc"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPut, "/v3/console/ns/service/cluster", url.Values{
		"namespaceId": {"ns7"}, "groupName": {"g"}, "serviceName": {"svc"},
		"clusterName": {"edge"}, "checkPort": {"9090"}, "useInstancePort4Check": {"true"},
		"healthChecker": {"type=tcp"}, "metadata": {"k=v"},
	}, http.StatusOK)
}

func TestNamingAdminInstanceRegisterAndPartialUpdate(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/admin/ns/instance", url.Values{
		"namespaceId": {"ns8"}, "groupName": {"g"}, "serviceName": {"svc"},
		"clusterName": {"DEFAULT"}, "ip": {"10.0.0.1"}, "port": {"8080"},
		"weight": {"1"}, "healthy": {"true"}, "enabled": {"true"}, "ephemeral": {"true"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPut, "/v3/admin/ns/instance/partial", url.Values{
		"namespaceId": {"ns8"}, "groupName": {"g"}, "serviceName": {"svc"},
		"clusterName": {"DEFAULT"}, "ip": {"10.0.0.1"}, "port": {"8080"},
		"weight": {"2"}, "metadata": {"zone=a"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/ns/instance?namespaceId=ns8&groupName=g&serviceName=svc&clusterName=DEFAULT", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var instances []struct {
		Weight  float64          `json:"weight"`
		Metadata map[string]string `json:"metadata"`
	}
	if err := json.Unmarshal(data, &instances); err != nil {
		t.Fatalf("unmarshal: %v (data=%s)", err, data)
	}
	if len(instances) != 1 || instances[0].Weight != 2 || instances[0].Metadata["zone"] != "a" {
		t.Fatalf("instances = %+v", instances)
	}
}

func TestNamingSubscribers(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ns/service", url.Values{
		"namespaceId": {"ns9"}, "groupName": {"g"}, "serviceName": {"svc"},
	}, http.StatusOK)
	body := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service/subscribers?namespaceId=ns9&groupName=g&serviceName=svc&pageNo=1&pageSize=10", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var page struct {
		TotalCount int `json:"totalCount"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if page.TotalCount != 0 {
		t.Fatalf("subscribers = %d, want 0", page.TotalCount)
	}
}

func TestNamingHealthCheckers(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/ns/health/checkers", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var checkers []map[string]string
	if err := json.Unmarshal(data, &checkers); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(checkers) == 0 {
		t.Fatalf("checkers empty")
	}
}

func TestNamingServiceUpdateAndDelete(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ns/service", url.Values{
		"namespaceId": {"ns10"}, "groupName": {"g"}, "serviceName": {"svc"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPut, "/v3/console/ns/service", url.Values{
		"namespaceId": {"ns10"}, "groupName": {"g"}, "serviceName": {"svc"},
		"ephemeral": {"false"}, "protectThreshold": {"0.3"}, "metadata": {"a=b"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodDelete, "/v3/console/ns/service", url.Values{
		"namespaceId": {"ns10"}, "groupName": {"g"}, "serviceName": {"svc"},
	}, http.StatusOK)
}

func TestNamingServiceGetMissingReturns400(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	missing := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service?groupName=g&serviceName=svc", nil, http.StatusBadRequest)
	if missing.Code != 10000 || !strings.Contains(missing.Message, "namespaceId") {
		t.Fatalf("missing = %+v", missing)
	}
	notFound := doJSON(t, handler, http.MethodGet, "/v3/console/ns/service?namespaceId=missing&groupName=g&serviceName=svc", nil, http.StatusNotFound)
	if notFound.Code != 404 {
		t.Fatalf("code = %d, want 404", notFound.Code)
	}
}
