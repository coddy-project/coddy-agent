package llm

import (
	"bytes"
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

// wireCoverage lists, for each llm type a model call carries, the exported
// fields that travel on the coddy wire, the ones that stay local to the
// process that owns the history, and the ones folded into another field
// before a call. A field added to the type without a decision here fails
// TestWireCoversEveryExportedField, so the wire never silently drops or
// leaks a field.
var wireCoverage = []struct {
	typ    reflect.Type
	wire   []string
	local  []string
	folded []string
}{
	{
		typ:  reflect.TypeOf(Message{}),
		wire: []string{"Role", "Content", "ImageParts", "Reasoning", "ReasoningSignature", "ToolCalls", "ToolCallID"},
		// Artifacts and the absolute paths are the local host's; the rest is
		// transcript bookkeeping no provider is sent.
		local:  []string{"Artifacts", "ReasoningDurationMs", "Model", "CreatedAt", "PlanDocument", "CompactionSummary", "BackgroundWake"},
		folded: []string{"Rules"},
	},
	{
		typ:   reflect.TypeOf(ImagePart{}),
		wire:  []string{"DataURL", "MIMEType", "Name"},
		local: []string{"FilePath", "ThumbnailPath", "Size"},
	},
	{
		typ:  reflect.TypeOf(ToolCall{}),
		wire: []string{"ID", "Name", "InputJSON"},
	},
	{
		typ:  reflect.TypeOf(ToolDefinition{}),
		wire: []string{"Name", "Description", "InputSchema"},
	},
	{
		typ: reflect.TypeOf(StreamChunk{}),
		wire: []string{"TextDelta", "ReasoningDelta", "ToolCall", "ToolCallDelta", "ToolCallNamed",
			"StopReason", "InputTokens", "OutputTokens"},
	},
	{
		typ: reflect.TypeOf(Response{}),
		wire: []string{"Content", "ToolCalls", "Reasoning", "ReasoningSignature", "StopReason",
			"InputTokens", "OutputTokens", "CachedInputTokens"},
	},
}

// uncoveredWireFields returns the exported fields of typ that are in none of
// the lists, and the listed names typ does not have.
func uncoveredWireFields(typ reflect.Type, lists ...[]string) (missing, unknown []string) {
	listed := map[string]bool{}
	for _, l := range lists {
		for _, name := range l {
			listed[name] = true
		}
	}
	have := map[string]bool{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		have[f.Name] = true
		if !listed[f.Name] {
			missing = append(missing, f.Name)
		}
	}
	for name := range listed {
		if !have[name] {
			unknown = append(unknown, name)
		}
	}
	slices.Sort(unknown)
	return missing, unknown
}

func TestWireCoversEveryExportedField(t *testing.T) {
	for _, c := range wireCoverage {
		missing, unknown := uncoveredWireFields(c.typ, c.wire, c.local, c.folded)
		if len(missing) > 0 {
			t.Errorf("%s: exported fields %v are neither on the coddy wire nor in the local-only list: decide in wireCoverage and convert them in coddy_wire.go",
				c.typ.Name(), missing)
		}
		if len(unknown) > 0 {
			t.Errorf("%s: wireCoverage names fields the type no longer has: %v", c.typ.Name(), unknown)
		}
	}
}

func TestWireCoverageCheckerReportsAnUnlistedField(t *testing.T) {
	type sample struct {
		Kept   string
		Added  int
		hidden string
	}
	_ = sample{}.hidden
	missing, unknown := uncoveredWireFields(reflect.TypeOf(sample{}), []string{"Kept"}, []string{"Gone"})
	if !slices.Equal(missing, []string{"Added"}) {
		t.Fatalf("missing = %v, want [Added]", missing)
	}
	if !slices.Equal(unknown, []string{"Gone"}) {
		t.Fatalf("unknown = %v, want [Gone]", unknown)
	}
}

// fillNonZero sets every exported field of v to a value that is not the zero
// value, so a field a conversion forgets shows up as a difference.
func fillNonZero(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		v.SetString("x-" + v.Type().Name())
	case reflect.Int, reflect.Int32, reflect.Int64:
		v.SetInt(7)
	case reflect.Bool:
		v.SetBool(true)
	case reflect.Slice:
		s := reflect.MakeSlice(v.Type(), 1, 1)
		fillNonZero(s.Index(0))
		v.Set(s)
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		fillNonZero(p.Elem())
		v.Set(p)
	case reflect.Interface:
		v.Set(reflect.ValueOf(map[string]any{"type": "object", "required": []any{"city"}}))
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				fillNonZero(v.Field(i))
			}
		}
	}
}

