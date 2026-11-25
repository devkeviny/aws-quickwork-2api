package admin

import (
	"errors"
	"net/http"

	"quickwork-openai/internal/outbound"
	"quickwork-openai/internal/store"
)

func (s *Server) handleProxies(w http.ResponseWriter, r *http.Request) {
	proxies, err := s.store.Proxies(r.Context())
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not list proxies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"proxies": proxies})
}

func (s *Server) handleProxyImport(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Proxies string `json:"proxies"`
	}
	if !decodeJSON(w, r, &input, 1<<20) {
		return
	}
	if _, err := outbound.ParseList(input.Proxies); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_proxy", err.Error())
		return
	}
	imported, err := s.store.ImportProxies(r.Context(), input.Proxies)
	input.Proxies = ""
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not import proxies")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"imported": imported})
}

func (s *Server) handleProxyDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == "" || id == "direct" {
		writeError(w, http.StatusBadRequest, "invalid_proxy", "Select a stored proxy")
		return
	}
	s.configMu.Lock()
	defer s.configMu.Unlock()
	if id == s.cfg.ProxyID || id == s.saved.ProxyID {
		writeError(w, http.StatusConflict, "proxy_in_use", "Change the default proxy before deleting it")
		return
	}
	removed, err := s.store.DeleteProxy(r.Context(), id)
	if errors.Is(err, store.ErrProxyInUse) {
		writeError(w, http.StatusConflict, "proxy_in_use", "Unbind accounts before deleting this proxy")
		return
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "database_error", "Could not delete proxy")
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, "proxy_not_found", "Proxy not found")
		return
	}
	s.cfg.Proxies.CloseIdleConnections()
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
