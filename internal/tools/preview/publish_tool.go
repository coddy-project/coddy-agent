package preview

// preview_publish and preview_unpublish: the agent's half of the
// published-preview channel.
//
// preview_server hands the operator a loopback address and keeps reading the
// project off the disk, which is right while the two of them are iterating.
// These two are for the other situation: Coddy runs on a server behind an
// authenticated HTTPS edge, and the work has to reach somebody through that
// edge. Publishing copies the directory into the artifact root the operator
// configured, where the static origin serves it under an address that does not
// move, and asks for permission first, because what it produces is readable by
// everyone the edge already lets in.

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/tooling"
)

// The names of the publishing tools, kept in one place so the registry, the
// documentation and the tests cannot drift apart.
const (
	ToolPreviewPublish   = "preview_publish"
	ToolPreviewUnpublish = "preview_unpublish"
)

// RegisterPublishTools registers the publishing tools via add. They are
// offered only where the operator turned the channel on: without an artifact
// root and an address there is nothing to publish to, and a tool that always
// fails is a tool the model keeps trying.
func RegisterPublishTools(add func(*tooling.Tool)) {
	add(PreviewPublishTool())
	add(PreviewUnpublishTool())
}

// PreviewPublishTool copies a directory into the artifact root and returns the
// address the deployment's edge serves it under.
func PreviewPublishTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolPreviewPublish,
			Description: "Publish a finished directory of the project as a static site at a stable address this " +
				"deployment serves through its own authenticated reverse proxy, and return that address. Use it when " +
				"the user asks to share, publish or hand somebody a built site, report or demo (\"put the build " +
				"somewhere I can send to the team\"), and this Coddy is not running on the user's own machine. The " +
				"files are copied, so the address keeps answering with what was published even after the project " +
				"changes; publish again under the same name to replace it, and " + ToolPreviewUnpublish + " to take it " +
				"down. Dot-files (.git, .env) are never copied. For work you are iterating on with the user, use " +
				ToolPreviewServer + " instead: it serves the project itself on a local port and shows every edit on reload.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"path": map[string]interface{}{
						"type": "string",
						"description": "Directory to publish, relative to the working directory (default: the working directory itself). " +
							"A file is accepted too: its directory is published and the returned address opens that file. " +
							"Must be inside the working directory.",
					},
					"name": map[string]interface{}{
						"type": "string",
						"description": "A short name for this preview, letters and digits (\"release-notes\"). Publishing again under the " +
							"same name replaces the artifact at the same address, which is what to do when the user asks for an " +
							"updated build at the link they already have. Left out, every publish gets an address of its own.",
					},
					"ttl_seconds": map[string]interface{}{
						"type": "integer",
						"description": "How long the preview stays up, in seconds. 0 keeps it until it is unpublished - ask for that only " +
							"when the user wants a lasting link. Left out, the configured lifetime applies.",
					},
				},
			},
		},
		RequiresPermission: true,
		Execute:            executePreviewPublish,
	}
}

// PreviewUnpublishTool takes a published preview down.
func PreviewUnpublishTool() *tooling.Tool {
	return &tooling.Tool{
		Definition: llm.ToolDefinition{
			Name: ToolPreviewUnpublish,
			Description: "Take a preview published with " + ToolPreviewPublish + " down: its address stops answering and the " +
				"copied files are removed. Name it by the name it was published under, or by the id in the address. " +
				"Only previews this session published can be taken down.",
			InputSchema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"name": map[string]interface{}{
						"type":        "string",
						"description": "The name the preview was published under.",
					},
					"id": map[string]interface{}{
						"type":        "string",
						"description": "The id in the published address, the path element after the public URL. Use it when the preview was published without a name.",
					},
				},
			},
		},
		Execute: executePreviewUnpublish,
	}
}

type previewPublishArgs struct {
	Path string `json:"path"`
	Name string `json:"name"`
	// TTLSeconds is a pointer so "stay up until unpublished" (0) is told apart
	// from "the operator's lifetime applies" (left out).
	TTLSeconds *int `json:"ttl_seconds"`
}

type previewUnpublishArgs struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// publishSettings reads the resolved section off the environment, refusing
// when the channel is off.
func publishSettings(env *tooling.Env) (*tooling.PreviewPublishSettings, error) {
	if env == nil || env.PreviewPublish == nil || !env.PreviewPublish.Enabled {
		return nil, fmt.Errorf("publishing previews is off (tools.preview_publish.enable is false); %s serves this machine's own browser instead", ToolPreviewServer)
	}
	s := env.PreviewPublish
	if strings.TrimSpace(s.Root) == "" {
		return nil, fmt.Errorf("publishing previews has no artifact root (tools.preview_publish.root)")
	}
	return s, nil
}

