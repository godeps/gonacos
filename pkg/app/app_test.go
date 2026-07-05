package app

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
)

type resultBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

func TestNewHandlerHealthEndpoints(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	tests := []struct {
		name string
		path string
	}{
		{name: "console liveness", path: "/v3/console/health/liveness"},
		{name: "console readiness with nacos prefix", path: "/nacos/v3/console/health/readiness"},
		{name: "admin liveness", path: "/v3/admin/core/state/liveness"},
		{name: "admin readiness with nacos prefix", path: "/nacos/v3/admin/core/state/readiness"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tt.path, nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
			}
			var body resultBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Code != 0 || body.Message != "success" || body.Data != "ok" {
				t.Fatalf("body = %+v, want success ok", body)
			}
		})
	}
}

func TestNewHandlerRegistersContractOperationStubs(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	// All manifest operations are now implemented. The previously-stubbed
	// /v3/admin/ns/client/distro now returns 200 instead of 501.
	req := httptest.NewRequest(http.MethodGet, "/nacos/v3/admin/ns/client/distro", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, http.StatusOK, rec.Body.String())
	}
}

func TestNewHandlerReturnsNacosResultForUnknownRoute(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	req := httptest.NewRequest(http.MethodGet, "/v3/missing", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
	var body resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if body.Code != 404 || body.Message != "resource not found" {
		t.Fatalf("body = %+v, want not found result", body)
	}
}

func TestNamespaceConsoleLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	namespaceID := "console_ns"

	postForm(t, handler, http.MethodPost, "/nacos/v3/console/core/namespace", url.Values{
		"customNamespaceId": {namespaceID},
		"namespaceName":     {"console namespace"},
		"namespaceDesc":     {"created"},
	}, http.StatusOK)

	detail := doJSON(t, handler, http.MethodGet, "/nacos/v3/console/core/namespace?namespaceId="+namespaceID, nil, http.StatusOK)
	data := detail.Data.(map[string]any)
	if data["namespace"] != namespaceID || data["namespaceShowName"] != "console namespace" {
		t.Fatalf("detail data = %+v", data)
	}

	list := doJSON(t, handler, http.MethodGet, "/nacos/v3/console/core/namespace/list", nil, http.StatusOK)
	if !listContainsNamespace(list.Data.([]any), namespaceID) {
		t.Fatalf("list missing namespace %q: %+v", namespaceID, list.Data)
	}

	exists := doJSON(t, handler, http.MethodGet, "/nacos/v3/console/core/namespace/exist?customNamespaceId="+namespaceID, nil, http.StatusOK)
	if exists.Data != true {
		t.Fatalf("exists data = %v, want true", exists.Data)
	}

	postForm(t, handler, http.MethodPut, "/nacos/v3/console/core/namespace", url.Values{
		"namespaceId":   {namespaceID},
		"namespaceName": {"console namespace updated"},
		"namespaceDesc": {"updated"},
	}, http.StatusOK)

	updated := doJSON(t, handler, http.MethodGet, "/nacos/v3/console/core/namespace?namespaceId="+namespaceID, nil, http.StatusOK)
	updatedData := updated.Data.(map[string]any)
	if updatedData["namespaceShowName"] != "console namespace updated" || updatedData["namespaceDesc"] != "updated" {
		t.Fatalf("updated data = %+v", updatedData)
	}

	postForm(t, handler, http.MethodDelete, "/nacos/v3/console/core/namespace", url.Values{
		"namespaceId": {namespaceID},
	}, http.StatusOK)
	existsAfterDelete := doJSON(t, handler, http.MethodGet, "/nacos/v3/console/core/namespace/exist?customNamespaceId="+namespaceID, nil, http.StatusOK)
	if existsAfterDelete.Data != false {
		t.Fatalf("exists after delete data = %v, want false", existsAfterDelete.Data)
	}
}

func TestNamespaceAdminLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	namespaceID := "admin_ns"

	postForm(t, handler, http.MethodPost, "/v3/admin/core/namespace", url.Values{
		"namespaceId":   {namespaceID},
		"namespaceName": {"admin namespace"},
		"namespaceDesc": {"created"},
	}, http.StatusOK)

	check := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/check?namespaceId="+namespaceID, nil, http.StatusOK)
	if check.Data != float64(1) {
		t.Fatalf("check data = %v, want 1", check.Data)
	}

	detail := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace?namespaceId="+namespaceID, nil, http.StatusOK)
	data := detail.Data.(map[string]any)
	if data["namespace"] != namespaceID || data["namespaceDesc"] != "created" {
		t.Fatalf("detail data = %+v", data)
	}

	postForm(t, handler, http.MethodPut, "/v3/admin/core/namespace", url.Values{
		"namespaceId":   {namespaceID},
		"namespaceName": {"admin namespace updated"},
		"namespaceDesc": {"updated"},
	}, http.StatusOK)

	postForm(t, handler, http.MethodDelete, "/v3/admin/core/namespace", url.Values{
		"namespaceId": {namespaceID},
	}, http.StatusOK)
	checkAfterDelete := doJSON(t, handler, http.MethodGet, "/v3/admin/core/namespace/check?namespaceId="+namespaceID, nil, http.StatusOK)
	if checkAfterDelete.Data != float64(0) {
		t.Fatalf("check after delete data = %v, want 0", checkAfterDelete.Data)
	}
}

