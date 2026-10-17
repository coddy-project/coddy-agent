package session

import "testing"

func TestUserMessageTextNamesTheFilesAMessageCameWith(t *testing.T) {
	note := func(lines ...string) string {
		s := "\n\n<coddy_session_assets>Uploaded files saved to session assets (read-only). You can read or copy them:\n"
		for _, l := range lines {
			s += l + "\n"
		}
		return s + "</coddy_session_assets>"
	}
	cases := []struct{ name, content, want string }{
		{
			"a console prompt keeps its own markers",
			"[image #1] what is circled here?" + note("- /h/sessions/s/assets/pasted-1.png"),
			"[image #1] what is circled here?",
		},
		{
			"pictures and a file sent from the web UI",
			"describe them" + note(
				"- /h/sessions/s/assets/pasted-1.png",
				"- /h/sessions/s/assets/shot_1.jpg (shot.jpg)",
				"- /h/sessions/s/assets/report.pdf",
			),
			"[image #1] [image #2] [file: report.pdf] describe them",
		},
		{
			"a picture alone",
			note("- /h/sessions/s/assets/photo.webp"),
			"[image #1]",
		},
		{
			"no files",
			"just text",
			"just text",
		},
	}
	for _, c := range cases {
		got := UserMessageText(c.content)
		if got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
		// A surface may apply it to text a replay already rendered.
		if again := UserMessageText(got); again != got {
			t.Errorf("%s: applied twice it gave %q", c.name, again)
		}
	}
	// The digest a model reads keeps the plain display text.
	if got := UserMessageDisplayText("describe them" + note("- /a/pasted-1.png")); got != "describe them" {
		t.Errorf("UserMessageDisplayText changed: %q", got)
	}
}
