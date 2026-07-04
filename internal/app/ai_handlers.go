package app

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/godeps/gonacos/internal/ai"
	"github.com/godeps/gonacos/internal/protocol"
)

type aiHandler struct {
	service *ai.Service
	mode    string
}

func registerAIRoutes(register func(string, string, http.HandlerFunc), service *ai.Service) {
	admin := aiHandler{service: service, mode: "admin"}
	console := aiHandler{service: service, mode: "console"}
	client := aiHandler{service: service, mode: "client"}

	for _, h := range []aiHandler{admin, console} {
		registerPromptRoutes(register, h)
		registerSkillRoutes(register, h)
		registerAgentSpecRoutes(register, h)
		registerA2ARoutes(register, h)
		registerImportRoutes(register, h)
		registerPipelineRoutes(register, h)
	}
	registerMcpRoutes(register, admin, console)
	registerCopilotRoutes(register, console)
	registerClientAIRoutes(register, client)
}

func registerPromptRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/prompt", "/v3/console/ai/prompt"} {
		register(http.MethodPost, base, h.promptPublishLegacy)
		register(http.MethodDelete, base, h.promptDelete)
		register(http.MethodPost, base+"/draft", h.promptCreateDraft)
		register(http.MethodPut, base+"/draft", h.promptUpdateDraft)
		register(http.MethodDelete, base+"/draft", h.promptDeleteDraft)
		register(http.MethodPost, base+"/submit", h.promptSubmit)
		register(http.MethodPost, base+"/publish", h.promptPublish)
		register(http.MethodPost, base+"/force-publish", h.promptForcePublish)
		register(http.MethodPost, base+"/redraft", h.promptRedraft)
		register(http.MethodPost, base+"/online", h.promptOnline)
		register(http.MethodPost, base+"/offline", h.promptOffline)
		register(http.MethodGet, base+"/list", h.promptList)
		register(http.MethodGet, base+"/detail", h.promptDetail)
		register(http.MethodGet, base+"/version", h.promptVersionDetail)
		register(http.MethodGet, base+"/versions", h.promptVersions)
		register(http.MethodGet, base+"/version/download", h.promptVersionDownload)
		register(http.MethodPut, base+"/labels", h.promptUpdateLabels)
		register(http.MethodPut, base+"/label", h.promptBindLabel)
		register(http.MethodDelete, base+"/label", h.promptUnbindLabel)
		register(http.MethodPut, base+"/biz-tags", h.promptUpdateBizTags)
		register(http.MethodPut, base+"/description", h.promptUpdateDescription)
		register(http.MethodGet, base+"/metadata", h.promptGetMetadata)
		register(http.MethodPut, base+"/metadata", h.promptUpdateMetadata)
		register(http.MethodGet, base+"/governance", h.promptGovernance)
	}
}

func registerSkillRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/skills", "/v3/console/ai/skills"} {
		register(http.MethodGet, base, h.skillDetail)
		register(http.MethodDelete, base, h.skillDelete)
		register(http.MethodPost, base+"/draft", h.skillCreateDraft)
		register(http.MethodPut, base+"/draft", h.skillUpdateDraft)
		register(http.MethodDelete, base+"/draft", h.skillDeleteDraft)
		register(http.MethodPost, base+"/submit", h.skillSubmit)
		register(http.MethodPost, base+"/publish", h.skillPublish)
		register(http.MethodPost, base+"/force-publish", h.skillForcePublish)
		register(http.MethodPost, base+"/redraft", h.skillRedraft)
		register(http.MethodPost, base+"/online", h.skillOnline)
		register(http.MethodPost, base+"/offline", h.skillOffline)
		register(http.MethodGet, base+"/list", h.skillList)
		register(http.MethodPost, base+"/upload", h.skillUpload)
		register(http.MethodPost, base+"/upload/batch", h.skillBatchUpload)
		register(http.MethodGet, base+"/version", h.skillVersionDetail)
		register(http.MethodGet, base+"/version/download", h.skillVersionDownload)
		register(http.MethodPut, base+"/labels", h.skillUpdateLabels)
		register(http.MethodPut, base+"/biz-tags", h.skillUpdateBizTags)
		register(http.MethodPut, base+"/scope", h.skillUpdateScope)
	}
}

func registerAgentSpecRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/agentspecs", "/v3/console/ai/agentspecs"} {
		register(http.MethodGet, base, h.agentSpecDetail)
		register(http.MethodDelete, base, h.agentSpecDelete)
		register(http.MethodPost, base+"/draft", h.agentSpecCreateDraft)
		register(http.MethodPut, base+"/draft", h.agentSpecUpdateDraft)
		register(http.MethodDelete, base+"/draft", h.agentSpecDeleteDraft)
		register(http.MethodPost, base+"/submit", h.agentSpecSubmit)
		register(http.MethodPost, base+"/publish", h.agentSpecPublish)
		register(http.MethodPost, base+"/force-publish", h.agentSpecForcePublish)
		register(http.MethodPost, base+"/redraft", h.agentSpecRedraft)
		register(http.MethodPost, base+"/online", h.agentSpecOnline)
		register(http.MethodPost, base+"/offline", h.agentSpecOffline)
		register(http.MethodGet, base+"/list", h.agentSpecList)
		register(http.MethodPost, base+"/upload", h.agentSpecUpload)
		register(http.MethodGet, base+"/version", h.agentSpecVersionDetail)
		register(http.MethodPut, base+"/labels", h.agentSpecUpdateLabels)
		register(http.MethodPut, base+"/biz-tags", h.agentSpecUpdateBizTags)
		register(http.MethodPut, base+"/scope", h.agentSpecUpdateScope)
	}
}

func registerA2ARoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/a2a", "/v3/console/ai/a2a"} {
		register(http.MethodGet, base, h.a2aGet)
		register(http.MethodPost, base, h.a2aRegister)
		register(http.MethodPut, base, h.a2aUpdate)
		register(http.MethodDelete, base, h.a2aDelete)
	}
	register(http.MethodGet, "/v3/admin/ai/a2a/list", h.a2aList)
	register(http.MethodGet, "/v3/console/ai/a2a/list", h.a2aList)
	register(http.MethodGet, "/v3/admin/ai/a2a/version/list", h.a2aVersions)
	register(http.MethodGet, "/v3/console/ai/a2a/version/list", h.a2aVersions)
}

func registerMcpRoutes(register func(string, string, http.HandlerFunc), admin, console aiHandler) {
	for _, base := range []string{"/v3/admin/ai/mcp", "/v3/console/ai/mcp"} {
		register(http.MethodGet, base, admin.mcpGet)
		register(http.MethodPost, base, admin.mcpCreate)
		register(http.MethodPut, base, admin.mcpUpdate)
		register(http.MethodDelete, base, admin.mcpDelete)
	}
	register(http.MethodGet, "/v3/admin/ai/mcp/list", admin.mcpList)
	register(http.MethodGet, "/v3/console/ai/mcp/list", console.mcpList)
	register(http.MethodGet, "/v3/console/ai/mcp/importToolsFromMcp", console.mcpImportTools)
	register(http.MethodPost, "/v3/console/ai/mcp/import/validate", console.mcpValidateImport)
	register(http.MethodPost, "/v3/console/ai/mcp/import/execute", console.mcpExecuteImport)
}

func registerImportRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/import", "/v3/console/ai/import"} {
		register(http.MethodGet, base+"/sources", h.importSources)
		register(http.MethodPost, base+"/search", h.importSearch)
		register(http.MethodPost, base+"/validate", h.importValidate)
		register(http.MethodPost, base+"/execute", h.importExecute)
	}
}

func registerPipelineRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	for _, base := range []string{"/v3/admin/ai/pipelines", "/v3/console/ai/pipelines"} {
		register(http.MethodGet, base, h.pipelineListLegacy)
		register(http.MethodGet, base+"/list", h.pipelineList)
		register(http.MethodGet, base+"/detail", h.pipelineDetail)
		register(http.MethodGet, base+"/{pipelineId}", h.pipelineGet)
	}
}

func registerCopilotRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	register(http.MethodGet, "/v3/console/copilot/config", h.copilotConfigGet)
	register(http.MethodPost, "/v3/console/copilot/config", h.copilotConfigSave)
	register(http.MethodPost, "/v3/console/copilot/prompt/optimize", h.copilotOptimizePrompt)
	register(http.MethodPost, "/v3/console/copilot/prompt/debug", h.copilotDebugPrompt)
	register(http.MethodPost, "/v3/console/copilot/skill/generate", h.copilotGenerateSkill)
	register(http.MethodPost, "/v3/console/copilot/skill/optimize", h.copilotOptimizeSkill)
}

