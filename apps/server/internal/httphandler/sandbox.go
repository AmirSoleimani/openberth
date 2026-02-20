package httphandler

import (
	"net/http"
	"strings"

	"github.com/openberth/openberth/apps/server/internal/service"
)

func (h *Handlers) SandboxCreate(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	var req struct {
		Files           map[string]string `json:"files"`
		Name            string            `json:"name"`
		TTL             string            `json:"ttl"`
		Port            int               `json:"port"`
		Env             map[string]string `json:"env"`
		Memory          string            `json:"memory"`
		NetworkQuota    string            `json:"network_quota"`
		Language        string            `json:"language"`
		ProtectMode     string            `json:"protect_mode"`
		ProtectUsername  string            `json:"protect_username"`
		ProtectPassword string            `json:"protect_password"`
		ProtectApiKey   string            `json:"protect_api_key"`
		ProtectUsers    []string          `json:"protect_users"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.CreateSandbox(user, service.SandboxCreateParams{
		Files:           req.Files,
		Name:            req.Name,
		TTL:             req.TTL,
		Port:            req.Port,
		Env:             req.Env,
		Memory:          req.Memory,
		NetworkQuota:    req.NetworkQuota,
		Language:        req.Language,
		ProtectMode:     req.ProtectMode,
		ProtectUsername:  req.ProtectUsername,
		ProtectPassword: req.ProtectPassword,
		ProtectApiKey:   req.ProtectApiKey,
		ProtectUsers:    strings.Join(req.ProtectUsers, ","),
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 202, result)
}

func (h *Handlers) SandboxPush(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Changes []service.PushChange `json:"changes"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.SandboxPush(user, service.PushParams{
		SandboxID: id,
		Changes:   req.Changes,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

func (h *Handlers) SandboxInstall(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Packages  []string `json:"packages"`
		Uninstall bool     `json:"uninstall"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.SandboxInstall(user, service.InstallParams{
		SandboxID: id,
		Packages:  req.Packages,
		Uninstall: req.Uninstall,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

func (h *Handlers) SandboxExec(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		Command string `json:"command"`
		Timeout int    `json:"timeout"`
	}
	if err := decodeJSONBody(r, &req); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	result, err := h.svc.SandboxExec(user, service.ExecParams{
		SandboxID: id,
		Command:   req.Command,
		Timeout:   req.Timeout,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 200, result)
}

func (h *Handlers) PromoteSandbox(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	var req struct {
		TTL          string            `json:"ttl"`
		Memory       string            `json:"memory"`
		CPUs         string            `json:"cpus"`
		NetworkQuota string            `json:"network_quota"`
		Env          map[string]string `json:"env"`
	}
	// Allow empty body
	decodeJSONBody(r, &req)

	result, err := h.svc.PromoteSandbox(user, service.PromoteParams{
		SandboxID:    id,
		TTL:          req.TTL,
		Memory:       req.Memory,
		CPUs:         req.CPUs,
		NetworkQuota: req.NetworkQuota,
		Env:          req.Env,
	})
	if err != nil {
		writeErr(w, err)
		return
	}

	jsonResp(w, 202, result)
}

func (h *Handlers) SandboxLogs(w http.ResponseWriter, r *http.Request) {
	user := h.requireAuth(w, r)
	if user == nil {
		return
	}

	id := r.PathValue("id")
	result, err := h.svc.GetLogs(user, id, parseTail(r))
	if err != nil {
		writeErr(w, err)
		return
	}
	jsonResp(w, 200, result)
}
