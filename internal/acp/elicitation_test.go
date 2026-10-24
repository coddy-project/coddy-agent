package acp_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
)

func questionParams(questions ...acp.QuestionPrompt) acp.QuestionRequestParams {
	return acp.QuestionRequestParams{
		SessionID:  "sess_1",
		ToolCallID: "call_1",
		Questions:  questions,
	}
}

func TestQuestionElicitationNamesOneFieldPerQuestion(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(
		acp.QuestionPrompt{Question: "Pick a stack", Options: []acp.QuestionOption{{Label: "Go"}}},
		acp.QuestionPrompt{Question: "Pick a store", Options: []acp.QuestionOption{{Label: "SQLite"}}},
	))
	if req.Mode != acp.ElicitationModeForm {
		t.Fatalf("mode = %q, want %q", req.Mode, acp.ElicitationModeForm)
	}
	if req.SessionID != "sess_1" || req.ToolCallID != "call_1" {
		t.Fatalf("scope lost: %#v", req)
	}
	if req.RequestedSchema == nil || req.RequestedSchema.Type != "object" {
		t.Fatalf("no object schema: %#v", req.RequestedSchema)
	}
	if len(req.RequestedSchema.Properties) != 2 {
		t.Fatalf("properties = %d, want 2: %#v", len(req.RequestedSchema.Properties), req.RequestedSchema.Properties)
	}
	for _, name := range []string{"q1", "q2"} {
		if _, ok := req.RequestedSchema.Properties[name]; !ok {
			t.Fatalf("property %q missing: %#v", name, req.RequestedSchema.Properties)
		}
	}
	if len(req.RequestedSchema.Required) != 2 {
		t.Fatalf("required = %#v, want both fields", req.RequestedSchema.Required)
	}
}

func TestQuestionElicitationKeepsAPickerForAClosedQuestion(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Question: "Pick a stack",
		Options:  []acp.QuestionOption{{Label: "Go"}, {Label: "Rust"}},
		Custom:   false,
	}))
	prop := req.RequestedSchema.Properties["q1"]
	if prop.Type != "string" {
		t.Fatalf("type = %q, want string", prop.Type)
	}
	if got := strings.Join(prop.Enum, ","); got != "Go,Rust" {
		t.Fatalf("enum = %#v, want Go,Rust", prop.Enum)
	}
	if len(prop.OneOf) != 0 {
		t.Fatalf("oneOf written for options without descriptions: %#v", prop.OneOf)
	}
}

func TestQuestionElicitationUsesTitledChoicesWhenAnOptionDescribesItself(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Question: "Pick a stack",
		Options: []acp.QuestionOption{
			{Label: "Go", Description: "statically typed"},
			{Label: "Rust"},
		},
		Custom: false,
	}))
	prop := req.RequestedSchema.Properties["q1"]
	if len(prop.Enum) != 0 {
		t.Fatalf("enum written where descriptions call for oneOf: %#v", prop.Enum)
	}
	if len(prop.OneOf) != 2 || prop.OneOf[0].Const != "Go" || prop.OneOf[0].Description != "statically typed" {
		t.Fatalf("oneOf = %#v, want the titled options", prop.OneOf)
	}
}

// A question that allows the user's own answer is a free-text field: a form
// cannot offer a picker and an escape from it at once, and the tool's contract
// is that an answer matching no option label is what the user typed. The
// labels stay on screen as suggestions.
func TestQuestionElicitationOffersFreeTextWhenTheAnswerMayBeTyped(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Question: "Pick a stack",
		Options:  []acp.QuestionOption{{Label: "Go"}, {Label: "Rust"}},
		Custom:   true,
	}))
	prop := req.RequestedSchema.Properties["q1"]
	if prop.Type != "string" {
		t.Fatalf("type = %q, want string", prop.Type)
	}
	if len(prop.Enum) != 0 || len(prop.OneOf) != 0 {
		t.Fatalf("a free-text field must not be a picker: %#v", prop)
	}
	if !strings.Contains(prop.Description, "Go") || !strings.Contains(prop.Description, "Rust") {
		t.Fatalf("description does not offer the options: %q", prop.Description)
	}
}

func TestQuestionElicitationMapsMultipleToAMultiSelect(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Question: "Pick the tools",
		Options:  []acp.QuestionOption{{Label: "grep"}, {Label: "glob"}},
		Multiple: true,
		Custom:   false,
	}))
	prop := req.RequestedSchema.Properties["q1"]
	if prop.Type != "array" {
		t.Fatalf("type = %q, want array", prop.Type)
	}
	if prop.Items == nil || prop.Items.Type != "string" {
		t.Fatalf("items = %#v, want a string item schema", prop.Items)
	}
	if got := strings.Join(prop.Items.Enum, ","); got != "grep,glob" {
		t.Fatalf("items.enum = %#v, want grep,glob", prop.Items.Enum)
	}
}

