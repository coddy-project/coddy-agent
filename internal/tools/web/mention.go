package web

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"mime"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/go-shiori/go-readability"

	"github.com/EvilFreelancer/coddy-agent/internal/mention"
)

// A page an "@https://..." mention names is read with webfetch's transport
// and address guard: public addresses only, every redirect vetted.
const (
	mentionFetchTimeout = 20 * time.Second
	// mentionFetchChars caps what one page puts into a message.
	mentionFetchChars = 64 << 10
)

func init() { mention.RegisterURLFetcher(FetchForMention) }

// FetchForMention reads a page for a URL mention: HTML through readability
// into Markdown, JSON pretty-printed, other text as it came, capped at
// mentionFetchChars. Anything else - an image, an archive - is refused with
// its content type, so the attachment can say what the link points at.
func FetchForMention(ctx context.Context, rawURL string) (string, error) {
	args, err := json.Marshal(map[string]interface{}{"url": rawURL, "timeout_seconds": int(mentionFetchTimeout / time.Second)})
	if err != nil {
		return "", err
	}
	req, err := ParseHTTPRequest(string(args), "")
	if err != nil {
		return "", err
	}
	tr, err := req.send(ctx, transferPolicy{
		timeout:         req.Timeout,
		followRedirects: true,
		guard:           fetchGuard,
		decompress:      true,
	})
	if err != nil {
		return "", err
	}
	defer tr.Close()
	resp := tr.resp
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("http %d", resp.StatusCode)
	}
	body, _, err := readLimited(resp.Body, maxFetchHTMLBytes)
	if err != nil {
		return "", err
	}
	mediaType, _, _ := mime.ParseMediaType(resp.Header.Get("Content-Type"))
	var out string
	switch {
	case mediaType == "application/json" || strings.HasSuffix(mediaType, "+json"):
		var pretty bytes.Buffer
		if json.Indent(&pretty, body, "", "  ") == nil {
			out = pretty.String()
		} else {
			out = string(body)
		}
	case mediaType == "" || mediaType == "text/html" || mediaType == "application/xhtml+xml":
		article, err := readability.FromReader(bytes.NewReader(body), resp.Request.URL)
		if err != nil {
			return "", fmt.Errorf("readability: %w", err)
		}
		html := strings.TrimSpace(article.Content)
		if html == "" {
			html = strings.TrimSpace(article.TextContent)
		}
		md, err := HTMLToMarkdown(html)
		if err != nil {
			return "", err
		}
		if title := strings.TrimSpace(article.Title); title != "" {
			md = "# " + title + "\n\n" + strings.TrimSpace(md)
		}
		out = strings.TrimSpace(md)
	case strings.HasPrefix(mediaType, "text/") || mediaType == "application/xml" || strings.HasSuffix(mediaType, "+xml") ||
		mediaType == "application/yaml" || mediaType == "application/x-yaml" || mediaType == "application/javascript":
		out = string(body)
	default:
		return "", fmt.Errorf("the page is %s, not text", mediaType)
	}
	if len(out) > mentionFetchChars {
		cut := out[:mentionFetchChars]
		for len(cut) > 0 && !utf8.ValidString(cut) {
			cut = cut[:len(cut)-1]
		}
		out = cut + "\n\n[cut: the page is longer than a mention inlines; fetch the rest with webfetch]"
	}
	return out, nil
}
