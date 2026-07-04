package contract

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Manifest struct {
	GeneratedBy string            `json:"generatedBy"`
	OpenAPI     []OpenAPISurface  `json:"openapi"`
	GRPC        []GRPCService     `json:"grpc"`
	Summary     ManifestSummary   `json:"summary"`
	Sources     map[string]string `json:"sources"`
}

type ManifestSummary struct {
	OpenAPISurfaces   int `json:"openapiSurfaces"`
	OpenAPIOperations int `json:"openapiOperations"`
	GRPCServices      int `json:"grpcServices"`
	GRPCRPCs          int `json:"grpcRpcs"`
}

type OpenAPISurface struct {
	Name       string              `json:"name"`
	File       string              `json:"file"`
	Title      string              `json:"title"`
	Version    string              `json:"version"`
	OpenAPI    string              `json:"openapi"`
	SHA256     string              `json:"sha256"`
	Operations []OpenAPIOperation  `json:"operations"`
	Tags       []OpenAPITagSummary `json:"tags,omitempty"`
}

type OpenAPIOperation struct {
	Method      string   `json:"method"`
	Path        string   `json:"path"`
	OperationID string   `json:"operationId,omitempty"`
	Tags        []string `json:"tags,omitempty"`
	Summary     string   `json:"summary,omitempty"`
}

type OpenAPITagSummary struct {
	Name           string `json:"name"`
	OperationCount int    `json:"operationCount"`
}

type GRPCService struct {
	Name string    `json:"name"`
	RPCs []GRPCRPC `json:"rpcs"`
}

type GRPCRPC struct {
	Name         string `json:"name"`
	ClientStream bool   `json:"clientStream"`
	ServerStream bool   `json:"serverStream"`
	RequestType  string `json:"requestType"`
	ResponseType string `json:"responseType"`
}

type openAPIDocument struct {
	OpenAPI string `json:"openapi"`
	Info    struct {
		Title   string `json:"title"`
		Version string `json:"version"`
	} `json:"info"`
	Paths map[string]map[string]struct {
		OperationID string   `json:"operationId"`
		Tags        []string `json:"tags"`
		Summary     string   `json:"summary"`
	} `json:"paths"`
}

func Build(root string) (*Manifest, error) {
	openapiDir := filepath.Join(root, "api", "openapi", "upstream")
	protoPath := filepath.Join(root, "api", "proto", "nacos_grpc_service.proto")

	surfaces, err := loadOpenAPISurfaces(openapiDir)
	if err != nil {
		return nil, err
	}

	grpc, protoHash, err := loadGRPCServices(protoPath)
	if err != nil {
		return nil, err
	}

	manifest := &Manifest{
		GeneratedBy: "gonacos-contract",
		OpenAPI:     surfaces,
		GRPC:        grpc,
		Sources: map[string]string{
			"openapi":     "https://nacos.io/swagger/{client,admin,console}/zh/api.json",
			"grpc":        "other/nacos-sdk-go/api/proto/nacos_grpc_service.proto",
			"protoSHA256": protoHash,
		},
	}
	for _, surface := range surfaces {
		manifest.Summary.OpenAPISurfaces++
		manifest.Summary.OpenAPIOperations += len(surface.Operations)
	}
	for _, service := range grpc {
		manifest.Summary.GRPCServices++
		manifest.Summary.GRPCRPCs += len(service.RPCs)
	}

	return manifest, nil
}

func Write(manifest *Manifest, path string) error {
	data, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal manifest: %w", err)
	}
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write manifest %q: %w", path, err)
	}
	return nil
}

func Verify(root, manifestPath string) error {
	current, err := Build(root)
	if err != nil {
		return err
	}

	expectedData, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest %q: %w", manifestPath, err)
	}
	var expected Manifest
	if err := json.Unmarshal(expectedData, &expected); err != nil {
		return fmt.Errorf("parse manifest %q: %w", manifestPath, err)
	}

	currentData, err := json.Marshal(current)
	if err != nil {
		return fmt.Errorf("marshal current manifest: %w", err)
	}
	expectedCanonical, err := json.Marshal(&expected)
	if err != nil {
		return fmt.Errorf("marshal expected manifest: %w", err)
	}
	if string(currentData) != string(expectedCanonical) {
		return fmt.Errorf("contract manifest is stale; run `make contract-generate`")
	}
	return nil
}