func executePreviewPublish(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[previewPublishArgs](argsJSON)
	if err != nil {
		return "", err
	}
	settings, err := publishSettings(env)
	if err != nil {
		return "", err
	}
	if args.TTLSeconds != nil && *args.TTLSeconds < 0 {
		return "", fmt.Errorf("ttl_seconds must not be negative")
	}
	source, openPath, err := resolveRoot(args.Path, env.CWD)
	if err != nil {
		return "", err
	}
	ttl := secondsTTL(settings.TTLSeconds)
	if args.TTLSeconds != nil {
		ttl = secondsTTL(*args.TTLSeconds)
	}
	st := store{root: settings.Root}
	a, err := st.publish(publishRequest{
		Source:    source,
		SessionID: env.SessionID,
		Name:      args.Name,
		TTL:       ttl,
		MaxBytes:  settings.MaxBytes,
	})
	if err != nil {
		return "", err
	}
	// An artifact that outlives its own root's budget is nobody's gain: the
	// publish that just succeeded is the moment the expired ones can go.
	st.sweep(time.Now())
	return describePublished(a, settings.PublicURL, openPath), nil
}

func executePreviewUnpublish(_ context.Context, argsJSON string, env *tooling.Env) (string, error) {
	args, err := tooling.ParseArgs[previewUnpublishArgs](argsJSON)
	if err != nil {
		return "", err
	}
	settings, err := publishSettings(env)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(args.ID)
	if id == "" {
		if strings.TrimSpace(args.Name) == "" {
			return "", fmt.Errorf("name the preview to take down, by its name or by the id in its address")
		}
		if id, err = artifactID(env.SessionID, args.Name); err != nil {
			return "", err
		}
	}
	st := store{root: settings.Root}
	if err := st.remove(id, env.SessionID); err != nil {
		return "", err
	}
	return fmt.Sprintf("Unpublished %s: %s no longer answers.", id, publishedAddress(settings.PublicURL, id, "")), nil
}

// maxTTL caps a lifetime a call asked for. Without it a number large enough to
// overflow time.Duration wraps to zero, which is how the tool spells "stays up
// until it is unpublished": the one thing a caller asking for a lifetime did
// not mean.
const maxTTL = 365 * 24 * time.Hour

// secondsTTL is a lifetime in seconds as a duration, bounded.
func secondsTTL(seconds int) time.Duration {
	if seconds <= 0 {
		return 0
	}
	if time.Duration(seconds) > maxTTL/time.Second {
		return maxTTL
	}
	return time.Duration(seconds) * time.Second
}

// publishedAddress is where an artifact is read, always ending in a slash so
// the page's own relative assets, modules and fetches resolve inside it.
func publishedAddress(publicURL, id, openPath string) string {
	base := strings.TrimSpace(publicURL)
	if base == "" {
		base = "/"
	}
	if !strings.HasSuffix(base, "/") {
		base += "/"
	}
	address := base + url.PathEscape(id) + "/"
	if openPath != "" {
		address += url.PathEscape(openPath)
	}
	return address
}

// describePublished is the tool result. It is written for the model, and its
// job is to get the address in front of the user with an invitation to open
// it, the way the preview server's result does.
func describePublished(a artifact, publicURL, openPath string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Published preview: %s\n", publishedAddress(publicURL, a.ID, openPath))
	fmt.Fprintf(&b, "Copied %d files (%s) from %s as artifact %s.\n", a.Files, humanBytes(a.Bytes), a.Source, a.ID)
	if a.Skipped > 0 {
		// A page whose assets quietly did not come is worse than one that did
		// not publish, so what was left out is said rather than counted.
		fmt.Fprintf(&b, "%d entries were left out: a dot-file, a link that leaves the published directory or names a directory, "+
			"or something that is not a regular file. Check the page for anything missing.\n", a.Skipped)
	}
	if a.ExpiresAt.IsZero() {
		fmt.Fprintf(&b, "It stays up until %s takes it down.\n", ToolPreviewUnpublish)
	} else {
		fmt.Fprintf(&b, "It stays up until %s, or until %s takes it down.\n", a.ExpiresAt.Format(time.RFC3339), ToolPreviewUnpublish)
	}
	if a.Name != "" {
		fmt.Fprintf(&b, "Publishing %q again replaces it at the same address.\n", a.Name)
	}
	fmt.Fprintf(&b, "This is a copy, so it keeps answering with what was published even as the project changes. "+
		"Give the user this link and invite them to open it in their browser.")
	return b.String()
}

// humanBytes is the size of an artifact as a person reads it.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