func registerClientAIRoutes(register func(string, string, http.HandlerFunc), h aiHandler) {
	register(http.MethodGet, "/v3/client/ai/prompt", h.clientPrompt)
	register(http.MethodGet, "/v3/client/ai/skills", h.clientSkill)
	register(http.MethodGet, "/v3/client/ai/agentspecs", h.clientAgentSpecs)
	register(http.MethodGet, "/v3/client/ai/agentspecs/search", h.clientAgentSpecsSearch)
}

// --- Prompt handlers ---

func (h aiHandler) promptCreateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.CreatePromptDraft(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptUpdateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdatePromptDraft(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptDeleteDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeletePromptDraft(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) promptSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.SubmitPrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptPublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishPrompt(formValue(r, "id"), false)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptForcePublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishPrompt(formValue(r, "id"), true)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptRedraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.RedraftPrompt(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptOnline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OnlinePrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptOffline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OfflinePrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeletePrompt(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) promptDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.GetPrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListPrompts())
}

func (h aiHandler) promptVersions(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	versions, err := h.service.ListPromptVersions(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, versions)
}

func (h aiHandler) promptVersionDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	v, err := h.service.GetPromptVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, v)
}

func (h aiHandler) promptVersionDownload(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	content, err := h.service.DownloadPromptVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, content)
}