func TestNamespaceValidationErrors(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	tests := []struct {
		name   string
		method string
		path   string
		form   url.Values
		want   string
	}{
		{
			name:   "console create missing namespace name",
			method: http.MethodPost,
			path:   "/v3/console/core/namespace",
			form:   url.Values{"customNamespaceId": {"missing_name"}},
			want:   "namespaceName is required",
		},
		{
			name:   "admin create missing namespace id",
			method: http.MethodPost,
			path:   "/v3/admin/core/namespace",
			form:   url.Values{"namespaceName": {"missing id"}},
			want:   "namespaceId is required",
		},
		{
			name:   "invalid namespace name",
			method: http.MethodPost,
			path:   "/v3/admin/core/namespace",
			form: url.Values{
				"namespaceId":   {"bad_name"},
				"namespaceName": {"bad@name"},
			},
			want: "invalid namespaceName",
		},
		{
			name:   "too long namespace id",
			method: http.MethodPost,
			path:   "/v3/admin/core/namespace",
			form: url.Values{
				"namespaceId":   {strings.Repeat("n", 129)},
				"namespaceName": {"too long"},
			},
			want: "too long namespaceId",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := postForm(t, handler, tt.method, tt.path, tt.form, http.StatusBadRequest)
			if !strings.Contains(body.Message, tt.want) {
				t.Fatalf("message = %q, want containing %q", body.Message, tt.want)
			}
		})
	}
}

func TestConfigAdminConsoleAndClientLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	form := url.Values{
		"dataId":     {"app.json"},
		"groupName":  {"DEFAULT_GROUP"},
		"content":    {`{"name":"initial"}`},
		"type":       {"json"},
		"desc":       {"initial config"},
		"configTags": {"tag-a,tag-b"},
	}
	postForm(t, handler, http.MethodPost, "/nacos/v3/admin/cs/config", form, http.StatusOK)

	adminDetail := doJSON(t, handler, http.MethodGet, "/nacos/v3/admin/cs/config?dataId=app.json&groupName=DEFAULT_GROUP", nil, http.StatusOK)
	adminData := adminDetail.Data.(map[string]any)
	if adminData["namespaceId"] != "public" || adminData["content"] != `{"name":"initial"}` || adminData["type"] != "json" {
		t.Fatalf("admin detail data = %+v", adminData)
	}
	if adminData["md5"] != "032fb56bbc6dd6328e0898bfa8c658bd" {
		t.Fatalf("md5 = %v", adminData["md5"])
	}

	postForm(t, handler, http.MethodPut, "/v3/admin/cs/config", url.Values{
		"dataId":     {"app.json"},
		"groupName":  {"DEFAULT_GROUP"},
		"desc":       {"updated desc"},
		"configTags": {"tag-c"},
	}, http.StatusOK)
	updated := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId=app.json&groupName=DEFAULT_GROUP", nil, http.StatusOK)
	updatedData := updated.Data.(map[string]any)
	if updatedData["desc"] != "updated desc" || updatedData["configTags"] != "tag-c" {
		t.Fatalf("updated detail data = %+v", updatedData)
	}

	client := doJSON(t, handler, http.MethodGet, "/v3/client/cs/config?dataId=app.json&groupName=DEFAULT_GROUP", nil, http.StatusOK)
	clientData := client.Data.(map[string]any)
	if clientData["content"] != `{"name":"initial"}` || clientData["contentType"] != "json" || clientData["success"] != true {
		t.Fatalf("client data = %+v", clientData)
	}
	if clientData["lastModified"].(float64) <= 0 {
		t.Fatalf("lastModified = %v, want positive", clientData["lastModified"])
	}

	list := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/list?pageNo=1&pageSize=10&groupName=DEFAULT_GROUP&dataId=app&search=blur", nil, http.StatusOK)
	page := list.Data.(map[string]any)
	if page["totalCount"] != float64(1) {
		t.Fatalf("list page = %+v", page)
	}

	postForm(t, handler, http.MethodDelete, "/v3/admin/cs/config", url.Values{
		"dataId":    {"app.json"},
		"groupName": {"DEFAULT_GROUP"},
	}, http.StatusOK)
	consoleMissing := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId=app.json&groupName=DEFAULT_GROUP", nil, http.StatusOK)
	if consoleMissing.Data != nil {
		t.Fatalf("console missing data = %v, want nil", consoleMissing.Data)
	}
	adminMissing := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config?dataId=app.json&groupName=DEFAULT_GROUP", nil, http.StatusNotFound)
	if adminMissing.Code != 404 {
		t.Fatalf("admin missing body = %+v", adminMissing)
	}
}

func TestConfigValidationErrors(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	tests := []struct {
		name   string
		method string
		path   string
		form   url.Values
		want   string
	}{
		{
			name:   "missing data id",
			method: http.MethodPost,
			path:   "/v3/admin/cs/config",
			form: url.Values{
				"groupName": {"DEFAULT_GROUP"},
				"content":   {"content"},
			},
			want: "dataId is required",
		},
		{
			name:   "missing group name",
			method: http.MethodPost,
			path:   "/v3/console/cs/config",
			form: url.Values{
				"dataId":  {"app"},
				"content": {"content"},
			},
			want: "groupName is required",
		},
		{
			name:   "missing content",
			method: http.MethodPost,
			path:   "/v3/admin/cs/config",
			form: url.Values{
				"dataId":    {"app"},
				"groupName": {"DEFAULT_GROUP"},
			},
			want: "content is required",
		},
		{
			name:   "legacy group does not replace groupName",
			method: http.MethodPost,
			path:   "/v3/admin/cs/config",
			form: url.Values{
				"dataId":  {"app"},
				"group":   {"DEFAULT_GROUP"},
				"content": {"content"},
			},
			want: "groupName is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := postForm(t, handler, tt.method, tt.path, tt.form, http.StatusBadRequest)
			if !strings.Contains(body.Message, tt.want) {
				t.Fatalf("message = %q, want containing %q", body.Message, tt.want)
			}
		})
	}
}

