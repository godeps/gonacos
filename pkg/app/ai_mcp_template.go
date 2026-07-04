package app

import (
	"net/http"
	"strings"

	"github.com/godeps/gonacos/pkg/ai"
	"github.com/godeps/gonacos/pkg/ai/mcptemplate"
	"github.com/godeps/gonacos/pkg/protocol"
)

// registerTemplateRoutes mounts the MCP template admin API. Console gets
// read-only list/detail; admin gets full CRUD plus render/instantiate.
func registerTemplateRoutes(register func(string, string, http.HandlerFunc), admin, console aiHandler) {
	for _, base := range []string{"/v3/admin/ai/mcp/templates", "/v3/console/ai/mcp/templates"} {
		register(http.MethodGet, base+"/list", admin.templateList)
		register(http.MethodGet, base+"/detail", admin.templateDetail)
	}
	register(http.MethodPost, "/v3/admin/ai/mcp/templates/create", admin.templateCreate)
	register(http.MethodPut, "/v3/admin/ai/mcp/templates/update", admin.templateUpdate)
	register(http.MethodDelete, "/v3/admin/ai/mcp/templates/delete", admin.templateDelete)
	register(http.MethodPost, "/v3/admin/ai/mcp/templates/render", admin.templateRender)
	register(http.MethodPost, "/v3/admin/ai/mcp/templates/instantiate", admin.templateInstantiate)
	// Console read-only.
	register(http.MethodGet, "/v3/console/ai/mcp/templates/list", console.templateList)
	register(http.MethodGet, "/v3/console/ai/mcp/templates/detail", console.templateDetail)
}

// templateList returns builtin + user templates.
func (h aiHandler) templateList(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	protocol.WriteResult(w, http.StatusOK, h.service.ListTemplates())
}

// templateDetail returns a single template by ID.
func (h aiHandler) templateDetail(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	tmpl, err := h.service.GetTemplate(formValue(r, "id"))
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, tmpl)
}

// templateCreate stores a new user template. The body is JSON matching the
// mcptemplate.Template struct.
func (h aiHandler) templateCreate(w http.ResponseWriter, r *http.Request) {
	var tmpl mcptemplate.Template
	if err := decodeJSONRequest(r, &tmpl); err != nil {
		writeTemplateError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.service.CreateTemplate(tmpl)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// templateUpdate replaces an existing user template.
func (h aiHandler) templateUpdate(w http.ResponseWriter, r *http.Request) {
	var tmpl mcptemplate.Template
	if err := decodeJSONRequest(r, &tmpl); err != nil {
		writeTemplateError(w, http.StatusBadRequest, err.Error())
		return
	}
	res, err := h.service.UpdateTemplate(tmpl)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, res)
}

// templateDelete removes a user template by ID.
func (h aiHandler) templateDelete(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	if err := h.service.DeleteTemplate(formValue(r, "id")); err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, true)
}

// templateRender renders a template with the provided variable values and
// returns the YAML bytes. The form must contain "id" plus any variable
// values as top-level form fields (e.g. "serverName=foo&toolName=bar").
func (h aiHandler) templateRender(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := formValue(r, "id")
	if strings.TrimSpace(id) == "" {
		writeAIError(w, ai.ErrTemplateIDRequired)
		return
	}
	values := collectFormValues(r, "id")
	out, err := h.service.RenderTemplate(id, values)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, map[string]any{
		"id":     id,
		"yaml":   string(out),
		"rendered": true,
	})
}

// templateInstantiate renders a template and creates an apitomcp config from
// the result. The form must contain "id" plus variable values.
func (h aiHandler) templateInstantiate(w http.ResponseWriter, r *http.Request) {
	if !parseForm(w, r) {
		return
	}
	id := formValue(r, "id")
	if strings.TrimSpace(id) == "" {
		writeAIError(w, ai.ErrTemplateIDRequired)
		return
	}
	values := collectFormValues(r, "id")
	cfg, err := h.service.InstantiateTemplate(id, values)
	if err != nil {
		writeAIError(w, err)
		return
	}
	protocol.WriteResult(w, http.StatusOK, cfg)
}

// collectFormValues returns all form values except the excluded keys. This
// is used to pass variable values to RenderTemplate/InstantiateTemplate
// without needing to know the variable names upfront.
func collectFormValues(r *http.Request, exclude ...string) map[string]string {
	out := map[string]string{}
	ex := map[string]bool{}
	for _, k := range exclude {
		ex[k] = true
	}
	for k, v := range r.Form {
		if ex[k] {
			continue
		}
		if len(v) > 0 {
			out[k] = v[0]
		}
	}
	return out
}

// writeTemplateError maps an HTTP status to a protocol error code. Used for
// JSON-body validation errors that bypass writeAIError.
func writeTemplateError(w http.ResponseWriter, status int, msg string) {
	code := protocol.CodeParameterValidateError
	switch status {
	case http.StatusNotFound:
		code = protocol.CodeNotFound
	case http.StatusConflict:
		code = protocol.CodeConflict
	}
	protocol.WriteError(w, status, protocol.Error{
		Code:    code,
		Message: msg,
	})
}