// zeroFields clears the named exported fields of the struct v points at.
func zeroFields(v reflect.Value, names []string) {
	for _, n := range names {
		f := v.FieldByName(n)
		f.Set(reflect.Zero(f.Type()))
	}
}

// viaJSON sends v through the encoding the wire uses.
func viaJSON[T any](t *testing.T, v T) T {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("unmarshal %s: %v", raw, err)
	}
	return out
}

func coverageFor(t *testing.T, typ reflect.Type) (local []string) {
	t.Helper()
	for _, c := range wireCoverage {
		if c.typ == typ {
			return append(slices.Clone(c.local), c.folded...)
		}
	}
	t.Fatalf("no wireCoverage entry for %s", typ)
	return nil
}

func TestWireMessageRoundTripCarriesTheWireFieldsAndClearsTheLocalOnes(t *testing.T) {
	var msg Message
	fillNonZero(reflect.ValueOf(&msg).Elem())

	got := WireMessagesToLLM(viaJSON(t, WireMessagesFromLLM([]Message{msg})))
	if len(got) != 1 {
		t.Fatalf("got %d messages, want 1", len(got))
	}

	want := msg
	zeroFields(reflect.ValueOf(&want).Elem(), coverageFor(t, reflect.TypeOf(Message{})))
	for i := range want.ImageParts {
		zeroFields(reflect.ValueOf(&want.ImageParts[i]).Elem(), coverageFor(t, reflect.TypeOf(ImagePart{})))
	}
	if !reflect.DeepEqual(got[0], want) {
		t.Fatalf("round trip lost or leaked a field\n got: %+v\nwant: %+v", got[0], want)
	}
}

func TestWireNeverCarriesALocalPath(t *testing.T) {
	msg := Message{
		Role: RoleUser, Content: "see the picture",
		ImageParts: []ImagePart{{DataURL: "data:image/png;base64,AAAA", MIMEType: "image/png", Name: "a.png",
			FilePath: "/home/alice/.coddy/sessions/s/assets/a.png", ThumbnailPath: "/home/alice/.coddy/t.png", Size: 4}},
		Artifacts: []Artifact{{ID: "1", Name: "report", SourcePath: "/home/alice/work/report.pdf"}},
	}
	raw, err := json.Marshal(WireMessagesFromLLM([]Message{msg}))
	if err != nil {
		t.Fatal(err)
	}
	for _, leak := range []string{"/home/alice", "file_path", "thumbnail_path", "artifacts", "report"} {
		if strings.Contains(string(raw), leak) {
			t.Errorf("the wire message mentions %q: %s", leak, raw)
		}
	}
	if !strings.Contains(string(raw), "data:image/png;base64,AAAA") {
		t.Errorf("the wire message lost the image bytes: %s", raw)
	}
}

func TestWireToolsRoundTrip(t *testing.T) {
	var tool ToolDefinition
	fillNonZero(reflect.ValueOf(&tool).Elem())
	got := WireToolsToLLM(viaJSON(t, WireToolsFromLLM([]ToolDefinition{tool})))
	if len(got) != 1 || !reflect.DeepEqual(got[0], tool) {
		t.Fatalf("tool round trip\n got: %+v\nwant: %+v", got, tool)
	}
}

func TestWireToolsEncodeDeterministically(t *testing.T) {
	schema := map[string]any{"type": "object", "properties": map[string]any{"b": map[string]any{"type": "string"}, "a": map[string]any{"type": "number"}}}
	first, err := json.Marshal(WireToolsFromLLM([]ToolDefinition{{Name: "t", Description: "d", InputSchema: schema}}))
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 20; i++ {
		again, _ := json.Marshal(WireToolsFromLLM([]ToolDefinition{{Name: "t", Description: "d", InputSchema: schema}}))
		if !bytes.Equal(first, again) {
			t.Fatalf("tool encoding is not deterministic:\n%s\n%s", first, again)
		}
	}
}