func TestConfigBatchDeleteAndClone(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {"source.json"},
		"groupName": {"GROUP_A"},
		"content":   {"clone-content"},
		"type":      {"json"},
	}, http.StatusOK)
	source := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config?dataId=source.json&groupName=GROUP_A", nil, http.StatusOK)
	sourceID := source.Data.(map[string]any)["id"].(string)

	adminClone := postJSON(t, handler, "/v3/admin/cs/config/clone?namespaceId=public&policy=OVERWRITE",
		`[{"configId":`+sourceID+`,"targetDataId":"admin-target.json","targetGroupName":"GROUP_B"}]`, http.StatusOK)
	cloneData := adminClone.Data.(map[string]any)
	if cloneData["succCount"] != float64(1) {
		t.Fatalf("admin clone data = %+v", cloneData)
	}
	adminTarget := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config?dataId=admin-target.json&groupName=GROUP_B", nil, http.StatusOK)
	if adminTarget.Data.(map[string]any)["content"] != "clone-content" {
		t.Fatalf("admin target = %+v", adminTarget.Data)
	}

	consoleClone := postJSON(t, handler, "/v3/console/cs/config/clone?targetNamespaceId=public&policy=OVERWRITE",
		`[{"cfgId":"`+sourceID+`","dataId":"console-target.json","group":"GROUP_C"}]`, http.StatusOK)
	consoleCloneData := consoleClone.Data.(map[string]any)
	if consoleCloneData["succCount"] != float64(1) {
		t.Fatalf("console clone data = %+v", consoleCloneData)
	}

	firstID := adminTarget.Data.(map[string]any)["id"].(string)
	consoleTarget := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId=console-target.json&groupName=GROUP_C", nil, http.StatusOK)
	secondID := consoleTarget.Data.(map[string]any)["id"].(string)
	batch := doJSON(t, handler, http.MethodDelete, "/v3/console/cs/config/batchDelete?ids="+url.QueryEscape(firstID+","+secondID+",999999"), nil, http.StatusOK)
	if batch.Data != true {
		t.Fatalf("batch data = %v, want true", batch.Data)
	}
	if got := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId=admin-target.json&groupName=GROUP_B", nil, http.StatusOK); got.Data != nil {
		t.Fatalf("admin-target after batch delete = %v, want nil", got.Data)
	}
	if got := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId=console-target.json&groupName=GROUP_C", nil, http.StatusOK); got.Data != nil {
		t.Fatalf("console-target after batch delete = %v, want nil", got.Data)
	}

	adminBatch := doJSON(t, handler, http.MethodDelete, "/v3/admin/cs/config/batch?ids="+sourceID, nil, http.StatusOK)
	if adminBatch.Data != true {
		t.Fatalf("admin batch data = %v, want true", adminBatch.Data)
	}
}

func TestConfigCloneAndBatchDeleteValidation(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	missingIDs := doJSON(t, handler, http.MethodDelete, "/v3/admin/cs/config/batch", nil, http.StatusBadRequest)
	if !strings.Contains(missingIDs.Message, "ids") {
		t.Fatalf("missing ids message = %q", missingIDs.Message)
	}
	missingNamespace := postJSON(t, handler, "/v3/console/cs/config/clone", `[]`, http.StatusBadRequest)
	if !strings.Contains(missingNamespace.Message, "namespace") {
		t.Fatalf("missing namespace message = %q", missingNamespace.Message)
	}
	empty := postJSON(t, handler, "/v3/console/cs/config/clone?targetNamespaceId=public", `[]`, http.StatusOK)
	if empty.Code != 20001 {
		t.Fatalf("empty clone code = %d, want no selected config", empty.Code)
	}
	absent := postJSON(t, handler, "/v3/admin/cs/config/clone?namespaceId=public",
		`[{"configId":`+strconv.Itoa(999999)+`,"targetDataId":"absent","targetGroupName":"absent"}]`, http.StatusOK)
	if absent.Code != 20002 {
		t.Fatalf("absent clone code = %d, want data empty", absent.Code)
	}
}

func TestConfigHistoryLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "history.json"
	groupName := "HISTORY_GROUP"
	firstContent := "history-first-content"
	secondContent := "history-second-content"

	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {firstContent},
	}, http.StatusOK)
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {secondContent},
	}, http.StatusOK)

	current := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	currentData := current.Data.(map[string]any)
	if currentData["content"] != secondContent {
		t.Fatalf("current config = %+v", currentData)
	}

	list := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history/list?pageNo=1&pageSize=1000&dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	page := list.Data.(map[string]any)
	if page["pageNumber"] != float64(1) || page["totalCount"] != float64(2) {
		t.Fatalf("history page = %+v", page)
	}
	items := page["pageItems"].([]any)
	newest := items[0].(map[string]any)
	oldest := items[1].(map[string]any)
	if newest["dataId"] != dataID || newest["groupName"] != groupName || !strings.HasPrefix(newest["opType"].(string), "U") {
		t.Fatalf("newest history = %+v", newest)
	}
	if !strings.HasPrefix(oldest["opType"].(string), "I") {
		t.Fatalf("oldest history = %+v", oldest)
	}

	nid := formatJSONID(newest["id"])
	detail := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history?dataId="+dataID+"&groupName="+groupName+"&nid="+nid, nil, http.StatusOK)
	detailData := detail.Data.(map[string]any)
	if detailData["content"] != firstContent || detailData["md5"] != "a9e9b76fd88f79e7517b13d3bf9a1ae6" || detailData["publishType"] != "formal" {
		t.Fatalf("history detail = %+v", detailData)
	}

	configID := url.QueryEscape(currentData["id"].(string))
	previous := doJSON(t, handler, http.MethodGet, "/v3/console/cs/history/previous?dataId="+dataID+"&groupName="+groupName+"&id="+configID, nil, http.StatusOK)
	previousData := previous.Data.(map[string]any)
	if previousData["content"] != firstContent || formatJSONID(previousData["id"]) != nid {
		t.Fatalf("previous history = %+v", previousData)
	}

	configs := doJSON(t, handler, http.MethodGet, "/v3/console/cs/history/configs?namespaceId=public", nil, http.StatusOK)
	if !listContainsConfig(configs.Data.([]any), dataID, groupName) {
		t.Fatalf("namespace configs missing %s/%s: %+v", dataID, groupName, configs.Data)
	}

	mismatch := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history?dataId=wrong-"+dataID+"&groupName="+groupName+"&nid="+nid, nil, http.StatusForbidden)
	if mismatch.Code != 403 || !strings.Contains(mismatch.Message, "access") {
		t.Fatalf("mismatch body = %+v", mismatch)
	}
}

