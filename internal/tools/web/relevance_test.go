package web

import "testing"

// rowsAbout builds a batch of results whose text is the given subject.
func rowsAbout(n int, title, url, snippet string) []Result {
	out := make([]Result, 0, n)
	for i := 0; i < n; i++ {
		out = append(out, Result{Title: title, URL: url, Snippet: snippet})
	}
	return out
}

// TestDecoyGateCatchesTheMeasuredBingDecoys uses the result sets Bing was
// actually observed serving for unrelated queries.
func TestDecoyGateCatchesTheMeasuredBingDecoys(t *testing.T) {
	cases := []struct {
		name  string
		query string
		rows  []Result
	}{
		{
			name:  "chinese pizza pages for a Go query",
			query: "golang context cancellation best practices",
			rows: []Result{
				{Title: "披萨馅料有哪些经典的种类？", URL: "https://www.zhihu.com/question/27157954"},
				{Title: "乌兹别克斯坦是一个怎么样的国家？", URL: "https://www.zhihu.com/question/60608232"},
				{Title: "如何评价这个问题？", URL: "https://www.zhihu.com/question/1"},
				{Title: "为什么有人喜欢吃辣？", URL: "https://www.zhihu.com/question/2"},
				{Title: "旅行时最难忘的一顿饭", URL: "https://www.zhihu.com/question/3"},
			},
		},
		{
			name:  "tourism pages for a repository query",
			query: "coddy agent EvilFreelancer github",
			rows: []Result{
				{Title: "Visit Rainier | Official Site Of Mt. Rainier Tourism", URL: "https://visitrainier.com/", Snippet: "Plan your trip."},
				{Title: "The Best Mt. Rainier Scenic Drives", URL: "https://visitrainier.com/driving-tours/", Snippet: "Scenic routes."},
				{Title: "Where to stay near the mountain", URL: "https://visitrainier.com/lodging/", Snippet: "Hotels and cabins."},
				{Title: "Wildflower season on the mountain", URL: "https://visitrainier.com/wildflowers/", Snippet: "When to visit."},
				{Title: "Camping permits and reservations", URL: "https://visitrainier.com/camping/", Snippet: "Book a site."},
			},
		},
		{
			name:  "french windows help for a Russian query",
			query: "что такое ReAct агент",
			rows: []Result{
				{Title: "Explorateur de fichiers dans Windows", URL: "https://support.microsoft.com/fr-fr/windows/1"},
				{Title: "Reparer l'Explorateur de fichiers", URL: "https://support.microsoft.com/fr-fr/windows/2"},
				{Title: "Maitriser l'Explorateur de fichiers", URL: "https://support.microsoft.com/fr-fr/windows/3"},
				{Title: "Ouvrir l'explorateur avec un raccourci", URL: "https://support.microsoft.com/fr-fr/windows/4"},
				{Title: "Personnaliser l'affichage des dossiers", URL: "https://support.microsoft.com/fr-fr/windows/5"},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, isDecoy := decoyReason(tc.query, tc.rows); !isDecoy {
				t.Fatalf("decoy not detected for %q", tc.query)
			}
		})
	}
}