func TestWireChunkRoundTripMirrorsTheStreamChunk(t *testing.T) {
	var chunk StreamChunk
	fillNonZero(reflect.ValueOf(&chunk).Elem())
	got := viaJSON(t, WireChunkFromStream(chunk)).ToStreamChunk()
	if !reflect.DeepEqual(got, chunk) {
		t.Fatalf("chunk round trip\n got: %+v\nwant: %+v", got, chunk)
	}
}

func TestWireFinalRoundTripCarriesTheWholeResponse(t *testing.T) {
	var resp Response
	fillNonZero(reflect.ValueOf(&resp).Elem())
	got := viaJSON(t, WireFinalFromResponse(&resp)).ToResponse()
	if !reflect.DeepEqual(*got, resp) {
		t.Fatalf("final round trip\n got: %+v\nwant: %+v", *got, resp)
	}
	if got.CachedInputTokens == 0 {
		t.Fatal("cached_input_tokens did not survive the final frame")
	}
}

func TestWireFrameEncodingIsOneDataLineWithATypeAndNoEventField(t *testing.T) {
	for name, frame := range map[string]any{
		"chunk": WireChunkFromStream(StreamChunk{TextDelta: "hi"}),
		"final": WireFinalFromResponse(&Response{Content: "hi", StopReason: "end_turn"}),
		"error": WireError{Kind: "busy", Status: 429},
	} {
		line, err := EncodeWireFrame(frame)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		text := string(line)
		if !strings.HasPrefix(text, "data: {") || !strings.HasSuffix(text, "}\n\n") {
			t.Errorf("%s: frame is not `data: {json}` and a blank line: %q", name, text)
		}
		if strings.Contains(text, "event:") || strings.Count(text, "\n") != 2 {
			t.Errorf("%s: frame has an event field or spans lines: %q", name, text)
		}
		var head struct{ Type string }
		if err := json.Unmarshal([]byte(strings.TrimPrefix(strings.TrimSpace(text), "data: ")), &head); err != nil || head.Type != name {
			t.Errorf("%s: type = %q (%v)", name, head.Type, err)
		}
	}
}

func TestWireErrorBodyIsTheFlatObjectWithoutAType(t *testing.T) {
	raw, err := json.Marshal(WireError{Status: 429, Kind: "busy", Message: "all slots are taken", RetryAfterS: 1})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["type"]; ok {
		t.Errorf("the pre-stream error body must not carry a frame type: %s", raw)
	}
	for _, k := range []string{"status", "kind", "message", "retry_after_s", "emitted"} {
		if _, ok := m[k]; !ok {
			t.Errorf("error body lacks %q: %s", k, raw)
		}
	}
}

func TestWireRequestOmitsOptionsTheClientDidNotSet(t *testing.T) {
	raw, err := json.Marshal(WireRequest{Protocol: CoddyProtocol, Model: "coder", Messages: []WireMessage{}, Tools: []WireTool{}})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"options":{}`) {
		t.Fatalf("an empty options object is expected, got %s", raw)
	}
	if !strings.Contains(string(raw), `"protocol":1`) || !strings.Contains(string(raw), `"tools":[]`) || !strings.Contains(string(raw), `"messages":[]`) {
		t.Fatalf("protocol, messages and tools are always present: %s", raw)
	}
}

func TestWireListingShape(t *testing.T) {
	raw, err := json.Marshal(WireListing{Protocol: CoddyProtocol, Data: []WireModelRow{{
		ID: "coder", Revision: "rev1", MaxContextTokens: 200000, Multimodal: true,
		ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", AllowReasoningOff: true,
	}}})
	if err != nil {
		t.Fatal(err)
	}
	var back WireListing
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	want := WireModelRow{ID: "coder", Revision: "rev1", MaxContextTokens: 200000, Multimodal: true,
		ReasoningLevels: []string{"low", "high"}, ReasoningDefault: "low", AllowReasoningOff: true}
	if back.Protocol != 1 || len(back.Data) != 1 || !reflect.DeepEqual(back.Data[0], want) {
		t.Fatalf("listing round trip: %+v", back)
	}
	for _, key := range []string{`"id"`, `"revision"`, `"max_context_tokens"`, `"multimodal"`, `"reasoning_levels"`, `"reasoning_default"`, `"allow_reasoning_off"`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("listing row lacks %s: %s", key, raw)
		}
	}
	if strings.Contains(string(raw), "stream") {
		t.Errorf("the listing has no stream field: %s", raw)
	}
}