// Titled multi-select items have the MCP shape ACP adopts, {"anyOf": [...]}
// with no "type": a "type":"string" beside them names the untitled variant,
// whose required "enum" is missing, and a strict client refuses the form.
func TestQuestionElicitationWritesTitledMultiSelectItemsWithoutAType(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Question: "Pick the tools",
		Options:  []acp.QuestionOption{{Label: "grep", Description: "search text"}, {Label: "glob"}},
		Multiple: true,
	}))
	raw, err := json.Marshal(req.RequestedSchema.Properties["q1"])
	if err != nil {
		t.Fatal(err)
	}
	var prop struct {
		Type  string                     `json:"type"`
		Items map[string]json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(raw, &prop); err != nil {
		t.Fatal(err)
	}
	if prop.Type != "array" {
		t.Fatalf("type = %q, want array in %s", prop.Type, raw)
	}
	if _, ok := prop.Items["type"]; ok {
		t.Fatalf("titled items carry a type: %s", raw)
	}
	if _, ok := prop.Items["anyOf"]; !ok {
		t.Fatalf("titled items lack anyOf: %s", raw)
	}
	if _, ok := prop.Items["enum"]; ok {
		t.Fatalf("titled items carry an enum too: %s", raw)
	}
}

func TestQuestionElicitationTitlesAFieldFromItsHeader(t *testing.T) {
	req := acp.QuestionElicitation(questionParams(acp.QuestionPrompt{
		Header:   "Stack",
		Question: "Which one?",
		Options:  []acp.QuestionOption{{Label: "Go"}},
		Custom:   false,
	}))
	prop := req.RequestedSchema.Properties["q1"]
	if prop.Title != "Stack" {
		t.Fatalf("title = %q, want the header", prop.Title)
	}
	if prop.Description != "Which one?" {
		t.Fatalf("description = %q, want the question text", prop.Description)
	}
	if req.Message != "Which one?" {
		t.Fatalf("message = %q, want the question text", req.Message)
	}
}

func TestElicitationAnswersReadsTheFormBack(t *testing.T) {
	questions := []acp.QuestionPrompt{
		{Question: "Pick a stack", Options: []acp.QuestionOption{{Label: "Go"}}, Custom: true},
		{Question: "Pick the tools", Options: []acp.QuestionOption{{Label: "grep"}, {Label: "glob"}}, Multiple: true, Custom: false},
	}
	res := &acp.CreateElicitationResult{
		Action: acp.ElicitationAccept,
		Content: map[string]acp.ElicitationContentValue{
			"q1": {Values: []string{"Something else"}},
			"q2": {Values: []string{"grep", "glob"}},
		},
	}
	got := acp.ElicitationAnswers(questions, res)
	if len(got.Answers) != 2 {
		t.Fatalf("answers = %#v, want one entry per question", got.Answers)
	}
	if len(got.Answers[0]) != 1 || got.Answers[0][0] != "Something else" {
		t.Fatalf("typed answer lost: %#v", got.Answers[0])
	}
	if len(got.Answers[1]) != 2 {
		t.Fatalf("multi-select answer lost: %#v", got.Answers[1])
	}
}

// ACP asks the agent to validate an accepted form again: a closed question
// answers only with the options it offered, whatever a client let through.
func TestElicitationAnswersDropsAPickNoOptionOffers(t *testing.T) {
	questions := []acp.QuestionPrompt{
		{Question: "Pick a stack", Options: []acp.QuestionOption{{Label: "Go"}, {Label: "Rust"}}, Custom: false},
		{Question: "Pick the tools", Options: []acp.QuestionOption{{Label: "grep"}, {Label: "glob"}}, Multiple: true, Custom: false},
	}
	res := &acp.CreateElicitationResult{
		Action: acp.ElicitationAccept,
		Content: map[string]acp.ElicitationContentValue{
			"q1": {Values: []string{"Python"}},
			"q2": {Values: []string{"grep", "sed"}},
		},
	}
	got := acp.ElicitationAnswers(questions, res)
	if len(got.Answers) != 2 {
		t.Fatalf("answers = %#v, want one entry per question", got.Answers)
	}
	if len(got.Answers[0]) != 0 {
		t.Fatalf("a closed picker passed an unoffered answer: %#v", got.Answers[0])
	}
	if strings.Join(got.Answers[1], ",") != "grep" {
		t.Fatalf("closed multi-select = %#v, want only the offered grep", got.Answers[1])
	}
}

