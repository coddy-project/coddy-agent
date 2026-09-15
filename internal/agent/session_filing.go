package agent

import (
	"fmt"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The hooks behind the session_describe tool. They are the only place where the
// tool's vocabulary meets the session's own: the tool parses arguments and
// reports what happened, the session owns the folding and the storage, and this
// file decides what an update means.

// sessionFilingOf reports how the session is filed right now. The title is the
// effective one - the pinned title when there is one, and otherwise the phrase
// derived from the first message - because that is what a list shows and what
// the model has to judge before renaming anything.
func sessionFilingOf(st SessionState) tooling.SessionFiling {
	return tooling.SessionFiling{Title: st.ConversationTitle(), Tags: st.GetTags()}
}

// applySessionFiling writes the parts of upd that are set and answers with what
// the session carries afterwards. It validates before it writes anything, so a
// call that names a title too long for a list row changes neither the title nor
// the tags: a half-applied filing is harder to reason about than a refused one.
func applySessionFiling(st SessionState, upd tooling.SessionFilingUpdate) (tooling.SessionFiling, error) {
	var title string
	if upd.Title != nil {
		title = session.NormalizeTitle(*upd.Title)
		if length, tooLong := session.TitleTooLong(title); tooLong {
			return sessionFilingOf(st), fmt.Errorf(
				"the title is %d characters long; a session title is a row of a list, keep it under %d",
				length, session.MaxSessionTitleRunes)
		}
	}

	if upd.Title != nil {
		// An empty title is not a blank row: it drops the pin and the session
		// goes back to the phrase its first message gave it.
		st.SetTitlePinned(title)
	}
	switch {
	case upd.Tags != nil:
		st.SetTags(*upd.Tags)
	case len(upd.AddTags) > 0 || len(upd.RemoveTags) > 0:
		st.SetTags(session.MergeTags(st.GetTags(), upd.AddTags, upd.RemoveTags))
	}
	return sessionFilingOf(st), nil
}