func TestConfigHistoryValidation(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "history-validation.json"
	groupName := "HISTORY_VALIDATION_GROUP"

	missingGroup := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history/list?dataId="+dataID+"&pageNo=1&pageSize=10", nil, http.StatusBadRequest)
	if !strings.Contains(missingGroup.Message, "groupName") {
		t.Fatalf("missing group message = %q", missingGroup.Message)
	}
	invalidPageNo := doJSON(t, handler, http.MethodGet, "/v3/console/cs/history/list?dataId="+dataID+"&groupName="+groupName+"&pageNo=0&pageSize=10", nil, http.StatusBadRequest)
	if !strings.Contains(invalidPageNo.Message, "pageNo") {
		t.Fatalf("invalid pageNo message = %q", invalidPageNo.Message)
	}
	invalidPageSize := doJSON(t, handler, http.MethodGet, "/v3/console/cs/history/list?dataId="+dataID+"&groupName="+groupName+"&pageNo=1&pageSize=0", nil, http.StatusBadRequest)
	if !strings.Contains(invalidPageSize.Message, "pageSize") {
		t.Fatalf("invalid pageSize message = %q", invalidPageSize.Message)
	}
	missingNID := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history?dataId="+dataID+"&groupName="+groupName, nil, http.StatusBadRequest)
	if !strings.Contains(missingNID.Message, "nid") {
		t.Fatalf("missing nid message = %q", missingNID.Message)
	}
	absent := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history?dataId="+dataID+"&groupName="+groupName+"&nid=999999999999", nil, http.StatusBadRequest)
	if !strings.Contains(absent.Message, "source must not be null") {
		t.Fatalf("absent message = %q", absent.Message)
	}
	missingNamespace := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history/configs", nil, http.StatusBadRequest)
	if !strings.Contains(missingNamespace.Message, "namespaceId") {
		t.Fatalf("missing namespace message = %q", missingNamespace.Message)
	}
	invalidNamespace := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/history/configs?namespaceId=invalid%20namespace", nil, http.StatusBadRequest)
	if !strings.Contains(invalidNamespace.Message, "namespaceId") {
		t.Fatalf("invalid namespace message = %q", invalidNamespace.Message)
	}
}

func TestConfigExportAndImportZip(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "export.json"
	groupName := "EXPORT_GROUP"
	content := "export-content"

	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {content},
		"type":      {"text"},
		"desc":      {"export desc"},
	}, http.StatusOK)
	config := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	configID := config.Data.(map[string]any)["id"].(string)

	rec := serveRaw(t, handler, httptest.NewRequest(http.MethodGet, "/v3/admin/cs/config/export?ids="+url.QueryEscape(configID), nil), http.StatusOK)
	if disposition := rec.Header().Get("Content-Disposition"); !strings.HasPrefix(disposition, "attachment;filename=nacos_config_export_") {
		t.Fatalf("content disposition = %q", disposition)
	}
	entries := unzipEntries(t, rec.Body.Bytes())
	if entries[groupName+"/"+dataID] != content {
		t.Fatalf("zip entries = %+v", entries)
	}
	metadata := entries[".metadata.yml"]
	if !strings.Contains(metadata, "dataId: "+dataID) || !strings.Contains(metadata, "group: "+groupName) || !strings.Contains(metadata, "type: text") {
		t.Fatalf("metadata = %q", metadata)
	}

	importDataID := "import.json"
	importGroup := "IMPORT_GROUP"
	importBody := buildImportZip(t, importDataID, importGroup, "import-content", "import desc")
	imported := postMultipart(t, handler, "/v3/console/cs/config/import?policy=ABORT", "file", "nacos-import.zip", importBody, http.StatusOK)
	importedData := imported.Data.(map[string]any)
	if importedData["succCount"] != float64(1) {
		t.Fatalf("import data = %+v", importedData)
	}
	got := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config?dataId="+importDataID+"&groupName="+importGroup, nil, http.StatusOK)
	gotData := got.Data.(map[string]any)
	if gotData["content"] != "import-content" || gotData["desc"] != "import desc" || gotData["type"] != "text" {
		t.Fatalf("imported config = %+v", gotData)
	}

	consoleExport := serveRaw(t, handler, httptest.NewRequest(http.MethodGet, "/v3/console/cs/config/export2?ids="+url.QueryEscape(configID), nil), http.StatusOK)
	if len(unzipEntries(t, consoleExport.Body.Bytes())) == 0 {
		t.Fatalf("console export returned empty zip")
	}
}

