package ai

import (
	"errors"
	"strings"
	"testing"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	return NewService(nil)
}

func TestPromptFullLifecycle(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p1", "my-prompt", "Hello {{name}}", "alice", []string{"v1"}, nil, "greeting", nil); err != nil {
		t.Fatalf("create draft: %v", err)
	}
	r, err := s.GetPrompt("p1")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if r.State != StateDraft || r.Draft == nil {
		t.Fatalf("state = %v, draft = %v", r.State, r.Draft)
	}

	if _, err := s.SubmitPrompt("p1"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := s.PublishPrompt("p1", false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	r, _ = s.GetPrompt("p1")
	if r.State != StatePublished || r.Draft != nil || len(r.Versions) != 1 || r.CurrentVersion != "v1" {
		t.Fatalf("after publish: state=%v draft=%v versions=%d current=%s", r.State, r.Draft, len(r.Versions), r.CurrentVersion)
	}

	if _, err := s.OnlinePrompt("p1"); err != nil {
		t.Fatalf("online: %v", err)
	}
	r, _ = s.GetPrompt("p1")
	if r.State != StateOnline {
		t.Fatalf("state = %v, want ONLINE", r.State)
	}

	clientPrompt, err := s.QueryClientPrompt("p1")
	if err != nil {
		t.Fatalf("client query: %v", err)
	}
	if clientPrompt.CurrentVersion != "v1" {
		t.Fatalf("client version = %v", clientPrompt.CurrentVersion)
	}

	if _, err := s.OfflinePrompt("p1"); err != nil {
		t.Fatalf("offline: %v", err)
	}
	if _, err := s.QueryClientPrompt("p1"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("client query after offline: %v", err)
	}
}

func TestPromptPublishWithoutSubmitRequiresForce(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p2", "n", "content", "a", nil, nil, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.PublishPrompt("p2", false); !errors.Is(err, ErrInvalidState) {
		t.Fatalf("publish without submit: %v", err)
	}
	if _, err := s.PublishPrompt("p2", true); err != nil {
		t.Fatalf("force publish: %v", err)
	}
}

func TestPromptRedraftAndVersioning(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p3", "n", "v1 content", "a", nil, nil, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.SubmitPrompt("p3"); err != nil {
		t.Fatalf("submit: %v", err)
	}
	if _, err := s.PublishPrompt("p3", false); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if _, err := s.RedraftPrompt("p3", "v2 content", "a"); err != nil {
		t.Fatalf("redraft: %v", err)
	}
	if _, err := s.PublishPrompt("p3", true); err != nil {
		t.Fatalf("publish 2: %v", err)
	}
	r, _ := s.GetPrompt("p3")
	if len(r.Versions) != 2 || r.CurrentVersion != "v2" {
		t.Fatalf("versions = %d, current = %s", len(r.Versions), r.CurrentVersion)
	}
	versions, err := s.ListPromptVersions("p3")
	if err != nil || len(versions) != 2 {
		t.Fatalf("list versions: %v %d", err, len(versions))
	}
	v, err := s.GetPromptVersion("p3", "v1")
	if err != nil || v.Content != "v1 content" {
		t.Fatalf("get v1: %v %v", err, v)
	}
	content, err := s.DownloadPromptVersion("p3", "v2")
	if err != nil || content != "v2 content" {
		t.Fatalf("download v2: %v %q", err, content)
	}
}

func TestPromptDraftCannotBeReopened(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p4", "n", "content", "a", nil, nil, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.CreatePromptDraft("p4", "n", "content", "a", nil, nil, "", nil); !errors.Is(err, ErrDraftExists) {
		t.Fatalf("recreate draft: %v", err)
	}
}

func TestPromptLabelsAndBizTags(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p5", "n", "content", "a", nil, nil, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := s.UpdatePromptLabels("p5", []string{"prod", "stable"}); err != nil {
		t.Fatalf("update labels: %v", err)
	}
	if _, err := s.BindPromptLabel("p5", "latest"); err != nil {
		t.Fatalf("bind: %v", err)
	}
	r, _ := s.GetPrompt("p5")
	if len(r.Labels) != 3 {
		t.Fatalf("labels = %v", r.Labels)
	}
	if _, err := s.UnbindPromptLabel("p5", "stable"); err != nil {
		t.Fatalf("unbind: %v", err)
	}
	r, _ = s.GetPrompt("p5")
	if len(r.Labels) != 2 {
		t.Fatalf("labels after unbind = %v", r.Labels)
	}
	if _, err := s.UpdatePromptBizTags("p5", []string{"team1"}); err != nil {
		t.Fatalf("biz tags: %v", err)
	}
	r, _ = s.GetPrompt("p5")
	if len(r.BizTags) != 1 || r.BizTags[0] != "team1" {
		t.Fatalf("biz tags = %v", r.BizTags)
	}
}

func TestPromptDelete(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.CreatePromptDraft("p6", "n", "content", "a", nil, nil, "", nil); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := s.DeletePrompt("p6"); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if err := s.DeletePrompt("p6"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("re-delete: %v", err)
	}
}

func TestSkillUploadBypassesDraft(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	r, err := s.UploadSkill("s1", "my-skill", "skill content", "alice", nil, nil, "a skill", nil)
	if err != nil {
		t.Fatalf("upload: %v", err)
	}
	if r.State != StatePublished || len(r.Versions) != 1 {
		t.Fatalf("after upload: state=%v versions=%d", r.State, len(r.Versions))
	}
}