// TestDecoyGateKeepsGenuineAnswers is the other half: the gate must not fire on
// a real result set. The Go case is the batch a clean egress IP returned for
// the same query the decoys above answered.
func TestDecoyGateKeepsGenuineAnswers(t *testing.T) {
	cases := []struct {
		name  string
		query string
		rows  []Result
	}{
		{
			name:  "measured genuine Go answer",
			query: "golang context cancellation",
			rows: []Result{
				{Title: "The Go Programming Language", URL: "https://go.dev/"},
				{Title: "Download and install - The Go Programming Language", URL: "https://go.dev/doc/install"},
				{Title: "GitHub - golang/go: The Go programming language", URL: "https://github.com/golang/go"},
				{Title: "Go (programming language) - Wikipedia", URL: "https://en.wikipedia.org/wiki/Go_(programming_language)"},
			},
		},
		{
			name:  "an abbreviation the results spell out",
			query: "K8s pod OOM",
			rows: []Result{
				{Title: "Kubernetes 1.29 release notes", URL: "https://kubernetes.io/blog/releases"},
				{Title: "Troubleshooting OOMKilled containers", URL: "https://cncf.io/oomkilled"},
				{Title: "Kubernetes resource management", URL: "https://kubernetes.io/docs/resources"},
				{Title: "Requests and limits explained", URL: "https://kubernetes.io/docs/limits"},
				{Title: "Debugging evicted workloads", URL: "https://example.com/evicted"},
			},
		},
		{
			name:  "an acronym the results expand",
			query: "RAII in C++",
			rows: []Result{
				{Title: "Resource Acquisition Is Initialization", URL: "https://en.cppreference.com/w/cpp/language/raii"},
				{Title: "Scope-based resource management", URL: "https://isocpp.org/wiki/faq/resource"},
				{Title: "Smart pointers and ownership", URL: "https://learn.microsoft.com/cpp/smart-pointers"},
				{Title: "Destructors and cleanup", URL: "https://example.com/dtor"},
				{Title: "Exception safety guarantees", URL: "https://example.com/exceptions"},
			},
		},
		{
			name:  "a Russian verb answered by the noun",
			query: "как отменить подписку",
			rows: []Result{
				{Title: "Отмена подписки в личном кабинете", URL: "https://example.ru/otmena"},
				{Title: "Управление подписками", URL: "https://example.ru/subs"},
				{Title: "Возврат средств за подписку", URL: "https://example.ru/refund"},
				{Title: "Условия обслуживания", URL: "https://example.ru/terms"},
				{Title: "Служба поддержки", URL: "https://example.ru/help"},
			},
		},
		{
			name:  "only one row matches and that is enough",
			query: "golang context cancellation",
			rows: []Result{
				{Title: "Terminating programs", URL: "https://example.com/a"},
				{Title: "Ending a routine", URL: "https://example.com/b"},
				{Title: "How to cancel a running task in Go", URL: "https://example.com/c"},
			},
		},
		{
			name:  "the match is in the url",
			query: "kubernetes operator pattern",
			rows: []Result{
				{Title: "Extending the API", URL: "https://kubernetes.io/docs/concepts/extend/"},
				{Title: "Writing controllers", URL: "https://example.com/controllers"},
				{Title: "Custom resources", URL: "https://example.com/crd"},
			},
		},
		{
			name:  "the match survives a different word ending",
			query: "golang context cancellation",
			rows: []Result{
				{Title: "How to cancel work in Go", URL: "https://example.com/a"},
				{Title: "Stopping a goroutine", URL: "https://example.com/b"},
				{Title: "Deadlines and timeouts", URL: "https://example.com/c"},
			},
		},
		{
			name:  "a Russian query answered by Russian pages",
			query: "что такое ReAct агент",
			rows: []Result{
				{Title: "Агенты на основе ReAct", URL: "https://example.ru/react"},
				{Title: "Как работает агент", URL: "https://example.ru/agent"},
				{Title: "Обзор подходов", URL: "https://example.ru/overview", Snippet: "ReAct и другие схемы."},
			},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if reason, isDecoy := decoyReason(tc.query, tc.rows); isDecoy {
				t.Fatalf("genuine answer discarded as %q", reason)
			}
		})
	}
}

// TestDecoyGateStaysOutOfTheWayOnSmallBatches: a short answer can plausibly
// miss every query word, so the gate only judges a batch big enough to be a
// result page.
func TestDecoyGateStaysOutOfTheWayOnSmallBatches(t *testing.T) {
	rows := rowsAbout(decoyMinRows-1, "Entirely unrelated", "https://example.com/x", "nothing to do with it")
	if _, isDecoy := decoyReason("golang context cancellation", rows); isDecoy {
		t.Fatal("the gate must not judge a batch smaller than decoyMinRows")
	}
}

func TestDecoyGateIgnoresAQueryOfOnlyStopwords(t *testing.T) {
	rows := rowsAbout(5, "Something", "https://example.com/x", "anything")
	if _, isDecoy := decoyReason("how what why the and", rows); isDecoy {
		t.Fatal("a query with no content words cannot convict an engine")
	}
}

func TestSearchTokensDropsShortAndCommonWords(t *testing.T) {
	got := searchTokens("How to use the Go context API")
	for _, unwanted := range []string{"how", "the", "to", "use"} {
		if got[unwanted] {
			t.Errorf("%q should have been dropped", unwanted)
		}
	}
	for _, wanted := range []string{"context", "api"} {
		if !got[wanted] {
			t.Errorf("%q should have been kept, got %v", wanted, got)
		}
	}
}

func TestSearchTokensSplitsCJKPerCharacter(t *testing.T) {
	got := searchTokens("披萨馅料")
	if !got["披"] || !got["萨"] {
		t.Fatalf("CJK text must tokenize per character, got %v", got)
	}
}

func TestStemFoldsCommonEndings(t *testing.T) {
	for _, tc := range []struct{ in, want string }{
		{"cancellation", "cancel"},
		{"running", "run"},
		{"queries", "query"},
		{"tests", "test"},
		{"go", "go"},
		{"api", "api"},
		// Never shortened below three runes, where unrelated words collide.
		{"ping", "ping"},
		{"uses", "use"},
	} {
		if got := stem(tc.in); got != tc.want {
			t.Errorf("stem(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestStemMeetsTheWordItCameFrom is the property the gate actually depends on:
// two forms of one word must produce the same token, or a genuine answer is
// convicted as a decoy.
func TestStemMeetsTheWordItCameFrom(t *testing.T) {
	for _, pair := range [][2]string{
		{"cancellation", "cancel"},
		{"running", "run"},
		{"queries", "query"},
		{"tests", "test"},
		{"cancelled", "cancel"},
		{"routines", "routine"},
	} {
		if stem(pair[0]) != stem(pair[1]) {
			t.Errorf("%q and %q stem apart: %q vs %q", pair[0], pair[1], stem(pair[0]), stem(pair[1]))
		}
	}
}
