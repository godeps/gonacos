package app

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

// TestGRPCConfigBatchListen verifies the gRPC ConfigBatchListenRequest
// handler reports changed configs when the client md5 differs from the
// server's, and reports an empty list once the client md5 matches.
func TestGRPCConfigBatchListen(t *testing.T) {
	t.Parallel()

	// Build one service bundle shared by the HTTP handler and the gRPC
	// server. NewHandlerWithServices wires the bridge to the bundle's
	// config service, and SetupGRPCServerWithPush uses the same bundle's adapters.
	services := NewServiceBundle()
	handler := NewHandlerWithServices("../..", services)
	srv := SetupGRPCServerWithPush(services, nil)
	addr := "127.0.0.1:0"
	go func() { _ = srv.ListenAndServe(addr) }()
	for i := 0; i < 50 && srv.Addr() == nil; i++ {
		time.Sleep(2 * time.Millisecond)
	}
	if srv.Addr() == nil {
		t.Fatalf("grpc server did not start")
	}
	defer func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}()

	dataID := "grpc-batch-listen.json"
	group := "GRPC_LISTEN_GROUP"
	content := "grpc-listen-content"

	// Publish a config via the HTTP admin route.
	rec := publishConfigForGRPC(t, handler, dataID, group, content)
	if rec.Code != http.StatusOK {
		t.Fatalf("publish status = %d", rec.Code)
	}

	// First batch listen with empty md5 -> server should report the config
	// as changed.
	changed := sendBatchListen(t, srv, group, dataID, "public", "")
	if len(changed) != 1 {
		t.Fatalf("changedConfigs = %+v, want 1 entry", changed)
	}
	c := changed[0]
	if c["group"] != group || c["dataId"] != dataID || c["tenant"] != "public" {
		t.Fatalf("changed entry = %+v, want group=%s dataId=%s tenant=public", c, group, dataID)
	}

	// Look up the current md5 so we can send a matching listen.
	item, md5 := getMD5ForGRPC(t, handler, dataID, group)
	if md5 == "" {
		t.Fatalf("md5 empty; item=%+v", item)
	}

	// Second batch listen with matching md5 -> no changes.
	changed = sendBatchListen(t, srv, group, dataID, "public", md5)
	if len(changed) != 0 {
		t.Fatalf("changedConfigs = %+v, want empty when md5 matches", changed)
	}

	// Update the config content; the next listen should report a change
	// because the server md5 no longer matches the client's.
	publishConfigForGRPC(t, handler, dataID, group, content+"-v2")
	changed = sendBatchListen(t, srv, group, dataID, "public", md5)
	if len(changed) != 1 {
		t.Fatalf("changedConfigs after update = %+v, want 1 entry", changed)
	}
}

func publishConfigForGRPC(t *testing.T, handler http.Handler, dataID, group, content string) *httptest.ResponseRecorder {
	t.Helper()
	form := "dataId=" + dataID + "&groupName=" + group + "&content=" + content
	req := httptest.NewRequest(http.MethodPost, "/v3/admin/cs/config", strings.NewReader(form))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	return rec
}

func getMD5ForGRPC(t *testing.T, handler http.Handler, dataID, group string) (map[string]any, string) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "/v3/client/cs/config?dataId="+dataID+"&groupName="+group, nil)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("client query status = %d", rec.Code)
	}
	var resp struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatalf("decode client query: %v", err)
	}
	md5, _ := resp.Data["md5"].(string)
	return resp.Data, md5
}

func sendBatchListen(t *testing.T, srv *grpcsrv.Server, group, dataID, tenant, md5 string) []map[string]string {
	t.Helper()
	reqBody := map[string]any{
		"listen": true,
		"configListenContexts": []map[string]string{
			{"group": group, "dataId": dataID, "tenant": tenant, "md5": md5},
		},
	}
	bodyJSON, err := json.Marshal(reqBody)
	if err != nil {
		t.Fatalf("marshal listen request: %v", err)
	}
	payload := grpcsrv.Payload{
		Metadata: grpcsrv.Metadata{Type: "ConfigBatchListenRequest", Headers: map[string]string{}},
		Body: grpcsrv.Any{
			TypeURL: "type.googleapis.com/com.alibaba.nacos.api.config.request.ConfigBatchListenRequest",
			Value:   bodyJSON,
		},
	}
	frame := encodeGRPCFrame(payload)

	resp, err := http.Post(
		"http://"+srv.Addr().String()+"/Request/request",
		"application/grpc",
		bytes.NewReader(frame),
	)
	if err != nil {
		t.Fatalf("grpc post: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("grpc status = %d", resp.StatusCode)
	}
	if resp.Header.Get("grpc-status") != "0" {
		t.Fatalf("grpc-status = %v, want 0", resp.Header.Get("grpc-status"))
	}

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read grpc body: %v", err)
	}
	respPayload, err := decodeGRPCFrame(respBytes)
	if err != nil {
		t.Fatalf("decode grpc frame: %v", err)
	}
	var data struct {
		ChangedConfigs []map[string]string `json:"changedConfigs"`
	}
	if err := json.Unmarshal(respPayload.Body.Value, &data); err != nil {
		t.Fatalf("unmarshal response: %v (body=%s)", err, string(respPayload.Body.Value))
	}
	return data.ChangedConfigs
}

func encodeGRPCFrame(p grpcsrv.Payload) []byte {
	encoded := p.Encode()
	frame := grpcsrv.Frame{Payload: encoded}
	var buf bytes.Buffer
	_ = grpcsrv.WriteFrame(&buf, frame)
	return buf.Bytes()
}

func decodeGRPCFrame(b []byte) (grpcsrv.Payload, error) {
	f, err := grpcsrv.ReadFrame(bytes.NewReader(b))
	if err != nil {
		return grpcsrv.Payload{}, err
	}
	return grpcsrv.DecodePayload(f.Payload)
}