func TestConfigImportExportValidation(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	invalidNamespace := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/export?ids=1&namespaceId=invalid%20namespace", nil, http.StatusBadRequest)
	if !strings.Contains(invalidNamespace.Message, "namespaceId") {
		t.Fatalf("invalid namespace message = %q", invalidNamespace.Message)
	}

	missingFile := doJSON(t, handler, http.MethodPost, "/v3/admin/cs/config/import?policy=ABORT", nil, http.StatusOK)
	if missingFile.Code != 100005 || missingFile.Message != "imported file data is empty" {
		t.Fatalf("missing file result = %+v", missingFile)
	}

	badMetadata := postMultipart(t, handler, "/v3/console/cs/config/import?policy=ABORT", "file", "bad-import.zip", buildBadMetadataZip(t), http.StatusOK)
	if badMetadata.Code != 100002 || badMetadata.Message != "imported metadata is invalid" {
		t.Fatalf("bad metadata result = %+v", badMetadata)
	}
}

func TestConfigListenerQueries(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "listener.json"
	groupName := "LISTENER_GROUP"
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {"listener-content"},
	}, http.StatusOK)

	adminConfig := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/listener?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	assertListenerData(t, adminConfig.Data, "config")
	consoleConfig := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/listener?dataId="+dataID+"&groupName="+groupName+"&aggregation=false", nil, http.StatusOK)
	assertListenerData(t, consoleConfig.Data, "config")

	adminIP := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/listener?ip=127.0.0.1&all=true&namespaceId=public&aggregation=false", nil, http.StatusOK)
	assertListenerData(t, adminIP.Data, "ip")
	consoleIP := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/listener/ip?ip=127.0.0.1", nil, http.StatusOK)
	assertListenerData(t, consoleIP.Data, "ip")
}

func TestConfigListenerValidation(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	missingDataID := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/listener?groupName=GROUP", nil, http.StatusBadRequest)
	if missingDataID.Code != 10000 || !strings.Contains(missingDataID.Message, "dataId") {
		t.Fatalf("missing dataId body = %+v", missingDataID)
	}
	missingGroup := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/listener?dataId=app", nil, http.StatusBadRequest)
	if missingGroup.Code != 10000 || !strings.Contains(missingGroup.Message, "groupName") {
		t.Fatalf("missing groupName body = %+v", missingGroup)
	}
	missingIP := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/listener/ip", nil, http.StatusBadRequest)
	if missingIP.Code != 10000 || !strings.Contains(missingIP.Message, "ip") {
		t.Fatalf("missing ip body = %+v", missingIP)
	}
	adminMissingIP := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/listener", nil, http.StatusBadRequest)
	if adminMissingIP.Code != 10000 || !strings.Contains(adminMissingIP.Message, "ip") {
		t.Fatalf("admin missing ip body = %+v", adminMissingIP)
	}
}

func TestConfigBetaLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "beta.json"
	groupName := "BETA_GROUP"
	content := "beta-content"
	betaIPs := "127.0.0.1"

	postFormWithHeaders(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {content},
	}, map[string]string{"betaIps": betaIPs}, http.StatusOK)

	adminBeta := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/beta?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	adminData := adminBeta.Data.(map[string]any)
	if adminData["dataId"] != dataID || adminData["groupName"] != groupName || adminData["namespaceId"] != "public" {
		t.Fatalf("admin beta identity = %+v", adminData)
	}
	if adminData["content"] != content || adminData["grayName"] != "beta" || !strings.Contains(adminData["grayRule"].(string), betaIPs) {
		t.Fatalf("admin beta data = %+v", adminData)
	}

	consoleBeta := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/beta?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	consoleData := consoleBeta.Data.(map[string]any)
	if consoleData["content"] != content || consoleData["grayName"] != "beta" {
		t.Fatalf("console beta data = %+v", consoleData)
	}

	deleted := doJSON(t, handler, http.MethodDelete, "/v3/admin/cs/config/beta?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	if deleted.Data != true {
		t.Fatalf("delete beta data = %v, want true", deleted.Data)
	}
	adminMissing := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/beta?dataId="+dataID+"&groupName="+groupName, nil, http.StatusNotFound)
	if adminMissing.Code != 404 || !strings.Contains(adminMissing.Message, "not in beta") {
		t.Fatalf("admin missing beta = %+v", adminMissing)
	}
	consoleMissing := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/beta?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	if consoleMissing.Data != nil {
		t.Fatalf("console missing beta data = %v, want nil", consoleMissing.Data)
	}
}

func TestConfigBetaValidation(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	missingDataID := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/beta?groupName=GROUP", nil, http.StatusBadRequest)
	if missingDataID.Code != 10000 || !strings.Contains(missingDataID.Message, "dataId") {
		t.Fatalf("missing dataId beta = %+v", missingDataID)
	}
	missingGroup := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/beta?dataId=app", nil, http.StatusBadRequest)
	if missingGroup.Code != 10000 || !strings.Contains(missingGroup.Message, "groupName") {
		t.Fatalf("missing groupName beta = %+v", missingGroup)
	}
	absentAdmin := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/beta?dataId=absent&groupName=GROUP", nil, http.StatusNotFound)
	if absentAdmin.Code != 404 || !strings.Contains(absentAdmin.Message, "not in beta") {
		t.Fatalf("absent admin beta = %+v", absentAdmin)
	}
	absentConsole := doJSON(t, handler, http.MethodGet, "/v3/console/cs/config/beta?dataId=absent&groupName=GROUP", nil, http.StatusOK)
	if absentConsole.Data != nil {
		t.Fatalf("absent console beta = %v, want nil", absentConsole.Data)
	}
}

