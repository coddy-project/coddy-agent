package agent

import (
	"fmt"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/llm"
	"github.com/EvilFreelancer/coddy-agent/internal/session"
)

// saveGeneratedImages stores native output on the assistant message, never on
// a synthetic tool or user row. Provider item ids are not filesystem names.
func (a *Agent) saveGeneratedImages(images []llm.GeneratedImage) ([]llm.ImagePart, error) {
	if len(images) == 0 {
		return nil, nil
	}
	dir := strings.TrimSpace(a.state.GetPersistedSessionDir())
	if dir == "" {
		return nil, fmt.Errorf("generated images require a persisted session")
	}
	parts := make([]llm.ImagePart, 0, len(images))
	for i, img := range images {
		name := fmt.Sprintf("generated-%d%s", i+1, session.ToolImageExt(img.MIMEType))
		asset, thumb, err := session.SaveToolImageAsset(dir, name, img.MIMEType, img.Data)
		if err != nil {
			return parts, fmt.Errorf("save generated image: %w", err)
		}
		parts = append(parts, llm.ImagePart{Name: name, MIMEType: img.MIMEType, Size: len(img.Data), FilePath: asset, ThumbnailPath: thumb})
	}
	return parts, nil
}

func (a *Agent) sendGeneratedImages(messageID string, parts []llm.ImagePart) {
	images := toolImagesForSurfaces(a.state.GetID(), parts)
	if len(images) == 0 {
		return
	}
	update := acp.AssistantImagesUpdate{SessionUpdate: "assistant_images", MessageID: messageID}
	for _, img := range images {
		update.Images = append(update.Images, acp.AssistantImage{Name: img.Name, MIMEType: img.MIMEType, Asset: img.Asset, URL: img.URL, PreviewURL: img.PreviewURL})
	}
	if err := a.server.SendSessionUpdate(a.state.GetID(), update); err != nil {
		a.log.Warn("generated image update was not delivered", "err", err)
	}
}
