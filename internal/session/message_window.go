package session

import "github.com/EvilFreelancer/coddy-agent/internal/llm"

// MessagePageQuery names a window of a session's history for a paged read
// (GET /coddy/sessions/{id}/messages with limit, before or from). A client
// that holds only the tail of a long history reads it through these: the
// newest page first, then older pages ending where its window starts, and
// its own window again with From when a turn has ended.
type MessagePageQuery struct {
	// From is the message index the page starts at, running to Before or the
	// end of the history; negative when unset. It excludes Limit.
	From int
	// Before is the message index the page ends at, exclusive; negative when
	// unset (the end of the history).
	Before int
	// Limit is about how many messages the page holds, ending at Before; zero
	// when unset.
	Limit int
}

// MessagePage is the slice of the history a paged read returns, with the
// counts a client numbers the page's rows from so they match a full read.
type MessagePage struct {
	// Offset is the index of the first message of the page, End the index
	// after its last one, Total the length of the history.
	Offset int
	End    int
	Total  int
	// TurnsBefore counts the user-role messages before Offset that are not
	// compaction summaries: the numbering a rewind's user message index uses
	// (nthUserMessageIndex), so a client adds it to the position of a prompt
	// inside the page.
	TurnsBefore int
	// UserRowsBefore counts every user-role message before Offset: the
	// numbering UILogEntry.UserTurnIndex uses (CountUserTurns).
	UserRowsBefore int
}

// PageMessages returns the window of msgs a paged read names. Without From,
// Before or Limit it is the whole history. Positions past the history are
// clamped to its end.
//
// A page never splits a tool step: a start or an end that falls on a tool
// result moves back to the assistant message that issued the call, so a
// result always arrives with its call and a page joins the one after it
// without a gap or an overlap. With Limit, the start moves back to the
// nearest prompt when one lies within half a page, so a page usually opens
// with the message that started its turn; a very long turn is still cut, at
// the step the cut falls in. A page is never empty while messages remain
// before its end.
func PageMessages(msgs []llm.Message, q MessagePageQuery) MessagePage {
	total := len(msgs)
	end := total
	if q.Before >= 0 && q.Before < total {
		end = stepStart(msgs, q.Before)
	}
	start := 0
	switch {
	case q.From >= 0:
		start = stepStart(msgs, min(q.From, end))
	case q.Limit > 0:
		start = pageStart(msgs, end, q.Limit)
	}
	page := MessagePage{Offset: start, End: end, Total: total}
	for _, m := range msgs[:start] {
		if m.Role != llm.RoleUser {
			continue
		}
		page.UserRowsBefore++
		if !m.CompactionSummary {
			page.TurnsBefore++
		}
	}
	return page
}

// pageStart picks where a page of about limit messages ending at end starts.
func pageStart(msgs []llm.Message, end, limit int) int {
	cut := end - limit
	if cut <= 0 {
		return 0
	}
	for i := cut; i >= 0 && i >= cut-limit/2; i-- {
		if opensTurn(msgs[i]) {
			return i
		}
	}
	return stepStart(msgs, cut)
}

// stepStart moves i back over tool results to the message that issued the
// calls, so i never names the middle of a tool step.
func stepStart(msgs []llm.Message, i int) int {
	for i > 0 && i < len(msgs) && msgs[i].Role == llm.RoleTool {
		i--
	}
	return i
}

// opensTurn reports whether m is the message a turn starts with: a user-role
// message that is not a compaction summary (a background wake is one).
func opensTurn(m llm.Message) bool {
	return m.Role == llm.RoleUser && !m.CompactionSummary
}

// UILog returns the entries of log that belong to the page. An entry stamped
// with user turn t sits right before the t-th user-role message (0-based) -
// the end of the turn it was logged in - or at the end of the history when
// there is no such message. It belongs to the page holding that message, so
// an entry on the boundary between two pages opens the newer one: a client
// showing only the newest page still sees what ended the turn before it, and
// no entry is served by two pages. The whole history keeps every entry.
func (p MessagePage) UILog(msgs []llm.Message, log []UILogEntry) []UILogEntry {
	if len(log) == 0 {
		return nil
	}
	var userRows []int
	for i, m := range msgs {
		if m.Role == llm.RoleUser {
			userRows = append(userRows, i)
		}
	}
	out := make([]UILogEntry, 0, len(log))
	for _, e := range log {
		t := max(e.UserTurnIndex, 1)
		if t >= len(userRows) {
			// After the last prompt: the end of the history.
			if p.End == p.Total {
				out = append(out, e)
			}
			continue
		}
		if pos := userRows[t]; p.Offset <= pos && pos < p.End {
			out = append(out, e)
		}
	}
	return out
}
