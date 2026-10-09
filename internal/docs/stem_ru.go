package docs

// stemRussian is the Snowball stemmer for Russian
// (https://snowballstem.org/algorithms/russian/stemmer.html): it removes the
// inflectional endings of a lower-case word with ё already folded to е, so
// "сессия", "сессии" and "сессиями" all become "сесс". Endings are removed
// only from RV, the part of the word after its first vowel; the derivational
// ending -ость only from R2.
func stemRussian(w string) string {
	word := []rune(w)
	pV, p2 := ruRegions(word)
	if pV >= len(word) {
		return w
	}

	// Step 1: a perfective gerund, or else an optional reflexive ending
	// followed by an adjectival, a verb or a noun ending.
	if e, ok := ruAmong(word, pV, ruPerfectiveGerund); ok && ruAllowed(word, e, pV) {
		word = word[:len(word)-len(e.s)]
	} else {
		if e, ok := ruAmong(word, pV, ruReflexive); ok {
			word = word[:len(word)-len(e.s)]
		}
		if e, ok := ruAmong(word, pV, ruAdjective); ok {
			word = word[:len(word)-len(e.s)]
			if p, ok := ruAmong(word, pV, ruParticiple); ok && ruAllowed(word, p, pV) {
				word = word[:len(word)-len(p.s)]
			}
		} else if e, ok := ruAmong(word, pV, ruVerb); ok && ruAllowed(word, e, pV) {
			word = word[:len(word)-len(e.s)]
		} else if e, ok := ruAmong(word, pV, ruNoun); ok {
			word = word[:len(word)-len(e.s)]
		}
	}

	// Step 2: a final и.
	if n := len(word); n-1 >= pV && word[n-1] == 'и' {
		word = word[:n-1]
	}

	// Step 3: the derivational ending, inside R2.
	if e, ok := ruAmong(word, pV, ruDerivational); ok && len(word)-len(e.s) >= p2 {
		word = word[:len(word)-len(e.s)]
	}

	// Step 4: a superlative ending with a doubled н undoubled after it, a
	// doubled н, or a soft sign.
	if e, ok := ruAmong(word, pV, ruTidy); ok {
		switch string(e.s) {
		case "ейш", "ейше":
			word = word[:len(word)-len(e.s)]
			if n := len(word); n-2 >= pV && word[n-1] == 'н' && word[n-2] == 'н' {
				word = word[:n-1]
			}
		case "н":
			if n := len(word); n-2 >= pV && word[n-2] == 'н' {
				word = word[:n-1]
			}
		case "ь":
			word = word[:len(word)-1]
		}
	}
	return string(word)
}

func ruVowel(r rune) bool {
	switch r {
	case 'а', 'е', 'и', 'о', 'у', 'ы', 'э', 'ю', 'я':
		return true
	}
	return false
}

// ruRegions returns the start of RV (after the first vowel) and of R2 (after
// the first non-vowel that follows a vowel, twice); a region that does not
// exist starts at the end of the word.
func ruRegions(word []rune) (pV, p2 int) {
	n := len(word)
	pV, p2 = n, n
	i := 0
	for i < n && !ruVowel(word[i]) {
		i++
	}
	if i == n {
		return
	}
	pV = i + 1
	j := pV
	for j < n && ruVowel(word[j]) {
		j++
	}
	if j == n {
		return
	}
	j++ // R1 starts here
	for j < n && !ruVowel(word[j]) {
		j++
	}
	if j == n {
		return
	}
	j++
	for j < n && ruVowel(word[j]) {
		j++
	}
	if j < n {
		p2 = j + 1
	}
	return
}

// ruEnding is one ending of a class; afterA marks the endings that count only
// after а or я, which stay with the stem.
type ruEnding struct {
	s      []rune
	afterA bool
}

// ruAmong finds the longest ending of the class the word ends with, inside the
// region that starts at limit.
func ruAmong(word []rune, limit int, class []ruEnding) (ruEnding, bool) {
	var best ruEnding
	found := false
	for _, e := range class {
		start := len(word) - len(e.s)
		if start < limit || (found && len(e.s) <= len(best.s)) {
			continue
		}
		if string(word[start:]) == string(e.s) {
			best, found = e, true
		}
	}
	return best, found
}

// ruAllowed reports whether an ending found by ruAmong may be removed: an
// afterA ending needs а or я before it, inside the region.
func ruAllowed(word []rune, e ruEnding, limit int) bool {
	if !e.afterA {
		return true
	}
	i := len(word) - len(e.s) - 1
	return i >= limit && (word[i] == 'а' || word[i] == 'я')
}

func ruClass(afterA []string, plain []string) []ruEnding {
	var out []ruEnding
	for _, s := range afterA {
		out = append(out, ruEnding{s: []rune(s), afterA: true})
	}
	for _, s := range plain {
		out = append(out, ruEnding{s: []rune(s)})
	}
	return out
}

var (
	ruPerfectiveGerund = ruClass(
		[]string{"в", "вши", "вшись"},
		[]string{"ив", "ивши", "ившись", "ыв", "ывши", "ывшись"})
	ruAdjective = ruClass(nil, []string{
		"ее", "ие", "ые", "ое", "ими", "ыми", "ей", "ий", "ый", "ой", "ем", "им", "ым", "ом",
		"его", "ого", "ему", "ому", "их", "ых", "ую", "юю", "ая", "яя", "ою", "ею"})
	ruParticiple = ruClass(
		[]string{"ем", "нн", "вш", "ющ", "щ"},
		[]string{"ивш", "ывш", "ующ"})
	ruReflexive = ruClass(nil, []string{"ся", "сь"})
	ruVerb      = ruClass(
		[]string{"ла", "на", "ете", "йте", "ли", "й", "л", "ем", "н", "ло", "но", "ет", "ют", "ны", "ть", "ешь", "нно"},
		[]string{"ила", "ыла", "ена", "ейте", "уйте", "ите", "или", "ыли", "ей", "уй", "ил", "ыл", "им", "ым", "ен",
			"ило", "ыло", "ено", "ят", "ует", "уют", "ит", "ыт", "ены", "ить", "ыть", "ишь", "ую", "ю"})
	ruNoun = ruClass(nil, []string{
		"а", "ев", "ов", "ие", "ье", "е", "иями", "ями", "ами", "еи", "ии", "и", "ией", "ей", "ой", "ий", "й",
		"иям", "ям", "ием", "ем", "ам", "ом", "о", "у", "ах", "иях", "ях", "ы", "ь", "ию", "ью", "ю", "ия", "ья", "я"})
	ruDerivational = ruClass(nil, []string{"ост", "ость"})
	ruTidy         = ruClass(nil, []string{"ейш", "ейше", "н", "ь"})
)

// ruStopwords are the Russian function words a search of the documentation
// ignores, written with е for ё as the tokenizer folds them.
var ruStopwords = []string{
	"и", "в", "во", "не", "на", "с", "со", "как", "а", "то", "но", "да", "к", "ко", "у", "же", "за", "бы",
	"по", "от", "о", "об", "обо", "из", "до", "для", "при", "без", "под", "над", "про", "через", "или",
	"ли", "если", "это", "этот", "эта", "эти", "этого", "этой", "этом", "что", "чтобы", "где", "когда",
	"так", "уже", "еще", "его", "ее", "их", "он", "она", "оно", "они", "мы", "вы", "я", "ты", "там",
	"тут", "ни", "нибудь", "тоже", "также",
}
