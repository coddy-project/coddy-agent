//go:build http

package httpserver

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/serve"
)

// SetStatus gives the server the function that builds the status document
// (Options.Status) and registers GET /coddy/status for it. A server that is
// never given one has no such route and answers 404 for it, as a server of an
// older version does, which is what the web page reads as "no System page
// here". It is called before the server serves, like the other setters.
func (s *Server) SetStatus(doc func() serve.StatusDocument) {
	if doc == nil {
		return
	}
	first := s.statusDoc == nil
	s.statusDoc = doc
	if first {
		s.mux.HandleFunc("GET /coddy/status", s.coddyStatusGet)
	}
}

// coddyStatusGet says what this installation is, how it was started, what runs
// and what needs attention. The document is built where the process keeps what
// it reports (Runtime.StatusDocument), so this only encodes it. The route sits
// behind the gate of the API like any other of /coddy; it takes no credential
// of its own, and the metrics exporter's token is not one of the API's.
func (s *Server) coddyStatusGet(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	writeJSON(w, http.StatusOK, s.statusDoc())
}

// statusOpenAPI is GET /coddy/status and the schemas of its document, as the
// JSON that mergeStatusOpenAPI adds to the served spec. The members and the
// required ones of each schema are those of the matching struct in
// internal/serve, and the enumerations its constants; a test holds them to it.
// A JSON constant rather than map literals keeps the binary small, and
// docs/reference/http-api.md says the rest. A reference is written "#/Name"
// and mergeStatusOpenAPI adds the components/schemas path, so the text does
// not spell it out at every reference.
const statusOpenAPI = `{"path":{"get":{"operationId":"coddyStatusGet",` +
	`"summary":"What this installation is, what runs and what needs attention",` +
	`"description":"A server of an older version answers 404.",` +
	`"responses":{"200":{"description":"The status document","content":{"application/json":{"schema":{"$ref":"#/CoddyStatus"}}}},` +
	`"401":{"description":"Error","content":{"application/json":{"schema":{"$ref":"#/ErrorEnvelope"}}}}}}},` +
	`"schemas":{` +
	`"CoddyStatus":{"type":"object",` +
	`"required":["object","generatedAt","build","process","subsystems","turns"],"properties":{` +
	`"object":{"type":"string","enum":["coddy.status"]},"generatedAt":{"type":"string","format":"date-time"},` +
	`"build":{"$ref":"#/CoddyStatusBuild"},"process":{"$ref":"#/CoddyStatusProcess"},` +
	`"subsystems":{"type":"array","items":{"$ref":"#/CoddyStatusSubsystem"}},` +
	`"restartRequired":{"type":"boolean"},"restartRefused":{"type":"string"},` +
	`"turns":{"$ref":"#/CoddyStatusTurns"},"prompts":{"$ref":"#/CoddyStatusPrompts"},` +
	`"tasks":{"type":"object","description":"By kind, then status.","additionalProperties":{"type":"object","additionalProperties":{"type":"integer"}}},` +
	`"disk":{"$ref":"#/CoddyStatusDisk"},"unavailable":{"type":"array","items":{"type":"string","enum":["disk"]}},` +
	`"attention":{"type":"array","items":{"$ref":"#/CoddyStatusAttentionItem"}},` +
	`"metrics":{"$ref":"#/CoddyStatusMetrics"}}},` +
	`"CoddyStatusBuild":{"type":"object","required":["version","goVersion","os","arch"],"properties":{` +
	`"version":{"type":"string"},"revision":{"type":"string"},"goVersion":{"type":"string"},"os":{"type":"string"},"arch":{"type":"string"},` +
	`"tags":{"type":"array","items":{"type":"string"}}}},` +
	`"CoddyStatusProcess":{"type":"object","required":["startedAt","startedBy","configFile"],"properties":{` +
	`"startedAt":{"type":"string","format":"date-time"},"startedBy":{"type":"string","enum":["systemd","daemon","foreground"]},` +
	`"configFile":{"type":"string"}}},` +
	`"CoddyStatusSubsystem":{"type":"object","required":["kind","state"],"properties":{` +
	`"kind":{"type":"string"},"state":{"type":"string","enum":["running","starting","stopped","retained"]},` +
	`"address":{"type":"string"},"pendingAddress":{"type":"string"},` +
	`"credential":{"type":"string","enum":["token","none"]}}},` +
	`"CoddyStatusTurns":{"type":"object","required":["running"],"properties":{"running":{"type":"integer"},"oldestSeconds":{"type":"integer"}}},` +
	`"CoddyStatusPrompts":{"type":"object","properties":{` +
	`"permission":{"type":"integer"},"question":{"type":"integer"}}},` +
	`"CoddyStatusDisk":{"type":"object","required":["availableBytes","sizeBytes"],"properties":{"availableBytes":{"type":"integer"},"sizeBytes":{"type":"integer"}}},` +
	`"CoddyStatusAttentionItem":{"type":"object","required":["code"],"properties":{` +
	`"code":{"type":"string","enum":["restart_required","disk_low","subsystem_down","provider_failing","swarm_join_down"]},` +
	`"subsystem":{"type":"string"},"provider":{"type":"string"},"relay":{"type":"string"},` +
	`"offlineSince":{"type":"string","format":"date-time"}}},` +
	`"CoddyStatusMetrics":{"type":"object","required":["providers","series"],"properties":{` +
	`"providers":{"type":"array","items":{"$ref":"#/CoddyStatusProvider"}},"series":{"$ref":"#/CoddyStatusSeries"}}},` +
	`"CoddyStatusProvider":{"type":"object","required":["name","attempts","failures"],"properties":{` +
	`"name":{"type":"string"},"attempts":{"type":"integer"},"failures":{"type":"integer"},"meanSeconds":{"type":"number"},` +
	`"lastFailureAt":{"type":"string","format":"date-time"},"lastOkAt":{"type":"string","format":"date-time"}}},` +
	`"CoddyStatusSeries":{"type":"object","description":"Per minute; the mean in seconds.",` +
	`"required":["stepSeconds","end","attemptsOk","providerErrors","attemptSecondsMean","tokensIn","tokensOut","turnsRunning","tasksUnfinished"],"properties":{` +
	`"stepSeconds":{"type":"number"},"end":{"type":"string","format":"date-time"},` +
	`"attemptsOk":{"$ref":"#/CoddyStatusSamples"},"providerErrors":{"$ref":"#/CoddyStatusSamples"},` +
	`"attemptSecondsMean":{"$ref":"#/CoddyStatusSamples"},"tokensIn":{"$ref":"#/CoddyStatusSamples"},` +
	`"tokensOut":{"$ref":"#/CoddyStatusSamples"},"turnsRunning":{"$ref":"#/CoddyStatusSamples"},` +
	`"tasksUnfinished":{"$ref":"#/CoddyStatusSamples"}}},` +
	`"CoddyStatusSamples":{"type":"array","description":"Oldest first; null is a gap.","items":{"type":"number","nullable":true}}}}`

// mergeStatusOpenAPI adds GET /coddy/status and the schemas of its document to
// the served spec. A statusOpenAPI that is not valid JSON is a mistake in the
// source, caught by every test that builds the spec.
func mergeStatusOpenAPI(doc map[string]interface{}) {
	var part struct {
		Path    interface{}            `json:"path"`
		Schemas map[string]interface{} `json:"schemas"`
	}
	if err := json.Unmarshal([]byte(strings.ReplaceAll(statusOpenAPI, `"#/`, `"#/components/schemas/`)), &part); err != nil {
		panic("httpserver: statusOpenAPI is not JSON: " + err.Error())
	}
	doc["paths"].(map[string]interface{})["/coddy/status"] = part.Path
	schemas := doc["components"].(map[string]interface{})["schemas"].(map[string]interface{})
	for name, schema := range part.Schemas {
		schemas[name] = schema
	}
}