func (h aiHandler) promptUpdateLabels(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, err := parseStringList(formValue(r, "labels"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdatePromptLabels(formValue(r, "id"), labels)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptBindLabel(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.BindPromptLabel(formValue(r, "id"), formValue(r, "label"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptUnbindLabel(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.UnbindPromptLabel(formValue(r, "id"), formValue(r, "label"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptUpdateBizTags(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tags, err := parseStringList(formValue(r, "bizTags"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdatePromptBizTags(formValue(r, "id"), tags)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptUpdateDescription(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.UpdatePromptDescription(formValue(r, "id"), formValue(r, "description"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptGetMetadata(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.GetPrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res.Metadata)
}

func (h aiHandler) promptUpdateMetadata(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	metadata, err := parseMetadataMap(formValue(r, "metadata"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdatePromptMetadata(formValue(r, "id"), metadata)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) promptGovernance(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"id":          formValue(r, "id"),
		"violations":  []any{},
		"suggestions": []any{},
	})
}

// promptPublishLegacy is the POST /v3/admin/ai/prompt endpoint. It creates a
// draft from the request and force-publishes it in one call, matching the
// Nacos convenience semantics.
func (h aiHandler) promptPublishLegacy(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	_, err = h.service.CreatePromptDraft(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil && !errors.Is(err, ai.ErrDraftExists) {
		writeAIError(w, err)
		return
	}
	res, err := h.service.PublishPrompt(formValue(r, "id"), true)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// --- Skill handlers ---

func (h aiHandler) skillCreateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.CreateSkillDraft(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillUpdateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateSkillDraft(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillDeleteDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteSkillDraft(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) skillSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.SubmitSkill(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillPublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishSkill(formValue(r, "id"), false)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillForcePublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishSkill(formValue(r, "id"), true)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillRedraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.RedraftSkill(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillOnline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OnlineSkill(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillOffline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OfflineSkill(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteSkill(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) skillDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.GetSkill(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListSkills())
}

func (h aiHandler) skillVersionDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	v, err := h.service.GetSkillVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, v)
}

func (h aiHandler) skillVersionDownload(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	content, err := h.service.DownloadSkillVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, content)
}

func (h aiHandler) skillUpload(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UploadSkill(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillBatchUpload(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	items, err := parseUploadItems(formValue(r, "items"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	result, err := h.service.BatchUploadSkills(items)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, result)
}

func (h aiHandler) skillUpdateLabels(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, err := parseStringList(formValue(r, "labels"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateSkillLabels(formValue(r, "id"), labels)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillUpdateBizTags(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tags, err := parseStringList(formValue(r, "bizTags"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateSkillBizTags(formValue(r, "id"), tags)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) skillUpdateScope(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.UpdateSkillScope(formValue(r, "id"), formValue(r, "scope"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// --- AgentSpec handlers ---

func (h aiHandler) agentSpecCreateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.CreateAgentSpecDraft(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecUpdateDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateAgentSpecDraft(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecDeleteDraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteAgentSpecDraft(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) agentSpecSubmit(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.SubmitAgentSpec(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecPublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishAgentSpec(formValue(r, "id"), false)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecForcePublish(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.PublishAgentSpec(formValue(r, "id"), true)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecRedraft(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.RedraftAgentSpec(
		formValue(r, "id"),
		formValue(r, "content"),
		formValue(r, "author"),
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecOnline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OnlineAgentSpec(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecOffline(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.OfflineAgentSpec(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteAgentSpec(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) agentSpecDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.GetAgentSpec(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListAgentSpecs())
}

func (h aiHandler) agentSpecVersionDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	v, err := h.service.GetAgentSpecVersion(formValue(r, "id"), formValue(r, "version"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, v)
}

func (h aiHandler) agentSpecUpload(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, bizTags, metadata, err := parseAIResourceAttrs(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UploadAgentSpec(
		formValue(r, "id"),
		formValue(r, "name"),
		formValue(r, "content"),
		formValue(r, "author"),
		labels, bizTags,
		formValue(r, "description"),
		metadata,
	)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecUpdateLabels(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	labels, err := parseStringList(formValue(r, "labels"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateAgentSpecLabels(formValue(r, "id"), labels)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecUpdateBizTags(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tags, err := parseStringList(formValue(r, "bizTags"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateAgentSpecBizTags(formValue(r, "id"), tags)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) agentSpecUpdateScope(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.UpdateAgentSpecScope(formValue(r, "id"), formValue(r, "scope"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// --- A2A handlers ---

func (h aiHandler) a2aGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	agent, err := h.service.GetA2AAgent(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, agent)
}

func (h aiHandler) a2aRegister(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	agent, err := buildA2AAgent(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.RegisterA2AAgent(agent)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) a2aUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	agent, err := buildA2AAgent(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateA2AAgent(agent)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) a2aDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteA2AAgent(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) a2aList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListA2AAgents())
}

func (h aiHandler) a2aVersions(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	versions, err := h.service.ListA2AAgentVersions(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, versions)
}

// --- MCP handlers ---

func (h aiHandler) mcpGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	srv, err := h.service.GetMcpServer(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, srv)
}

func (h aiHandler) mcpCreate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	srv, err := buildMcpServer(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.CreateMcpServer(srv)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) mcpUpdate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	srv, err := buildMcpServer(r)
	if err != nil {
		writeAIError(w, err)
		return
	}
	res, err := h.service.UpdateMcpServer(srv)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) mcpDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteMcpServer(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) mcpList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListMcpServers())
}

func (h aiHandler) mcpImportTools(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tools, err := h.service.ImportToolsFromMcp(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, tools)
}

func (h aiHandler) mcpValidateImport(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.ValidateMcpImport(formValue(r, "url")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) mcpExecuteImport(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tools, err := h.service.ExecuteMcpImport(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, tools)
}

// --- Import handlers ---

func (h aiHandler) importSources(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListImportSources())
}

func (h aiHandler) importSearch(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	candidates, err := h.service.SearchImportCandidates(formValue(r, "sourceId"), formValue(r, "query"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, candidates)
}

func (h aiHandler) importValidate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.ValidateImport(formValue(r, "sourceId"), formValue(r, "itemId")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) importExecute(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	candidate, err := h.service.ExecuteImport(formValue(r, "sourceId"), formValue(r, "itemId"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, candidate)
}

// --- Pipeline handlers ---

func (h aiHandler) pipelineListLegacy(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListPipelines())
}

func (h aiHandler) pipelineList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListPipelines())
}

func (h aiHandler) pipelineDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	p, err := h.service.GetPipelineDetail(formValue(r, "pipelineId"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, p)
}

func (h aiHandler) pipelineGet(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := r.PathValue("pipelineId")
	if id == "" {
		id = formValue(r, "pipelineId")
	}
	p, err := h.service.GetPipeline(id)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, p)
}

// --- Copilot handlers ---

func (h aiHandler) copilotConfigGet(w http.ResponseWriter, r *http.Request) {
	protocol.WriteResult(w, http.StatusOK, map[string]string{
		"provider": "disabled",
		"model":    "",
	})
}

func (h aiHandler) copilotConfigSave(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

func (h aiHandler) copilotOptimizePrompt(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	out, err := h.service.OptimizePromptStream(formValue(r, "prompt"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, out)
}

func (h aiHandler) copilotDebugPrompt(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	out, err := h.service.DebugPromptStream(formValue(r, "prompt"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, out)
}

func (h aiHandler) copilotGenerateSkill(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	out, err := h.service.GenerateSkillStream(formValue(r, "description"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, out)
}

func (h aiHandler) copilotOptimizeSkill(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	out, err := h.service.OptimizeSkillStream(formValue(r, "skill"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, out)
}

// --- Client query handlers ---

func (h aiHandler) clientPrompt(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.QueryClientPrompt(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) clientSkill(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	res, err := h.service.QueryClientSkill(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

func (h aiHandler) clientAgentSpecs(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.QueryClientAgentSpecs())
}

func (h aiHandler) clientAgentSpecsSearch(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.SearchClientAgentSpecs(formValue(r, "query")))
}

// --- Helpers ---

func parseAIResourceAttrs(r *http.Request) (labels, bizTags []string, metadata map[string]string, err error) {
	labels, err = parseStringList(formValue(r, "labels"))
	if err != nil {
		return nil, nil, nil, err
	}
	bizTags, err = parseStringList(formValue(r, "bizTags"))
	if err != nil {
		return nil, nil, nil, err
	}
	metadata, err = parseMetadataMap(formValue(r, "metadata"))
	if err != nil {
		return nil, nil, nil, err
	}
	return labels, bizTags, metadata, nil
}

func parseStringList(s string) ([]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "[") {
		var out []string
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out, nil
}

func parseMetadataMap(s string) (map[string]string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	out := map[string]string{}
	if strings.HasPrefix(s, "{") {
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			return nil, err
		}
		return out, nil
	}
	for _, pair := range strings.Split(s, ",") {
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, errors.New("metadata pair must be k=v")
		}
		out[strings.TrimSpace(kv[0])] = strings.TrimSpace(kv[1])
	}
	return out, nil
}

func parseUploadItems(s string) ([]ai.UploadItem, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ai.ErrMissingContent
	}
	var items []ai.UploadItem
	if err := json.Unmarshal([]byte(s), &items); err != nil {
		return nil, err
	}
	return items, nil
}

func buildA2AAgent(r *http.Request) (ai.A2AAgent, error) {
	capabilities, err := parseStringList(formValue(r, "capabilities"))
	if err != nil {
		return ai.A2AAgent{}, err
	}
	metadata, err := parseMetadataMap(formValue(r, "metadata"))
	if err != nil {
		return ai.A2AAgent{}, err
	}
	return ai.A2AAgent{
		ID:           formValue(r, "id"),
		Name:         formValue(r, "name"),
		Description:  formValue(r, "description"),
		Endpoint:     formValue(r, "endpoint"),
		Protocol:     formValue(r, "protocol"),
		Capabilities: capabilities,
		Metadata:     metadata,
		Version:      formValue(r, "version"),
	}, nil
}

func buildMcpServer(r *http.Request) (ai.McpServer, error) {
	labels, err := parseStringList(formValue(r, "labels"))
	if err != nil {
		return ai.McpServer{}, err
	}
	metadata, err := parseMetadataMap(formValue(r, "metadata"))
	if err != nil {
		return ai.McpServer{}, err
	}
	tools, err := parseMcpTools(formValue(r, "tools"))
	if err != nil {
		return ai.McpServer{}, err
	}
	return ai.McpServer{
		ID:          formValue(r, "id"),
		Name:        formValue(r, "name"),
		Description: formValue(r, "description"),
		Protocol:    formValue(r, "protocol"),
		Endpoint:    formValue(r, "endpoint"),
		Tools:       tools,
		Labels:      labels,
		Metadata:    metadata,
	}, nil
}

func parseMcpTools(s string) ([]ai.McpTool, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	var tools []ai.McpTool
	if err := json.Unmarshal([]byte(s), &tools); err != nil {
		return nil, err
	}
	return tools, nil
}

func writeAIError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := protocol.CodeParameterValidateError
	switch {
	case errors.Is(err, ai.ErrMissingID),
		errors.Is(err, ai.ErrMissingName),
		errors.Is(err, ai.ErrMissingVersion),
		errors.Is(err, ai.ErrMissingContent):
		code = protocol.CodeParameterMissing
	case errors.Is(err, ai.ErrResourceNotFound),
		errors.Is(err, ai.ErrVersionNotFound),
		errors.Is(err, ai.ErrDraftNotFound):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	case errors.Is(err, ai.ErrResourceExists),
		errors.Is(err, ai.ErrDraftExists),
		errors.Is(err, ai.ErrInvalidState):
		code = protocol.CodeConflict
	case errors.Is(err, ai.ErrLLMDisabled):
		status = http.StatusServiceUnavailable
		code = protocol.CodeNotImplemented
	case errors.Is(err, ai.ErrImportSourceUnknown):
		status = http.StatusNotFound
		code = protocol.CodeNotFound
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: err.Error(),
	})
}