func TestConfigGrayLifecycle(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "gray.json"
	groupName := "GRAY_GROUP"
	content := "gray-content"
	grayName := "gray-v1"
	grayRule := `{"betaIps":"10.0.0.1,10.0.0.2"}`

	// Publish a regular config first so the gray has a base.
	regularContent := "regular-content"
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {regularContent},
	}, http.StatusOK)

	// Publish the gray version.
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config/gray", url.Values{
		"dataId":           {dataID},
		"groupName":        {groupName},
		"content":          {content},
		"grayName":         {grayName},
		"grayMatchRuleExp": {grayRule},
		"grayVersion":      {"v1"},
	}, http.StatusOK)

	// Query the gray by name.
	gray := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/gray?dataId="+dataID+"&groupName="+groupName+"&grayName="+grayName, nil, http.StatusOK)
	gData := gray.Data.(map[string]any)
	if gData["content"] != content || gData["grayName"] != grayName {
		t.Fatalf("gray data = %+v", gData)
	}
	if !strings.Contains(gData["grayRule"].(string), "10.0.0.1") {
		t.Fatalf("gray rule = %v, want betaIps", gData["grayRule"])
	}

	// List grays for the config.
	list := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/gray?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	items, _ := list.Data.([]any)
	if len(items) != 1 {
		t.Fatalf("gray list = %+v, want 1 item", list.Data)
	}

	// Delete the gray.
	deleted := doJSON(t, handler, http.MethodDelete, "/v3/admin/cs/config/gray?dataId="+dataID+"&groupName="+groupName+"&grayName="+grayName, nil, http.StatusOK)
	if deleted.Data != true {
		t.Fatalf("delete gray data = %v, want true", deleted.Data)
	}

	// Query the gray again -> not found.
	missing := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/gray?dataId="+dataID+"&groupName="+groupName+"&grayName="+grayName, nil, http.StatusNotFound)
	if missing.Code != 404 {
		t.Fatalf("missing gray code = %d, want 404", missing.Code)
	}
}

func TestConfigGrayClientQuery(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "gray-client.json"
	groupName := "GRAY_CLIENT_GROUP"
	regularContent := "regular-content"
	grayContent := "gray-content"
	grayName := "gray-ip"
	grayRule := `{"betaIps":"10.0.0.5"}`

	// Publish regular config.
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {regularContent},
	}, http.StatusOK)

	// Publish gray targeting 10.0.0.5.
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config/gray", url.Values{
		"dataId":           {dataID},
		"groupName":        {groupName},
		"content":          {grayContent},
		"grayName":         {grayName},
		"grayMatchRuleExp": {grayRule},
	}, http.StatusOK)

	// Gray IP should get gray content.
	betaReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	betaReq.RemoteAddr = "10.0.0.5:12345"
	betaRec := httptest.NewRecorder()
	handler.ServeHTTP(betaRec, betaReq)
	var betaResp struct {
		Data struct {
			Content string `json:"content"`
			Beta    bool   `json:"beta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(betaRec.Body).Decode(&betaResp); err != nil {
		t.Fatalf("decode gray response: %v", err)
	}
	if betaResp.Data.Content != grayContent {
		t.Fatalf("gray IP content = %q, want %q", betaResp.Data.Content, grayContent)
	}
	if !betaResp.Data.Beta {
		t.Fatalf("gray IP beta flag = false, want true")
	}

	// Non-gray IP should get regular content.
	regularReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	regularReq.RemoteAddr = "10.0.0.99:54321"
	regularRec := httptest.NewRecorder()
	handler.ServeHTTP(regularRec, regularReq)
	var regularResp struct {
		Data struct {
			Content string `json:"content"`
			Beta    bool   `json:"beta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(regularRec.Body).Decode(&regularResp); err != nil {
		t.Fatalf("decode regular response: %v", err)
	}
	if regularResp.Data.Content != regularContent {
		t.Fatalf("regular IP content = %q, want %q", regularResp.Data.Content, regularContent)
	}
	if regularResp.Data.Beta {
		t.Fatalf("regular IP beta flag = true, want false")
	}
}

func TestConfigListenerTracking(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "tracked-listener.json"
	groupName := "TRACKED_GROUP"
	content := "tracked-content"

	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {content},
	}, http.StatusOK)

	// Client query registers the IP as a listener.
	clientReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	clientReq.RemoteAddr = "192.168.1.50:54321"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, clientReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("client query status = %d, want 200", rec.Code)
	}

	// Config-scoped listener query should now show the IP.
	adminConfig := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/config/listener?dataId="+dataID+"&groupName="+groupName, nil, http.StatusOK)
	cfgData := adminConfig.Data.(map[string]any)
	if cfgData["queryType"] != "config" {
		t.Fatalf("queryType = %v, want config", cfgData["queryType"])
	}
	status := cfgData["listenersStatus"].(map[string]any)
	if len(status) == 0 {
		t.Fatalf("listenersStatus = %+v, want non-empty", status)
	}
	if _, ok := status["192.168.1.50"]; !ok {
		t.Fatalf("listenersStatus = %+v, want key 192.168.1.50", status)
	}

	// IP-scoped listener query should show the config.
	adminIP := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/listener?ip=192.168.1.50&namespaceId=public", nil, http.StatusOK)
	ipData := adminIP.Data.(map[string]any)
	if ipData["queryType"] != "ip" {
		t.Fatalf("ip queryType = %v, want ip", ipData["queryType"])
	}
	ipStatus := ipData["listenersStatus"].(map[string]any)
	if len(ipStatus) == 0 {
		t.Fatalf("ip listenersStatus = %+v, want non-empty", ipStatus)
	}
}

