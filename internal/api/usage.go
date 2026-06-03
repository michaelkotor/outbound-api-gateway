package api

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/michaelkotor/outbound-api-gateway/internal/storage"
)

// Handler serves the /usage endpoints backed by a storage.Storage. Fingerprints
// and configured limits are supplied separately because the storage records key
// names and counts, not secrets or the configured caps.
type Handler struct {
	usageStorage     storage.Storage
	fingerprints     map[string]string                  // keyName -> fingerprint
	configuredLimits map[string]map[time.Duration]int64 // keyName -> window -> max requests
}

// NewHandler constructs a usage API handler. fingerprints maps a key name to its
// safe display token; configuredLimits maps a key name and window to its
// configured request cap. Both may be nil.
func NewHandler(usageStorage storage.Storage, fingerprints map[string]string, configuredLimits map[string]map[time.Duration]int64) *Handler {
	if fingerprints == nil {
		fingerprints = map[string]string{}
	}
	if configuredLimits == nil {
		configuredLimits = map[string]map[time.Duration]int64{}
	}
	return &Handler{usageStorage: usageStorage, fingerprints: fingerprints, configuredLimits: configuredLimits}
}

// Usage handles GET /usage and GET /usage?route={name}.
func (handler *Handler) Usage(responseWriter http.ResponseWriter, request *http.Request) {
	route := request.URL.Query().Get("route")
	usageSnapshots, err := handler.usageStorage.ListUsage(request.Context(), route)
	if err != nil {
		http.Error(responseWriter, "failed to read usage", http.StatusInternalServerError)
		return
	}
	handler.write(responseWriter, usageSnapshots)
}

// UsageByKey handles GET /usage/{key_name}.
func (handler *Handler) UsageByKey(responseWriter http.ResponseWriter, request *http.Request) {
	keyName := chi.URLParam(request, "key_name")
	allUsage, err := handler.usageStorage.ListUsage(request.Context(), "")
	if err != nil {
		http.Error(responseWriter, "failed to read usage", http.StatusInternalServerError)
		return
	}
	filteredUsage := make([]storage.KeyUsage, 0)
	for _, keyUsage := range allUsage {
		if keyUsage.KeyName == keyName {
			filteredUsage = append(filteredUsage, keyUsage)
		}
	}
	handler.write(responseWriter, filteredUsage)
}

// write serializes usage snapshots into the public UsageResponse schema.
func (handler *Handler) write(responseWriter http.ResponseWriter, usageSnapshots []storage.KeyUsage) {
	response := UsageResponse{
		GeneratedAt: time.Now(),
		Keys:        make([]KeyPayload, 0, len(usageSnapshots)),
	}
	for _, keyUsage := range usageSnapshots {
		fingerprint := keyUsage.Fingerprint
		if fingerprint == "" {
			fingerprint = handler.fingerprints[keyUsage.KeyName]
		}
		windowPayloads := make([]WindowPayload, 0, len(keyUsage.Windows))
		for _, window := range keyUsage.Windows {
			configuredLimit := window.MaxRequests
			if windowsForKey, ok := handler.configuredLimits[keyUsage.KeyName]; ok {
				if maxRequests, ok := windowsForKey[window.Window]; ok {
					configuredLimit = maxRequests
				}
			}
			windowPayloads = append(windowPayloads, WindowPayload{
				Window:   window.Window.String(),
				Used:     window.Count,
				Limit:    configuredLimit,
				ResetsAt: window.ResetsAt,
			})
		}
		response.Keys = append(response.Keys, KeyPayload{
			Name:        keyUsage.KeyName,
			Fingerprint: fingerprint,
			Route:       keyUsage.Route,
			Windows:     windowPayloads,
			LastUsed:    keyUsage.LastUsed,
			CooledUntil: keyUsage.CooledUntil,
		})
	}

	responseWriter.Header().Set("Content-Type", "application/json")
	responseWriter.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(responseWriter).Encode(response)
}
