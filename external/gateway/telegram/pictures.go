//go:build gateway || gateway.telegram

package telegram

import (
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/EvilFreelancer/coddy-agent/internal/session"
	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"
)

// pictureSession is the session a chat turn runs in, as far as sending its
// pictures goes: which session it is, and where its bundle keeps the copies.
type pictureSession interface {
	GetID() string
	GetPersistedSessionDir() string
}

// albumMax is the most pictures Telegram takes as one album, and 2 the
// fewest: one picture goes as a photo of its own.
const albumMax = 10

// outgoingPicture is a picture of a call read from the copy the session kept.
type outgoingPicture struct {
	name string
	data []byte
}

// sendPictures posts the pictures a finished call of the chat's own session
// returned (read on an image file, an MCP tool's image blocks), captioned
// with their names, so the chat sees what the agent looked at while the turn
// runs: one picture as a photo, several as albums of up to ten, a picture
// left over after the last full album as a photo. The photo is the copy kept
// with the session, the picture the model was shown even when the workspace
// file changed since. A picture Telegram refuses as a photo (a side past its
// limits) goes as a document; one that cannot be read is left out, and the
// answer still comes. A subagent's calls belong to another session and are
// not the chat's to see.
func (s *Sender) sendPictures(sessionID string, images []session.ToolImage) {
	if len(images) == 0 || s.pictures == nil || sessionID != s.pictures.GetID() {
		return
	}
	dir := strings.TrimSpace(s.pictures.GetPersistedSessionDir())
	if dir == "" {
		return
	}
	assets := session.AssetsPath(dir)
	var pictures []outgoingPicture
	for _, img := range images {
		// The asset is a bare name inside the session's assets directory; any
		// other spelling is not a copy this session made.
		if img.Asset == "" || filepath.Base(img.Asset) != img.Asset || img.Asset == "." || img.Asset == ".." {
			continue
		}
		path := filepath.Join(assets, img.Asset)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			s.log.Warn("telegram: read picture", "asset", img.Asset, "err", err)
			continue
		}
		// The bytes decide, not the name: a file that is no picture (replaced
		// since the call, damaged) is not posted, not even as a document.
		if !strings.HasPrefix(http.DetectContentType(data), "image/") {
			s.log.Warn("telegram: the saved picture is not an image", "asset", img.Asset)
			continue
		}
		name := strings.TrimSpace(img.Name)
		if name == "" {
			name = img.Asset
		}
		pictures = append(pictures, outgoingPicture{name: name, data: data})
	}
	posted := false
	for start := 0; start < len(pictures); start += albumMax {
		group := pictures[start:min(start+albumMax, len(pictures))]
		if len(group) == 1 {
			posted = s.sendPicture(group[0]) || posted
			continue
		}
		posted = s.sendAlbum(group) || posted
	}
	if posted {
		s.moveLiveBelow()
	}
}

// sendAlbum posts 2 to albumMax pictures as one album and reports whether
// anything was posted. An album Telegram refuses as photos goes picture by
// picture, so each can still go as a document; any other failure (a rate
// limit, a failure on the way) is not tried another way.
func (s *Sender) sendAlbum(pictures []outgoingPicture) bool {
	media := make([]interface{}, 0, len(pictures))
	for _, p := range pictures {
		photo := tgbotapi.NewInputMediaPhoto(tgbotapi.FileBytes{Name: p.name, Bytes: p.data})
		photo.Caption = p.name
		media = append(media, photo)
	}
	_, err := s.bot.SendMediaGroup(tgbotapi.NewMediaGroup(s.chatID, media))
	if err == nil {
		return true
	}
	if !photoRefused(err) {
		s.log.Warn("telegram: send album", "pictures", len(pictures), "err", err)
		return false
	}
	s.log.Debug("telegram: album refused as photos, sending its pictures one by one", "pictures", len(pictures), "err", err)
	posted := false
	for _, p := range pictures {
		posted = s.sendPicture(p) || posted
	}
	return posted
}

// sendPicture posts one picture as a photo, or as a document when Telegram
// refuses it as a photo, and reports whether it was posted.
func (s *Sender) sendPicture(p outgoingPicture) bool {
	file := tgbotapi.FileBytes{Name: p.name, Bytes: p.data}
	photo := tgbotapi.NewPhoto(s.chatID, file)
	photo.Caption = p.name
	_, err := s.bot.Send(photo)
	if err == nil {
		return true
	}
	if !photoRefused(err) {
		s.log.Warn("telegram: send picture", "name", p.name, "err", err)
		return false
	}
	s.log.Debug("telegram: picture refused as a photo, sending it as a document", "name", p.name, "err", err)
	doc := tgbotapi.NewDocument(s.chatID, file)
	doc.Caption = p.name
	if _, err := s.bot.Send(doc); err != nil {
		s.log.Warn("telegram: send picture", "name", p.name, "err", err)
		return false
	}
	return true
}

// photoRefused reports whether Telegram refused a picture as a photo - its
// size, its shape, its format, or photos not being allowed in the chat - the
// one failure another try as a document can get past. A rate limit, a failure
// on the way or a Bad Request about the chat itself would meet the document
// the same way. The library leaves the code of an upload's error at zero, so
// the Bot API's own "Bad Request" wording tells a refusal apart, and its
// description names the photo or the image.
func photoRefused(err error) bool {
	var apiErr *tgbotapi.Error
	if !errors.As(err, &apiErr) || (apiErr.Code != http.StatusBadRequest && !strings.HasPrefix(apiErr.Message, "Bad Request")) {
		return false
	}
	desc := strings.ToLower(apiErr.Message)
	return strings.Contains(desc, "photo") || strings.Contains(desc, "image")
}

// moveLiveBelow keeps the answer under the pictures it follows. Without Rich
// Messages the answer grows in one live message sent before the call ran, and
// its final edit would leave the finished answer above the photos. So the
// live message is dropped once a picture is posted: the text streamed so far
// is still in the answer, which comes as a new message below, again a reply
// to the person's message. The old message is deleted when Telegram lets it
// be; when it does not (too old, already gone) the answer still moves below.
// Rich Messages need nothing: a draft is not a message, and the answer is
// sent at the end.
func (s *Sender) moveLiveBelow() {
	if s.rich.enabled {
		return
	}
	s.mu.Lock()
	liveID := s.liveID
	if liveID != 0 {
		s.liveID = 0
		s.replyTo = s.askedID
	}
	s.mu.Unlock()
	if liveID == 0 {
		return
	}
	if _, err := s.bot.Request(tgbotapi.NewDeleteMessage(s.chatID, liveID)); err != nil {
		s.log.Debug("telegram: delete the live message above a picture", "err", err)
	}
}