func loadOpenAPISurfaces(dir string) ([]OpenAPISurface, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil, fmt.Errorf("list openapi files: %w", err)
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no openapi files found in %q", dir)
	}
	sort.Strings(files)

	surfaces := make([]OpenAPISurface, 0, len(files))
	for _, file := range files {
		surface, err := loadOpenAPISurface(file)
		if err != nil {
			return nil, err
		}
		surfaces = append(surfaces, surface)
	}
	return surfaces, nil
}

func loadOpenAPISurface(path string) (OpenAPISurface, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return OpenAPISurface{}, fmt.Errorf("read openapi %q: %w", path, err)
	}
	var doc openAPIDocument
	if err := json.Unmarshal(data, &doc); err != nil {
		return OpenAPISurface{}, fmt.Errorf("parse openapi %q: %w", path, err)
	}
	if doc.OpenAPI == "" || doc.Info.Version == "" || len(doc.Paths) == 0 {
		return OpenAPISurface{}, fmt.Errorf("invalid openapi document %q", path)
	}

	var operations []OpenAPIOperation
	tagCounts := map[string]int{}
	for route, methods := range doc.Paths {
		for method, operation := range methods {
			method = strings.ToUpper(method)
			switch method {
			case "GET", "POST", "PUT", "DELETE", "PATCH", "HEAD", "OPTIONS":
			default:
				continue
			}
			operations = append(operations, OpenAPIOperation{
				Method:      method,
				Path:        route,
				OperationID: operation.OperationID,
				Tags:        append([]string(nil), operation.Tags...),
				Summary:     operation.Summary,
			})
			for _, tag := range operation.Tags {
				tagCounts[tag]++
			}
		}
	}
	sort.Slice(operations, func(i, j int) bool {
		if operations[i].Path == operations[j].Path {
			return operations[i].Method < operations[j].Method
		}
		return operations[i].Path < operations[j].Path
	})

	tags := make([]OpenAPITagSummary, 0, len(tagCounts))
	for tag, count := range tagCounts {
		tags = append(tags, OpenAPITagSummary{Name: tag, OperationCount: count})
	}
	sort.Slice(tags, func(i, j int) bool {
		return tags[i].Name < tags[j].Name
	})

	return OpenAPISurface{
		Name:       surfaceName(path),
		File:       filepath.ToSlash(path),
		Title:      doc.Info.Title,
		Version:    doc.Info.Version,
		OpenAPI:    doc.OpenAPI,
		SHA256:     sha256Hex(data),
		Operations: operations,
		Tags:       tags,
	}, nil
}

func loadGRPCServices(path string) ([]GRPCService, string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, "", fmt.Errorf("read proto %q: %w", path, err)
	}
	services, err := parseProtoServices(string(data))
	if err != nil {
		return nil, "", fmt.Errorf("parse proto %q: %w", path, err)
	}
	return services, sha256Hex(data), nil
}

var (
	serviceRe = regexp.MustCompile(`(?s)service\s+([A-Za-z_][A-Za-z0-9_]*)\s*\{(.*?)\}`)
	rpcRe     = regexp.MustCompile(`rpc\s+([A-Za-z_][A-Za-z0-9_]*)\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\)\s*returns\s*\(\s*(stream\s+)?([A-Za-z_][A-Za-z0-9_]*)\s*\)`)
)

func parseProtoServices(src string) ([]GRPCService, error) {
	matches := serviceRe.FindAllStringSubmatch(src, -1)
	if len(matches) == 0 {
		return nil, fmt.Errorf("no grpc services found")
	}

	services := make([]GRPCService, 0, len(matches))
	for _, match := range matches {
		service := GRPCService{Name: match[1]}
		for _, rpc := range rpcRe.FindAllStringSubmatch(match[2], -1) {
			service.RPCs = append(service.RPCs, GRPCRPC{
				Name:         rpc[1],
				ClientStream: strings.TrimSpace(rpc[2]) == "stream",
				RequestType:  rpc[3],
				ServerStream: strings.TrimSpace(rpc[4]) == "stream",
				ResponseType: rpc[5],
			})
		}
		if len(service.RPCs) == 0 {
			return nil, fmt.Errorf("service %q has no rpc methods", service.Name)
		}
		services = append(services, service)
	}
	sort.Slice(services, func(i, j int) bool {
		return services[i].Name < services[j].Name
	})
	return services, nil
}

func surfaceName(path string) string {
	base := filepath.Base(path)
	base = strings.TrimSuffix(base, filepath.Ext(base))
	return strings.TrimSuffix(base, ".zh")
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}
