package api

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"regexp"
	"strings"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

// topicPathPattern rejects leading/trailing dots and hyphens.
// First char: [A-Za-z0-9]; optional middle + final [A-Za-z0-9] for multi-char paths.
var topicPathPattern = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._-]*[A-Za-z0-9])?$`)

// handleHelpPreflight writes a CORS preflight response and returns true if the
// request was an OPTIONS preflight. The caller should return immediately when
// this function returns true.
func handleHelpPreflight(w http.ResponseWriter, r *http.Request) bool {
	if r.Method != http.MethodOptions {
		return false
	}
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
	w.Header().Set("Access-Control-Max-Age", "86400")
	w.WriteHeader(http.StatusNoContent)
	return true
}

// RegisterHelpRoutes mounts GET {contextPath}/help and
// GET {contextPath}/help/{topic} on the given mux. contextPath must NOT
// have a trailing slash. An empty contextPath mounts at "/help".
// version is closed over by the handlers and reported in the full-tree payload.
func RegisterHelpRoutes(mux *http.ServeMux, tree *help.Tree, contextPath, version string) {
	prefix := strings.TrimRight(contextPath, "/") + "/help"
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		if handleHelpPreflight(w, r) {
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.URL.Path != prefix {
			common.WriteError(w, r, common.Operational(
				http.StatusNotFound,
				common.ErrCodeHelpTopicNotFound,
				"no such help topic at this path",
			))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(renderer.HelpPayload{
			Schema:  1,
			Version: version,
			Topics:  tree.WalkDescriptors(),
		}); err != nil {
			slog.Error("help: failed to encode response", "error", err, "path", r.URL.Path)
		}
	})
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
		if handleHelpPreflight(w, r) {
			return
		}
		w.Header().Set("Access-Control-Allow-Origin", "*")
		topic := strings.TrimPrefix(r.URL.Path, prefix+"/")
		if !topicPathPattern.MatchString(topic) {
			common.WriteError(w, r, common.Operational(
				http.StatusBadRequest,
				common.ErrCodeBadRequest,
				"invalid topic path: contains disallowed characters",
			))
			return
		}
		segs := strings.Split(topic, ".")
		node := tree.Find(segs)
		if node == nil {
			common.WriteError(w, r, common.Operational(
				http.StatusNotFound,
				common.ErrCodeHelpTopicNotFound,
				"no such help topic: "+topic,
			))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		enc := json.NewEncoder(w)
		if err := enc.Encode(node.Descriptor()); err != nil {
			slog.Error("help: failed to encode response", "error", err, "path", r.URL.Path)
		}
	})
}
