package api

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help"
	"github.com/cyoda-platform/cyoda-go/cmd/cyoda/help/renderer"
	"github.com/cyoda-platform/cyoda-go/internal/common"
)

var topicPathPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// helpBinaryVersion is injected via SetHelpBinaryVersion; defaults to "dev" for tests.
var helpBinaryVersion = "dev"

// SetHelpBinaryVersion wires the version string displayed in the help
// payload. Called from the app bootstrap when the ldflag version is known.
func SetHelpBinaryVersion(v string) { helpBinaryVersion = v }

// RegisterHelpRoutes mounts GET {contextPath}/help and
// GET {contextPath}/help/{topic} on the given mux. contextPath must NOT
// have a trailing slash. An empty contextPath mounts at "/help".
func RegisterHelpRoutes(mux *http.ServeMux, tree *help.Tree, contextPath string) {
	prefix := strings.TrimRight(contextPath, "/") + "/help"
	mux.HandleFunc(prefix, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		if r.URL.Path != prefix {
			common.WriteError(w, r, common.Operational(
				http.StatusNotFound,
				common.ErrCodeHelpTopicNotFound,
				"no such help topic: "+strings.TrimPrefix(r.URL.Path, prefix+"/"),
			))
			return
		}
		serveFullHelpTree(w, tree)
	})
	mux.HandleFunc(prefix+"/", func(w http.ResponseWriter, r *http.Request) {
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
		serveSingleHelpTopic(w, node)
	})
}

func serveFullHelpTree(w http.ResponseWriter, tree *help.Tree) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(renderer.HelpPayload{
		Schema:  1,
		Version: helpBinaryVersion,
		Topics:  tree.WalkDescriptors(),
	})
}

func serveSingleHelpTopic(w http.ResponseWriter, t *help.Topic) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	_ = enc.Encode(t.Descriptor())
}
