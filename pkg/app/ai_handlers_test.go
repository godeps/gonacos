package app

import (
	"encoding/json"
	"net/http"
	"net/url"
	"testing"
)

func TestAIPromptFullLifecycleViaHTTP(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id":      {"p1"},
		"name":    {"greeting"},
		"content": {"Hello {{name}}"},
		"author":  {"alice"},
		"labels":  {"prod"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/submit", url.Values{
		"id": {"p1"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/publish", url.Values{
		"id": {"p1"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/online", url.Values{
		"id": {"p1"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/detail?id=p1", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info["state"] != "ONLINE" {
		t.Fatalf("state = %v, want ONLINE", info["state"])
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/client/ai/prompt?id=p1", nil, http.StatusOK)
	if body.Code != 0 {
		t.Fatalf("client query: %+v", body)
	}
}

func TestAIPromptPublishWithoutSubmitRequiresForce(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id": {"p2"}, "name": {"n"}, "content": {"c"},
	}, http.StatusOK)

	result := postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/publish", url.Values{
		"id": {"p2"},
	}, http.StatusBadRequest)
	if result.Code != 409 {
		t.Fatalf("publish without submit: %+v, want code 409", result)
	}

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/force-publish", url.Values{
		"id": {"p2"},
	}, http.StatusOK)
}