func TestConfigBetaClientQuery(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	dataID := "beta-client.json"
	groupName := "BETA_CLIENT_GROUP"
	regularContent := "regular-content"
	betaContent := "beta-content"

	// Publish regular config.
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {regularContent},
	}, http.StatusOK)

	// Publish beta config targeting 10.0.0.5.
	postFormWithHeaders(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {betaContent},
	}, map[string]string{"betaIps": "10.0.0.5"}, http.StatusOK)

	// Beta IP should get beta content.
	betaReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	betaReq.RemoteAddr = "10.0.0.5:12345"
	betaRec := httptest.NewRecorder()
	handler.ServeHTTP(betaRec, betaReq)
	var betaResp struct {
		Data struct {
			Content string `json:"content"`
			Beta    bool   `json:"beta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(betaRec.Body).Decode(&betaResp); err != nil {
		t.Fatalf("decode beta response: %v", err)
	}
	if betaResp.Data.Content != betaContent {
		t.Fatalf("beta IP content = %q, want %q", betaResp.Data.Content, betaContent)
	}
	if !betaResp.Data.Beta {
		t.Fatalf("beta IP beta flag = false, want true")
	}

	// Non-beta IP should get regular content.
	regularReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	regularReq.RemoteAddr = "10.0.0.99:54321"
	regularRec := httptest.NewRecorder()
	handler.ServeHTTP(regularRec, regularReq)
	var regularResp struct {
		Data struct {
			Content string `json:"content"`
			Beta    bool   `json:"beta"`
		} `json:"data"`
	}
	if err := json.NewDecoder(regularRec.Body).Decode(&regularResp); err != nil {
		t.Fatalf("decode regular response: %v", err)
	}
	if regularResp.Data.Content != regularContent {
		t.Fatalf("regular IP content = %q, want %q", regularResp.Data.Content, regularContent)
	}
	if regularResp.Data.Beta {
		t.Fatalf("regular IP beta flag = true, want false")
	}
}

func TestConfigCapacityAndMetrics(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")
	groupName := "CAPACITY_GROUP"
	dataID := "capacity.json"

	// Publish a config so usage > 0.
	postForm(t, handler, http.MethodPost, "/v3/admin/cs/config", url.Values{
		"dataId":    {dataID},
		"groupName": {groupName},
		"content":   {"capacity-content"},
	}, http.StatusOK)

	// Query capacity.
	capResp := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/capacity?groupName="+groupName, nil, http.StatusOK)
	capData := capResp.Data.(map[string]any)
	if capData["groupName"] != groupName {
		t.Fatalf("capacity groupName = %v, want %s", capData["groupName"], groupName)
	}
	if capData["quota"] == nil || capData["quota"].(float64) <= 0 {
		t.Fatalf("capacity quota = %v, want > 0", capData["quota"])
	}
	usage := capData["usage"].(float64)
	if usage < 1 {
		t.Fatalf("capacity usage = %v, want >= 1", usage)
	}

	// Update capacity.
	updateResp := doJSON(t, handler, http.MethodPost, "/v3/admin/cs/capacity?groupName="+groupName+"&quota=500&maxSize=2048", nil, http.StatusOK)
	if updateResp.Data != true {
		t.Fatalf("capacity update data = %v, want true", updateResp.Data)
	}

	// Verify updated.
	capResp2 := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/capacity?groupName="+groupName, nil, http.StatusOK)
	capData2 := capResp2.Data.(map[string]any)
	if capData2["quota"].(float64) != 500 {
		t.Fatalf("capacity quota = %v, want 500", capData2["quota"])
	}
	if capData2["maxSize"].(float64) != 2048 {
		t.Fatalf("capacity maxSize = %v, want 2048", capData2["maxSize"])
	}

	// Client query to register a listener.
	clientReq := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+groupName, nil)
	clientReq.RemoteAddr = "172.16.0.10:9999"
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, clientReq)

	// Local client metrics.
	metricsResp := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/metrics/ip?ip=172.16.0.10", nil, http.StatusOK)
	metricsData := metricsResp.Data.(map[string]any)
	if metricsData["ip"] != "172.16.0.10" {
		t.Fatalf("metrics ip = %v, want 172.16.0.10", metricsData["ip"])
	}
	if metricsData["listenerCount"].(float64) < 1 {
		t.Fatalf("metrics listenerCount = %v, want >= 1", metricsData["listenerCount"])
	}

	// Cluster client metrics.
	clusterResp := doJSON(t, handler, http.MethodGet, "/v3/admin/cs/metrics/cluster?ip=172.16.0.10", nil, http.StatusOK)
	clusterData := clusterResp.Data.(map[string]any)
	if clusterData["nodeCount"].(float64) < 1 {
		t.Fatalf("cluster nodeCount = %v, want >= 1", clusterData["nodeCount"])
	}

	// Local cache refresh (no-op).
	refreshResp := doJSON(t, handler, http.MethodPost, "/v3/admin/cs/ops/localCache", nil, http.StatusOK)
	refreshData := refreshResp.Data.(map[string]any)
	if refreshData["refreshed"] != true {
		t.Fatalf("localCache refreshed = %v, want true", refreshData["refreshed"])
	}
}

func TestConfigOpsLogLevel(t *testing.T) {
	t.Parallel()

	handler := NewHandler("../..")

	// Set config module log level.
	resp := doJSON(t, handler, http.MethodPut, "/v3/admin/cs/ops/log?logLevel=DEBUG", nil, http.StatusOK)
	if resp.Data != "DEBUG" {
		t.Fatalf("cs ops/log data = %v, want DEBUG", resp.Data)
	}

	// Verify via core ops/log endpoint.
	coreResp := doJSON(t, handler, http.MethodPut, "/v3/admin/core/ops/log?logLevel=INFO", nil, http.StatusOK)
	if coreResp.Data != "INFO" {
		t.Fatalf("core ops/log data = %v, want INFO", coreResp.Data)
	}
}

func postForm(t *testing.T, handler http.Handler, method, path string, form url.Values, wantStatus int) resultBody {
	t.Helper()

	return postFormWithHeaders(t, handler, method, path, form, nil, wantStatus)
}

func postFormWithHeaders(t *testing.T, handler http.Handler, method, path string, form url.Values, headers map[string]string, wantStatus int) resultBody {
	t.Helper()

	req := httptest.NewRequest(method, path, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return serveJSON(t, handler, req, wantStatus)
}

func doJSON(t *testing.T, handler http.Handler, method, path string, body *strings.Reader, wantStatus int) resultBody {
	t.Helper()

	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
	}
	return serveJSON(t, handler, req, wantStatus)
}

func doJSONWithHeaders(t *testing.T, handler http.Handler, method, path string, body *strings.Reader, headers map[string]string, wantStatus int) resultBody {
	t.Helper()

	var req *http.Request
	if body == nil {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, body)
	}
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return serveJSON(t, handler, req, wantStatus)
}

func postJSON(t *testing.T, handler http.Handler, path, body string, wantStatus int) resultBody {
	t.Helper()

	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return serveJSON(t, handler, req, wantStatus)
}

func postMultipart(t *testing.T, handler http.Handler, path, fieldName, fileName string, body []byte, wantStatus int) resultBody {
	t.Helper()

	var requestBody bytes.Buffer
	writer := multipart.NewWriter(&requestBody)
	part, err := writer.CreateFormFile(fieldName, fileName)
	if err != nil {
		t.Fatalf("create multipart file: %v", err)
	}
	if _, err := part.Write(body); err != nil {
		t.Fatalf("write multipart file: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close multipart writer: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, path, &requestBody)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return serveJSON(t, handler, req, wantStatus)
}

func serveJSON(t *testing.T, handler http.Handler, req *http.Request, wantStatus int) resultBody {
	t.Helper()

	rec := serveRaw(t, handler, req, wantStatus)
	var body resultBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

func serveRaw(t *testing.T, handler http.Handler, req *http.Request, wantStatus int) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != wantStatus {
		t.Fatalf("status = %d, want %d, body = %s", rec.Code, wantStatus, rec.Body.String())
	}
	return rec
}

func listContainsNamespace(items []any, namespaceID string) bool {
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok && row["namespace"] == namespaceID {
			return true
		}
	}
	return false
}

func listContainsConfig(items []any, dataID, groupName string) bool {
	for _, item := range items {
		row, ok := item.(map[string]any)
		if ok && row["dataId"] == dataID && row["groupName"] == groupName {
			return true
		}
	}
	return false
}

func assertListenerData(t *testing.T, data any, queryType string) {
	t.Helper()

	row := data.(map[string]any)
	if row["queryType"] != queryType {
		t.Fatalf("listener queryType = %v, want %s", row["queryType"], queryType)
	}
	status, ok := row["listenersStatus"].(map[string]any)
	if !ok {
		t.Fatalf("listenersStatus = %#v, want object", row["listenersStatus"])
	}
	if len(status) != 0 {
		t.Fatalf("listenersStatus = %+v, want empty", status)
	}
}

func formatJSONID(value any) string {
	switch v := value.(type) {
	case string:
		return v
	case float64:
		return strconv.FormatInt(int64(v), 10)
	default:
		return ""
	}
}

func unzipEntries(t *testing.T, body []byte) map[string]string {
	t.Helper()

	reader, err := zip.NewReader(bytes.NewReader(body), int64(len(body)))
	if err != nil {
		t.Fatalf("open zip: %v", err)
	}
	entries := map[string]string{}
	for _, file := range reader.File {
		rc, err := file.Open()
		if err != nil {
			t.Fatalf("open zip entry %s: %v", file.Name, err)
		}
		content, readErr := io.ReadAll(rc)
		closeErr := rc.Close()
		if readErr != nil {
			t.Fatalf("read zip entry %s: %v", file.Name, readErr)
		}
		if closeErr != nil {
			t.Fatalf("close zip entry %s: %v", file.Name, closeErr)
		}
		entries[file.Name] = string(content)
	}
	return entries
}

func buildImportZip(t *testing.T, dataID, groupName, content, desc string) []byte {
	t.Helper()

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	writeZipEntry(t, writer, ".metadata.yml", "metadata:\n"+
		"- dataId: "+dataID+"\n"+
		"  group: "+groupName+"\n"+
		"  type: text\n"+
		"  appName: ''\n"+
		"  desc: "+desc+"\n")
	writeZipEntry(t, writer, groupName+"/"+dataID, content)
	if err := writer.Close(); err != nil {
		t.Fatalf("close import zip: %v", err)
	}
	return out.Bytes()
}

func buildBadMetadataZip(t *testing.T) []byte {
	t.Helper()

	var out bytes.Buffer
	writer := zip.NewWriter(&out)
	writeZipEntry(t, writer, ".metadata.yml", "metadata: []\n")
	if err := writer.Close(); err != nil {
		t.Fatalf("close bad metadata zip: %v", err)
	}
	return out.Bytes()
}

func writeZipEntry(t *testing.T, writer *zip.Writer, name, content string) {
	t.Helper()

	entry, err := writer.Create(name)
	if err != nil {
		t.Fatalf("create zip entry %s: %v", name, err)
	}
	if _, err := entry.Write([]byte(content)); err != nil {
		t.Fatalf("write zip entry %s: %v", name, err)
	}
}
