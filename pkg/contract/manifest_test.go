package contract

import (
	"os"
	"path/filepath"
	"testing"
)

func TestBuildReadsOpenAPIAndProtoContracts(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "api/openapi/upstream/client.zh.json", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Client API", "version": "3.2.2"},
	  "paths": {
	    "/v3/client/cs/config": {
	      "get": {"operationId": "getConfig", "tags": ["config"], "summary": "Get config"}
	    },
	    "/v3/client/ns/instance": {
	      "post": {"operationId": "registerInstance", "tags": ["naming"]}
	    }
	  }
	}`)
	writeFile(t, root, "api/proto/nacos_grpc_service.proto", `
syntax = "proto3";
message Payload {}
service Request {
  rpc request (Payload) returns (Payload) {}
}
service BiRequestStream {
  rpc requestBiStream (stream Payload) returns (stream Payload) {}
}`)

	manifest, err := Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	if manifest.Summary.OpenAPISurfaces != 1 {
		t.Fatalf("OpenAPISurfaces = %d, want 1", manifest.Summary.OpenAPISurfaces)
	}
	if manifest.Summary.OpenAPIOperations != 2 {
		t.Fatalf("OpenAPIOperations = %d, want 2", manifest.Summary.OpenAPIOperations)
	}
	if manifest.Summary.GRPCServices != 2 || manifest.Summary.GRPCRPCs != 2 {
		t.Fatalf("grpc summary = %+v, want 2 services and 2 rpcs", manifest.Summary)
	}
	if got := manifest.OpenAPI[0].Operations[0].Path; got != "/v3/client/cs/config" {
		t.Fatalf("first operation path = %q", got)
	}
}

func TestVerifyDetectsStaleManifest(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	writeFile(t, root, "api/openapi/upstream/client.zh.json", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Client API", "version": "3.2.2"},
	  "paths": {"/v3/client/cs/config": {"get": {"operationId": "getConfig"}}}
	}`)
	writeFile(t, root, "api/proto/nacos_grpc_service.proto", `
syntax = "proto3";
message Payload {}
service Request {
  rpc request (Payload) returns (Payload) {}
}`)

	manifest, err := Build(root)
	if err != nil {
		t.Fatalf("Build() error = %v", err)
	}
	path := filepath.Join(root, "api/openapi/manifest.json")
	if err := Write(manifest, path); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := Verify(root, path); err != nil {
		t.Fatalf("Verify() fresh manifest error = %v", err)
	}

	writeFile(t, root, "api/openapi/upstream/admin.zh.json", `{
	  "openapi": "3.1.0",
	  "info": {"title": "Admin API", "version": "3.2.2"},
	  "paths": {"/v3/admin/core/state": {"get": {"operationId": "state"}}}
	}`)
	if err := Verify(root, path); err == nil {
		t.Fatalf("Verify() stale manifest error = nil, want error")
	}
}

func writeFile(t *testing.T, root, name, content string) {
	t.Helper()

	path := filepath.Join(root, filepath.FromSlash(name))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
}