func TestAIPromptLabelsAndBizTags(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id": {"p3"}, "name": {"n"}, "content": {"c"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/prompt/labels", url.Values{
		"id": {"p3"}, "labels": {"prod,stable"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/prompt/label", url.Values{
		"id": {"p3"}, "label": {"latest"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodDelete, "/v3/console/ai/prompt/label", url.Values{
		"id": {"p3"}, "label": {"stable"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/prompt/biz-tags", url.Values{
		"id": {"p3"}, "bizTags": {"team1"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/detail?id=p3", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	labels, _ := info["labels"].([]any)
	if len(labels) != 2 {
		t.Fatalf("labels = %v, want 2", labels)
	}
}

func TestAIPromptDelete(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id": {"p4"}, "name": {"n"}, "content": {"c"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodDelete, "/v3/console/ai/prompt", url.Values{
		"id": {"p4"},
	}, http.StatusOK)

	result := postForm(t, handler, http.MethodDelete, "/v3/console/ai/prompt", url.Values{
		"id": {"p4"},
	}, http.StatusNotFound)
	if result.Code != 404 {
		t.Fatalf("re-delete: %+v, want 404", result)
	}
}

func TestAIPromptMissingIDReturns400(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	result := postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"name": {"n"}, "content": {"c"},
	}, http.StatusBadRequest)
	if result.Code != 10000 {
		t.Fatalf("code = %d, want 10000 (parameter missing)", result.Code)
	}
}

func TestAIPromptRedraftAndVersionDownload(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id": {"p5"}, "name": {"n"}, "content": {"v1 content"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/submit", url.Values{
		"id": {"p5"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/publish", url.Values{
		"id": {"p5"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/redraft", url.Values{
		"id": {"p5"}, "content": {"v2 content"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/force-publish", url.Values{
		"id": {"p5"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/versions?id=p5", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var versions []map[string]any
	if err := json.Unmarshal(data, &versions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/version/download?id=p5&version=v2", nil, http.StatusOK)
	if body.Data != "v2 content" {
		t.Fatalf("download = %v, want 'v2 content'", body.Data)
	}
}

func TestAISkillUploadBypassesDraft(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/skills/upload", url.Values{
		"id":      {"s1"},
		"name":    {"my-skill"},
		"content": {"skill content"},
		"author":  {"alice"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/skills?id=s1", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var info map[string]any
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if info["state"] != "PUBLISHED" {
		t.Fatalf("state = %v, want PUBLISHED", info["state"])
	}
}

func TestAISkillBatchUpload(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	items := `[{"id":"s2","name":"skill2","content":"c2"},{"id":"s3","name":"skill3","content":"c3"}]`
	body := postForm(t, handler, http.MethodPost, "/v3/console/ai/skills/upload/batch", url.Values{
		"items": {items},
	}, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var result struct {
		Uploaded []string `json:"uploaded"`
		Failed   []string `json:"failed"`
	}
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(result.Uploaded) != 2 || len(result.Failed) != 0 {
		t.Fatalf("result = %+v", result)
	}
}

func TestAIAgentSpecUploadAndClientSearch(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/agentspecs/upload", url.Values{
		"id":      {"a1"},
		"name":    {"spec1"},
		"content": {"spec content"},
		"author":  {"alice"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPost, "/v3/console/ai/agentspecs/online", url.Values{
		"id": {"a1"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/client/ai/agentspecs", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var results []map[string]any
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(results) != 1 || results[0]["id"] != "a1" {
		t.Fatalf("results = %+v", results)
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/client/ai/agentspecs/search?query=spec1", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	if err := json.Unmarshal(data, &results); err != nil {
		t.Fatalf("unmarshal search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("search results = %d, want 1", len(results))
	}
}

func TestAIMcpServerCRUD(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/mcp", url.Values{
		"id":       {"m1"},
		"name":     {"weather"},
		"protocol": {"http"},
		"endpoint": {"http://weather.example"},
		"tools":    {`[{"name":"get_weather"}]`},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/mcp", url.Values{
		"id":   {"m1"},
		"name": {"weather-v2"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/mcp?id=m1", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var srv map[string]any
	if err := json.Unmarshal(data, &srv); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if srv["name"] != "weather-v2" {
		t.Fatalf("name = %v", srv["name"])
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/ai/mcp/list", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/ai/mcp/importToolsFromMcp?id=m1", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var tools []map[string]any
	if err := json.Unmarshal(data, &tools); err != nil {
		t.Fatalf("unmarshal tools: %v", err)
	}
	if len(tools) != 1 || tools[0]["name"] != "get_weather" {
		t.Fatalf("tools = %+v", tools)
	}

	postForm(t, handler, http.MethodDelete, "/v3/console/ai/mcp", url.Values{
		"id": {"m1"},
	}, http.StatusOK)
}

func TestAIA2AAgentRegisterAndVersions(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/a2a", url.Values{
		"id":       {"a1"},
		"name":     {"agent"},
		"endpoint": {"http://a"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/a2a", url.Values{
		"id": {"a1"}, "version": {"v2"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/a2a/version/list?id=a1", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var versions []map[string]any
	if err := json.Unmarshal(data, &versions); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(versions) != 2 {
		t.Fatalf("versions = %d, want 2", len(versions))
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/ai/a2a/list", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal list: %v", err)
	}
	if len(list) != 1 {
		t.Fatalf("list = %d, want 1", len(list))
	}
}

func TestAIImportSources(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/import/sources", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var sources []map[string]any
	if err := json.Unmarshal(data, &sources); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(sources) == 0 || sources[0]["id"] != "builtin" {
		t.Fatalf("sources = %+v", sources)
	}
}

func TestAIPipelines(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/pipelines/list", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var pipelines []map[string]any
	if err := json.Unmarshal(data, &pipelines); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(pipelines) == 0 {
		t.Fatalf("no pipelines")
	}

	body = doJSON(t, handler, http.MethodGet, "/v3/console/ai/pipelines/default", nil, http.StatusOK)
	data, _ = json.Marshal(body.Data)
	var p map[string]any
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if p["pipelineId"] != "default" {
		t.Fatalf("pipelineId = %v", p["pipelineId"])
	}
}

func TestAICopilotDisabledByDefault(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	result := postForm(t, handler, http.MethodPost, "/v3/console/copilot/prompt/optimize", url.Values{
		"prompt": {"hello"},
	}, http.StatusServiceUnavailable)
	if result.Code != 501 {
		t.Fatalf("code = %d, want 501", result.Code)
	}
}

func TestAICopilotConfig(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/copilot/config", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var cfg map[string]string
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if cfg["provider"] == "" {
		t.Fatalf("config = %+v", cfg)
	}
}

func TestAIPromptMetadataRoundtrip(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id":       {"p6"},
		"name":     {"n"},
		"content":  {"c"},
		"metadata": {`{"k1":"v1","k2":"v2"}`},
	}, http.StatusOK)

	postForm(t, handler, http.MethodPut, "/v3/console/ai/prompt/metadata", url.Values{
		"id":       {"p6"},
		"metadata": {"k3=v3"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/metadata?id=p6", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var metadata map[string]string
	if err := json.Unmarshal(data, &metadata); err != nil {
		t.Fatalf("unmarshal: %v (data=%s)", err, data)
	}
	if metadata["k3"] != "v3" {
		t.Fatalf("metadata = %+v", metadata)
	}
}

func TestAIPromptAdminAndConsoleBothWork(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/admin/ai/prompt/draft", url.Values{
		"id": {"p7"}, "name": {"n"}, "content": {"c"},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/console/ai/prompt/draft", url.Values{
		"id": {"p8"}, "name": {"n"}, "content": {"c"},
	}, http.StatusOK)

	body := doJSON(t, handler, http.MethodGet, "/v3/admin/ai/prompt/list", nil, http.StatusOK)
	data, _ := json.Marshal(body.Data)
	var list []map[string]any
	if err := json.Unmarshal(data, &list); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("list = %d, want 2", len(list))
	}
}

func TestAIPromptGovernanceStub(t *testing.T) {
	t.Parallel()
	handler := NewHandler("../..")

	body := doJSON(t, handler, http.MethodGet, "/v3/console/ai/prompt/governance?id=p1", nil, http.StatusOK)
	if body.Code != 0 {
		t.Fatalf("governance: %+v", body)
	}
	data, _ := json.Marshal(body.Data)
	var gov map[string]any
	if err := json.Unmarshal(data, &gov); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if gov["id"] != "p1" {
		t.Fatalf("governance id = %v, want p1", gov["id"])
	}
}
