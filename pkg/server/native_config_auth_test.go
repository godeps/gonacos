package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	grpcsrv "github.com/godeps/gonacos/pkg/protocol/grpc"
)

func TestNativeConfigAuthorizerScopesAllRequestedConfigs(t *testing.T) {
	authorize := func(secret, tenant, group, dataID string) error {
		if secret != "sdk-a" || (tenant != "" && (tenant != "tenant-a" || group != "GOLAB_FLAGS" || dataID != "golab.manifest")) {
			return errors.New("denied")
		}
		return nil
	}
	guard := nativeConfigGRPCAuthorizer(authorize)
	tests := []struct {
		name     string
		typeName string
		body     string
		secret   string
		wantCode int
	}{
		{"own query", "ConfigQueryRequest", `{"tenant":"tenant-a","group":"GOLAB_FLAGS","dataId":"golab.manifest"}`, "sdk-a", 0},
		{"cross tenant query", "ConfigQueryRequest", `{"tenant":"tenant-b","group":"GOLAB_FLAGS","dataId":"golab.manifest"}`, "sdk-a", grpcsrv.StatusPermissionDenied},
		{"missing token", "ConfigQueryRequest", `{"tenant":"tenant-a","group":"GOLAB_FLAGS","dataId":"golab.manifest"}`, "", grpcsrv.StatusUnauthenticated},
		{"mixed tenant subscription", "ConfigBatchListenRequest", `{"configListenContexts":[{"tenant":"tenant-a","group":"GOLAB_FLAGS","dataId":"golab.manifest"},{"tenant":"tenant-b","group":"GOLAB_FLAGS","dataId":"golab.manifest"}]}`, "sdk-a", grpcsrv.StatusPermissionDenied},
		{"own subscription", "ConfigBatchListenRequest", `{"configListenContexts":[{"tenant":"tenant-a","group":"GOLAB_FLAGS","dataId":"golab.manifest"}]}`, "sdk-a", 0},
		{"write denied", "ConfigPublishRequest", `{"tenant":"tenant-a","group":"GOLAB_FLAGS","dataId":"golab.manifest"}`, "sdk-a", grpcsrv.StatusPermissionDenied},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := guard(t.Context(), "/Request/request", grpcsrv.Payload{
				Metadata: grpcsrv.Metadata{Type: tt.typeName, Headers: map[string]string{"accessToken": tt.secret}},
				Body:     grpcsrv.Any{Value: []byte(tt.body)},
			})
			if tt.wantCode == 0 && err != nil {
				t.Fatal(err)
			}
			if tt.wantCode != 0 {
				status, ok := errors.AsType[*grpcsrv.StatusError](err)
				if !ok || status.Code != tt.wantCode {
					t.Fatalf("error = %v, want code %d", err, tt.wantCode)
				}
			}
		})
	}
	if err := guard(context.Background(), "/BiRequestStream/requestBiStream", grpcsrv.Payload{Metadata: grpcsrv.Metadata{Type: "ConnectionSetupRequest"}}); err != nil {
		t.Fatal(err)
	}
}

func TestNativeConfigHTTPExposesOnlySDKLogin(t *testing.T) {
	h := nativeConfigHTTPHandler(func(secret, _, _, _ string) error {
		if secret == "sdk-a" {
			return nil
		}
		return errors.New("denied")
	})
	for _, path := range []string{"/v3/cs/config", "/nacos/v1/cs/configs", "/v3/auth/user/login"} {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("%s: status %d, want 404", path, rec.Code)
		}
	}
	for _, tt := range []struct {
		name     string
		password string
		wantCode int
	}{
		{"valid", "sdk-a", http.StatusOK},
		{"invalid", "wrong", http.StatusUnauthorized},
	} {
		t.Run(tt.name, func(t *testing.T) {
			form := url.Values{"username": {"golab-sdk"}, "password": {tt.password}}
			req := httptest.NewRequest(http.MethodPost, "/nacos/v1/auth/users/login", strings.NewReader(form.Encode()))
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
			rec := httptest.NewRecorder()
			h.ServeHTTP(rec, req)
			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d", rec.Code, tt.wantCode)
			}
			if tt.wantCode == http.StatusOK && !strings.Contains(rec.Body.String(), `"accessToken":"sdk-a"`) {
				t.Fatalf("login response = %s", rec.Body.String())
			}
		})
	}
}