func TestSkillBatchUpload(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	result, err := s.BatchUploadSkills([]UploadItem{
		{ID: "s2", Name: "skill2", Content: "c2"},
		{ID: "s3", Name: "skill3", Content: "c3"},
	})
	if err != nil {
		t.Fatalf("batch: %v", err)
	}
	if len(result.Uploaded) != 2 || len(result.Failed) != 0 {
		t.Fatalf("result: %+v", result)
	}
}

func TestAgentSpecUploadAndClientQuery(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.UploadAgentSpec("a1", "spec1", "spec content", "alice", nil, nil, "", nil); err != nil {
		t.Fatalf("upload: %v", err)
	}
	if _, err := s.OnlineAgentSpec("a1"); err != nil {
		t.Fatalf("online: %v", err)
	}
	results := s.QueryClientAgentSpecs()
	if len(results) != 1 || results[0].ID != "a1" {
		t.Fatalf("query: %+v", results)
	}
	search := s.SearchClientAgentSpecs("spec1")
	if len(search) != 1 {
		t.Fatalf("search: %+v", search)
	}
	search = s.SearchClientAgentSpecs("nope")
	if len(search) != 0 {
		t.Fatalf("search nope: %+v", search)
	}
}

func TestMcpServerCRUD(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	srv, err := s.CreateMcpServer(McpServer{
		ID: "m1", Name: "weather", Protocol: "http", Endpoint: "http://weather.example",
		Tools: []McpTool{{Name: "get_weather"}},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if srv.CreatedAt.IsZero() {
		t.Fatalf("createdAt not set")
	}

	srv, err = s.UpdateMcpServer(McpServer{ID: "m1", Name: "weather-v2"})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if srv.Name != "weather-v2" {
		t.Fatalf("name = %s", srv.Name)
	}

	got, err := s.GetMcpServer("m1")
	if err != nil || got.ID != "m1" {
		t.Fatalf("get: %v %v", err, got)
	}
	if len(s.ListMcpServers()) != 1 {
		t.Fatalf("list count = %d", len(s.ListMcpServers()))
	}
	tools, err := s.ImportToolsFromMcp("m1")
	if err != nil || len(tools) != 1 || tools[0].Name != "get_weather" {
		t.Fatalf("import tools: %v %v", err, tools)
	}
	if err := s.DeleteMcpServer("m1"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestA2AAgentVersions(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	agent, err := s.RegisterA2AAgent(A2AAgent{ID: "a1", Name: "agent", Endpoint: "http://a"})
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if agent.Version != "v1" || len(agent.Versions) != 1 {
		t.Fatalf("initial: %+v", agent)
	}
	if _, err := s.UpdateA2AAgent(A2AAgent{ID: "a1", Version: "v2"}); err != nil {
		t.Fatalf("update: %v", err)
	}
	versions, err := s.ListA2AAgentVersions("a1")
	if err != nil || len(versions) != 2 {
		t.Fatalf("versions: %v %d", err, len(versions))
	}
	if len(s.ListA2AAgents()) != 1 {
		t.Fatalf("list: %d", len(s.ListA2AAgents()))
	}
}

func TestImportSources(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	sources := s.ListImportSources()
	if len(sources) == 0 || sources[0].ID != "builtin" {
		t.Fatalf("sources: %+v", sources)
	}
	candidates, err := s.SearchImportCandidates("builtin", "query")
	if err != nil || candidates != nil {
		t.Fatalf("search: %v %v", err, candidates)
	}
	if err := s.ValidateImport("builtin", "item1"); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if _, err := s.SearchImportCandidates("unknown", ""); !errors.Is(err, ErrImportSourceUnknown) {
		t.Fatalf("unknown source: %v", err)
	}
}

func TestPipelines(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	pipelines := s.ListPipelines()
	if len(pipelines) == 0 {
		t.Fatalf("no pipelines")
	}
	p, err := s.GetPipeline("default")
	if err != nil || p.ID != "default" {
		t.Fatalf("get: %v %v", err, p)
	}
	if _, err := s.GetPipeline("missing"); !errors.Is(err, ErrResourceNotFound) {
		t.Fatalf("missing: %v", err)
	}
}

func TestCopilotDisabledByDefault(t *testing.T) {
	t.Parallel()
	s := newTestService(t)

	if _, err := s.OptimizePromptStream("prompt"); !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("optimize: %v", err)
	}
	if _, err := s.DebugPromptStream("prompt"); !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("debug: %v", err)
	}
	if _, err := s.GenerateSkillStream("desc"); !errors.Is(err, ErrLLMDisabled) {
		t.Fatalf("generate: %v", err)
	}
}

// stubLLM is a test LLM client that returns predictable output.
type stubLLM struct{}

func (stubLLM) OptimizePrompt(p string) (string, error) { return "optimized:" + p, nil }
func (stubLLM) DebugPrompt(p string) (string, error)    { return "debug:" + p, nil }
func (stubLLM) GenerateSkill(d string) (string, error)  { return "skill:" + d, nil }
func (stubLLM) OptimizeSkill(s string) (string, error)  { return "opt:" + s, nil }

func TestCopilotWithStubLLM(t *testing.T) {
	t.Parallel()
	s := NewService(stubLLM{})

	out, err := s.OptimizePromptStream("hello")
	if err != nil || out != "optimized:hello" {
		t.Fatalf("optimize: %v %q", err, out)
	}
	out, err = s.GenerateSkillStream("a skill")
	if err != nil || !strings.HasPrefix(out, "skill:") {
		t.Fatalf("generate: %v %q", err, out)
	}
}
