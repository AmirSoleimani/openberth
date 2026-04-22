package httphandler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/AmirSoleimani/openberth/apps/server/internal/datastore"
)

// DataHandler serves the /_data/* REST API for per-deployment document storage.
// Caddy routes /_data/* requests from deployment subdomains to this handler.
// Uses the centralized SetCORSHeaders for consistent CORS across all endpoints.
func (h *Handlers) DataHandler(w http.ResponseWriter, r *http.Request) {
	SetCORSHeaders(w)
	if r.Method == http.MethodOptions {
		w.WriteHeader(204)
		return
	}

	// Extract subdomain from Host header. Host names are case-insensitive per
	// RFC 3986, but stored subdomains are always lowercase (SanitizeName) and
	// Cfg.Domain is lowercased at config load — so normalize the incoming host
	// before matching to avoid case-mismatch misses on forged/uppercase hosts.
	host := strings.ToLower(r.Host)
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}
	subdomain := strings.TrimSuffix(host, "."+h.svc.Cfg.Domain)
	if subdomain == "" || subdomain == host {
		jsonErr(w, 400, "Cannot determine deployment from hostname")
		return
	}

	// Verify deployment exists and is running
	deploy, err := h.svc.Store.GetDeploymentBySubdomain(subdomain)
	if err != nil || deploy == nil {
		jsonErr(w, 404, "Deployment not found")
		return
	}
	if deploy.Status != "running" {
		jsonErr(w, 503, "Deployment is not running")
		return
	}

	// Parse path: /_data, /_data/{collection}, /_data/{collection}/{id}
	path := strings.TrimPrefix(r.URL.Path, "/_data")
	path = strings.TrimPrefix(path, "/")
	path = strings.TrimSuffix(path, "/")

	parts := strings.SplitN(path, "/", 2)

	collection := ""
	docID := ""
	if len(parts) >= 1 {
		collection = parts[0]
	}
	if len(parts) >= 2 {
		docID = parts[1]
	}

	// Route: /_data -> list collections
	if collection == "" {
		if r.Method == http.MethodGet {
			h.dataListCollections(w, deploy.ID)
			return
		}
		jsonErr(w, 405, "Method not allowed")
		return
	}

	// Route: /_data/{collection}/{id} -> single document
	if docID != "" {
		switch r.Method {
		case http.MethodGet:
			h.dataGetDocument(w, deploy.ID, collection, docID)
		case http.MethodPut:
			h.dataUpdateDocument(w, r, deploy.ID, collection, docID)
		case http.MethodDelete:
			h.dataDeleteDocument(w, deploy.ID, collection, docID)
		default:
			jsonErr(w, 405, "Method not allowed")
		}
		return
	}

	// Route: /_data/{collection} -> collection operations
	switch r.Method {
	case http.MethodGet:
		h.dataListDocuments(w, r, deploy.ID, collection)
	case http.MethodPost:
		h.dataCreateDocument(w, r, deploy.ID, collection)
	case http.MethodDelete:
		h.dataDeleteCollection(w, deploy.ID, collection)
	default:
		jsonErr(w, 405, "Method not allowed")
	}
}

func (h *Handlers) dataListCollections(w http.ResponseWriter, deployID string) {
	collections, err := h.svc.DataStore.ListCollections(deployID)
	if err != nil {
		jsonErr(w, 500, "Failed to list collections: "+err.Error())
		return
	}
	if collections == nil {
		collections = []datastore.CollectionInfo{}
	}
	jsonResp(w, 200, map[string]interface{}{"collections": collections})
}

func (h *Handlers) dataListDocuments(w http.ResponseWriter, r *http.Request, deployID, collection string) {
	limit := 100
	offset := 0
	if l, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && l > 0 {
		limit = l
	}
	if o, err := strconv.Atoi(r.URL.Query().Get("offset")); err == nil && o >= 0 {
		offset = o
	}

	docs, total, err := h.svc.DataStore.ListDocuments(deployID, collection, limit, offset)
	if err != nil {
		jsonErr(w, 500, "Failed to list documents: "+err.Error())
		return
	}
	if docs == nil {
		docs = []datastore.Document{}
	}
	jsonResp(w, 200, map[string]interface{}{
		"documents": docs,
		"total":     total,
		"limit":     limit,
		"offset":    offset,
	})
}

func (h *Handlers) dataCreateDocument(w http.ResponseWriter, r *http.Request, deployID, collection string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 200*1024))
	if err != nil {
		jsonErr(w, 400, "Failed to read body")
		return
	}

	var data json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	doc, err := h.svc.DataStore.CreateDocument(deployID, collection, data)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}

	jsonResp(w, 201, doc)
}

func (h *Handlers) dataGetDocument(w http.ResponseWriter, deployID, collection, docID string) {
	doc, err := h.svc.DataStore.GetDocument(deployID, collection, docID)
	if err != nil {
		jsonErr(w, 500, "Failed to get document: "+err.Error())
		return
	}
	if doc == nil {
		jsonErr(w, 404, "Document not found")
		return
	}
	jsonResp(w, 200, doc)
}

func (h *Handlers) dataUpdateDocument(w http.ResponseWriter, r *http.Request, deployID, collection, docID string) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 200*1024))
	if err != nil {
		jsonErr(w, 400, "Failed to read body")
		return
	}

	var data json.RawMessage
	if err := json.Unmarshal(body, &data); err != nil {
		jsonErr(w, 400, "Invalid JSON: "+err.Error())
		return
	}

	doc, err := h.svc.DataStore.UpdateDocument(deployID, collection, docID, data)
	if err != nil {
		jsonErr(w, 400, err.Error())
		return
	}
	if doc == nil {
		jsonErr(w, 404, "Document not found")
		return
	}

	jsonResp(w, 200, doc)
}

func (h *Handlers) dataDeleteDocument(w http.ResponseWriter, deployID, collection, docID string) {
	if err := h.svc.DataStore.DeleteDocument(deployID, collection, docID); err != nil {
		if err.Error() == "not found" {
			jsonErr(w, 404, "Document not found")
			return
		}
		jsonErr(w, 500, "Failed to delete document: "+err.Error())
		return
	}
	jsonResp(w, 200, map[string]string{"status": "deleted"})
}

func (h *Handlers) dataDeleteCollection(w http.ResponseWriter, deployID, collection string) {
	count, err := h.svc.DataStore.DeleteCollection(deployID, collection)
	if err != nil {
		jsonErr(w, 500, "Failed to delete collection: "+err.Error())
		return
	}
	jsonResp(w, 200, map[string]interface{}{"status": "deleted", "count": count})
}
