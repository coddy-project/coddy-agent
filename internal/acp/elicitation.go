package acp

import (
	"fmt"
	"strings"
)

// QuestionElicitation turns the question tool's payload into the elicitation
// form that asks it: one form field per question, in the order the questions
// were asked, under the names q1, q2 and so on. The form is what a client that
// implements elicitation renders instead of the Coddy-only
// session/request_question.
//
// How a question becomes a field:
//
//   - a question that allows the user's own answer (Custom, the default) is a
//     free-text string, with the offered option labels listed in its
//     description as suggestions. That keeps the contract the model relies on:
//     an answer that matches no option label is the text the user typed.
//   - a question that must be answered with one of its options is a
//     single-select string, `enum` when no option carries a description and
//     `oneOf` when one does, so the client can show the descriptions.
//   - a question that allows several answers (Multiple) is a multi-select
//     array carrying the same choice set as its items.
func QuestionElicitation(params QuestionRequestParams) CreateElicitationRequest {
	props := make(map[string]ElicitationPropertySchema, len(params.Questions))
	required := make([]string, 0, len(params.Questions))
	for i, q := range params.Questions {
		name := elicitationFieldName(i)
		props[name] = elicitationProperty(q)
		required = append(required, name)
	}
	req := CreateElicitationRequest{
		SessionID:  params.SessionID,
		ToolCallID: params.ToolCallID,
		Mode:       ElicitationModeForm,
		Message:    elicitationMessage(params.Questions),
		RequestedSchema: &ElicitationSchema{
			Type:       "object",
			Title:      elicitationFormTitle(params.Questions),
			Properties: props,
			Required:   required,
		},
	}
	return req
}

// ElicitationAnswers reads the client's form answer back into the shape the
// question tool returns to the model: one entry per question, each the labels
// the user picked or typed. A declined or cancelled form answers with no
// entries at all.
func ElicitationAnswers(questions []QuestionPrompt, res *CreateElicitationResult) *QuestionResult {
	out := &QuestionResult{}
	if res == nil {
		return out
	}
	if res.Action != ElicitationAccept {
		return out
	}
	out.Answers = make([][]string, len(questions))
	for i := range questions {
		value, ok := res.Content[elicitationFieldName(i)]
		if !ok {
			out.Answers[i] = []string{}
			continue
		}
		if len(value.Values) == 0 {
			out.Answers[i] = []string{}
			continue
		}
		out.Answers[i] = append([]string(nil), value.Values...)
	}
	return out
}

// elicitationFieldName is the form property a question is asked under. The
// names are positional and stable, so the answer maps back without guessing.
func elicitationFieldName(index int) string {
	return fmt.Sprintf("q%d", index+1)
}

// elicitationProperty builds one form field out of a question.
func elicitationProperty(q QuestionPrompt) ElicitationPropertySchema {
	prop := ElicitationPropertySchema{
		Title: elicitationFieldTitle(q),
	}
	// A header stands in for the question text as the field's label, so the
	// question itself has to ride along as the description or the prompt the
	// user is answering is lost.
	if strings.TrimSpace(q.Header) != "" {
		prop.Description = strings.TrimSpace(q.Question)
	}

	labels, titled := elicitationChoices(q.Options)
	switch {
	case q.Multiple:
		prop.Type = "array"
		items := &MultiSelectItems{Type: "string"}
		if titled {
			items.AnyOf = labels
		} else {
			items.Enum = elicitationConsts(labels)
		}
		prop.Items = items
	case len(q.Options) > 0 && !q.Custom:
		prop.Type = "string"
		if titled {
			prop.OneOf = labels
		} else {
			prop.Enum = elicitationConsts(labels)
		}
	default:
		// Free text: the user may answer with one of the offered labels or
		// with words of their own, and a form cannot offer both a picker and
		// an escape from it. The labels go into the description as
		// suggestions so the choice is still on screen.
		prop.Type = "string"
		if hint := elicitationOptionHint(q.Options); hint != "" {
			prop.Description = strings.TrimSpace(strings.TrimSpace(prop.Description) + "\n\n" + hint)
		}
	}
	return prop
}

// elicitationFieldTitle is the label a field is listed under: the question's
// own header when it has one, else the question text.
func elicitationFieldTitle(q QuestionPrompt) string {
	if h := strings.TrimSpace(q.Header); h != "" {
		return h
	}
	return strings.TrimSpace(q.Question)
}

// elicitationMessage is the prompt over the form: the question being asked,
// which is the first one's text when a single call asked several.
func elicitationMessage(questions []QuestionPrompt) string {
	for _, q := range questions {
		if t := strings.TrimSpace(q.Question); t != "" {
			return t
		}
	}
	return "Answer the form"
}

// elicitationFormTitle is the title of the form itself: the first header any
// question carries, which names what the whole form is about.
func elicitationFormTitle(questions []QuestionPrompt) string {
	for _, q := range questions {
		if h := strings.TrimSpace(q.Header); h != "" {
			return h
		}
	}
	return ""
}

// elicitationChoices normalises the offered options into the titled form the
// schema writes, and reports whether any of them carries a description - which
// is what decides between a plain enum and a titled oneOf.
func elicitationChoices(options []QuestionOption) ([]EnumOption, bool) {
	out := make([]EnumOption, 0, len(options))
	titled := false
	for _, o := range options {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			continue
		}
		desc := strings.TrimSpace(o.Description)
		if desc != "" {
			titled = true
		}
		out = append(out, EnumOption{Const: label, Title: label, Description: desc})
	}
	return out, titled
}

// elicitationConsts is the plain-label form of a choice set, for a question
// whose options carry no descriptions.
func elicitationConsts(labels []EnumOption) []string {
	out := make([]string, 0, len(labels))
	for _, l := range labels {
		out = append(out, l.Const)
	}
	return out
}

// elicitationOptionHint lists the offered labels in a free-text field's
// description, so a form that shows only a text box still shows what the
// model offered to pick from.
func elicitationOptionHint(options []QuestionOption) string {
	labels := make([]string, 0, len(options))
	for _, o := range options {
		if label := strings.TrimSpace(o.Label); label != "" {
			labels = append(labels, label)
		}
	}
	if len(labels) == 0 {
		return ""
	}
	return "Options: " + strings.Join(labels, ", ")
}