// A multi-select whose question allows the user's own words keeps that way to
// answer: an optional text field beside the choices, whose text joins the
// picks as the answer that matches no option label.
func TestQuestionElicitationOffersOwnWordsBesideAMultiSelect(t *testing.T) {
	q := acp.QuestionPrompt{
		Question: "Pick the tools",
		Options:  []acp.QuestionOption{{Label: "grep"}, {Label: "glob"}},
		Multiple: true,
		Custom:   true,
	}
	req := acp.QuestionElicitation(questionParams(q))
	if req.RequestedSchema.Properties["q1"].Type != "array" {
		t.Fatalf("q1 = %#v, want the multi-select", req.RequestedSchema.Properties["q1"])
	}
	other, ok := req.RequestedSchema.Properties["q1_other"]
	if !ok || other.Type != "string" {
		t.Fatalf("properties = %#v, want a q1_other text field", req.RequestedSchema.Properties)
	}
	for _, name := range req.RequestedSchema.Required {
		if name == "q1_other" {
			t.Fatalf("the own-words field is required: %#v", req.RequestedSchema.Required)
		}
	}

	got := acp.ElicitationAnswers([]acp.QuestionPrompt{q}, &acp.CreateElicitationResult{
		Action: acp.ElicitationAccept,
		Content: map[string]acp.ElicitationContentValue{
			"q1":       {Values: []string{"grep"}},
			"q1_other": {Values: []string{" ripgrep "}},
		},
	})
	if strings.Join(got.Answers[0], ",") != "grep,ripgrep" {
		t.Fatalf("answer = %#v, want the pick and the typed text", got.Answers[0])
	}
}

// Fields are listed in the order the questions were asked, which is the order
// a client renders them in, also past nine questions where "q10" would sort
// before "q2".
func TestQuestionElicitationKeepsTheAskedOrderPastNineQuestions(t *testing.T) {
	var questions []acp.QuestionPrompt
	for i := 0; i < 11; i++ {
		questions = append(questions, acp.QuestionPrompt{Question: fmt.Sprintf("Question %d", i+1), Custom: true})
	}
	req := acp.QuestionElicitation(questionParams(questions...))
	raw, err := json.Marshal(req.RequestedSchema)
	if err != nil {
		t.Fatal(err)
	}
	var schema struct {
		Properties json.RawMessage `json:"properties"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(schema.Properties))
	if _, err := dec.Token(); err != nil {
		t.Fatal(err)
	}
	var titles []string
	for dec.More() {
		if _, err := dec.Token(); err != nil {
			t.Fatal(err)
		}
		var prop acp.ElicitationPropertySchema
		if err := dec.Decode(&prop); err != nil {
			t.Fatal(err)
		}
		titles = append(titles, prop.Title)
	}
	for i, title := range titles {
		if want := fmt.Sprintf("Question %d", i+1); title != want {
			t.Fatalf("field %d is %q, want %q (order %v)", i, title, want, titles)
		}
	}
	answers := acp.ElicitationAnswers(questions, &acp.CreateElicitationResult{
		Action: acp.ElicitationAccept,
		Content: map[string]acp.ElicitationContentValue{
			req.RequestedSchema.Required[10]: {Values: []string{"eleventh"}},
		},
	})
	if len(answers.Answers) != 11 || strings.Join(answers.Answers[10], "") != "eleventh" {
		t.Fatalf("answers = %#v, want the eleventh answer back in place", answers.Answers)
	}
}

func TestElicitationAnswersOfADeclinedFormIsEmpty(t *testing.T) {
	questions := []acp.QuestionPrompt{{Question: "Pick a stack", Options: []acp.QuestionOption{{Label: "Go"}}}}
	for _, action := range []string{acp.ElicitationDecline, acp.ElicitationCancel} {
		got := acp.ElicitationAnswers(questions, &acp.CreateElicitationResult{Action: action})
		if len(got.Answers) != 0 {
			t.Fatalf("%s answered with %#v, want none", action, got.Answers)
		}
	}
}

// What the user picks comes back the way the form collected it: a single value
// for a picker, one entry per selection for a multi-select, and whatever a
// control Coddy never asked for produced, so nothing a user typed is dropped.
func TestElicitationContentValueReadsEveryControl(t *testing.T) {
	cases := []struct {
		name string
		json string
		want []string
	}{
		{"string", `"Go"`, []string{"Go"}},
		{"array", `["a","b"]`, []string{"a", "b"}},
		{"number", `3.5`, []string{"3.5"}},
		{"integer as written", `1000000`, []string{"1000000"}},
		{"boolean", `true`, []string{"true"}},
		{"null", `null`, nil},
		{"array of primitives", `[1, true, "x", null]`, []string{"1", "true", "x"}},
		{"object as its JSON text", `{"a": 1}`, []string{`{"a":1}`}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var v acp.ElicitationContentValue
			if err := json.Unmarshal([]byte(tc.json), &v); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if strings.Join(v.Values, "|") != strings.Join(tc.want, "|") {
				t.Fatalf("values = %#v, want %#v", v.Values, tc.want)
			}
		})
	}
}

func TestElicitationContentValueMarshalsTheWayItWasCollected(t *testing.T) {
	one, err := json.Marshal(acp.ElicitationContentValue{Values: []string{"Go"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(one) != `"Go"` {
		t.Fatalf("single value = %s, want a bare string", one)
	}
	many, err := json.Marshal(acp.ElicitationContentValue{Values: []string{"a", "b"}})
	if err != nil {
		t.Fatal(err)
	}
	if string(many) != `["a","b"]` {
		t.Fatalf("several values = %s, want an array", many)
	}
}
