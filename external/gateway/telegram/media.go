//go:build gateway || gateway.telegram

package telegram

// What a person sends the bot besides text (issue #509): a photo or a
// document reaches the session the way an attachment of the web UI does -
// downloaded from Telegram here, handed over as ImageParts, saved with the
// session's assets by the manager and shown to a model that reads images -
// and the caption is the prompt. An album is collected into one turn.
// Anything else is answered in the chat, and every message the bot does not
// pass on is logged with a reason.

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"

	tgbotapi "github.com/go-telegram-bot-api/telegram-bot-api/v5"

	"github.com/EvilFreelancer/coddy-agent/internal/acp"
	"github.com/EvilFreelancer/coddy-agent/internal/config"
)

const (
	// maxInboundFileBytes is the most a bot downloads through the Bot API:
	// getFile refuses a larger file.
	maxInboundFileBytes = 20 << 20
	// maxAlbumItems is the most items Telegram puts in one album.
	maxAlbumItems = 10
	// defaultAlbumWait is how long the bot waits after the latest item of an
	// album for the next one. Telegram sends the items of an album together,
	// one message each, so the wait only has to cover the gaps between them.
	defaultAlbumWait = time.Second
	// fileDownloadTimeout bounds the download of one file.
	fileDownloadTimeout = 2 * time.Minute
)

// messageText is what the person wrote: the text of a message, else the
// caption of a photo or a file.
func messageText(msg *tgbotapi.Message) string {
	if text := strings.TrimSpace(msg.Text); text != "" {
		return text
	}
	return strings.TrimSpace(msg.Caption)
}

// withCaptionAsText is msg with the caption of a photo or a file in place of
// its text, the caption's entities with it: a command or a mention in a
// caption is one, the way Telegram marks it, and the bot reads both alike.
func withCaptionAsText(msg *tgbotapi.Message) *tgbotapi.Message {
	if msg == nil || msg.Text != "" || msg.Caption == "" {
		return msg
	}
	out := *msg
	out.Text, out.Entities = msg.Caption, msg.CaptionEntities
	return &out
}

// albumText is what the person wrote across an album: Telegram lets every
// item carry a caption, and they are read in the order the items were sent.
func albumText(items []*tgbotapi.Message) string {
	var parts []string
	for _, m := range items {
		if text := messageText(m); text != "" {
			parts = append(parts, text)
		}
	}
	return strings.Join(parts, "\n\n")
}

// mediaKind names what a message carries besides text, for the log and for
// the answer to what the bot cannot pass on: "photo" and "document", which
// it takes, or another kind. An empty name is a message with nothing but
// text, or with nothing at all (a service message).
func mediaKind(msg *tgbotapi.Message) string {
	switch {
	case len(msg.Photo) > 0:
		return "photo"
	case msg.Animation != nil:
		// Telegram sends an animation with a document beside it.
		return "animation"
	case msg.Document != nil:
		return "document"
	case msg.Video != nil:
		return "video"
	case msg.VideoNote != nil:
		return "video message"
	case msg.Voice != nil:
		return "voice message"
	case msg.Audio != nil:
		return "audio file"
	case msg.Sticker != nil:
		return "sticker"
	case msg.Location != nil, msg.Venue != nil:
		return "location"
	case msg.Contact != nil:
		return "contact"
	case msg.Poll != nil:
		return "poll"
	case msg.Dice != nil:
		return "dice"
	case msg.Game != nil:
		return "game"
	}
	return ""
}

// takesMedia reports whether the bot passes a kind of media on to the
// session.
func takesMedia(kind string) bool {
	return kind == "photo" || kind == "document"
}

// unsupportedMediaNote answers a message carrying media the bot cannot pass
// on. A video or a sound goes through as a file.
func unsupportedMediaNote(kind string) string {
	article := "a "
	if strings.ContainsRune("aeiou", rune(kind[0])) {
		article = "an "
	}
	return "I can't take " + article + kind + " here: send photos and files, with your question as their caption or as text."
}

// multimodalNote answers a file sent to a session whose model does not read
// images.
func multimodalNote(model string) string {
	return "The model of this chat (" + model + ") does not take pictures or files. Pick one that does with /model, or send your question as text."
}

// inboundFile is a file a message carries that the bot downloads.
type inboundFile struct {
	fileID string
	name   string // what the session's assets call it
	mime   string // the type the client declared; "" for a photo
	size   int    // the size Telegram reports; 0 when it does not say
}

// inboundFiles lists the files of a photo or a document message, in the
// order of the messages: the largest size of a photo (Telegram lists the
// sizes from the smallest up) and the document as sent.
func inboundFiles(msgs []*tgbotapi.Message) []inboundFile {
	var out []inboundFile
	for _, m := range msgs {
		switch mediaKind(m) {
		case "photo":
			largest := m.Photo[len(m.Photo)-1]
			out = append(out, inboundFile{fileID: largest.FileID, name: "photo_" + strconv.Itoa(m.MessageID), size: largest.FileSize})
		case "document":
			name := safeFileName(m.Document.FileName)
			if name == "" {
				name = "file_" + strconv.Itoa(m.MessageID)
			}
			out = append(out, inboundFile{fileID: m.Document.FileID, name: name, mime: m.Document.MimeType, size: m.Document.FileSize})
		}
	}
	return out
}

// safeFileName is the last element of a name a client chose, or "" when
// nothing usable is left; both separators count, whatever the system.
func safeFileName(name string) string {
	name = strings.TrimSpace(name)
	if i := strings.LastIndexAny(name, `/\`); i >= 0 {
		name = name[i+1:]
	}
	if name == "." || name == ".." {
		return ""
	}
	return name
}

// fileType is the media type a downloaded file goes to the session with: a
// picture whatever the client declared, since a picture sent as a file is
// still one for the model; else the declared type, when it is one; else what
// the bytes look like.
func fileType(declared string, data []byte) string {
	sniffed, _, _ := strings.Cut(http.DetectContentType(data), ";")
	if strings.HasPrefix(sniffed, "image/") {
		return sniffed
	}
	// The declared type is the client's word and goes into a data URL: only a
	// well-formed media type is taken.
	// application/octet-stream says the client does not know the type.
	if declared, _, err := mime.ParseMediaType(declared); err == nil && declared != "" && declared != "application/octet-stream" && !strings.HasPrefix(declared, "image/") {
		return declared
	}
	return sniffed
}

// pictureExtensions name a downloaded photo after its type, Telegram sending
// a photo without a file name.
var pictureExtensions = map[string]string{
	"image/jpeg": ".jpg",
	"image/png":  ".png",
	"image/gif":  ".gif",
	"image/webp": ".webp",
	"image/bmp":  ".bmp",
}

// apiOrigin is the Bot API origin the bot talks to: Bot.apiBase, else the
// environment's override, else api.telegram.org.
func (b *Bot) apiOrigin() string {
	base := b.apiBase
	if base == "" {
		base = os.Getenv(config.TelegramAPIBaseEnv)
	}
	base = strings.TrimRight(strings.TrimSpace(base), "/")
	if base == "" {
		return strings.TrimSuffix(tgbotapi.APIEndpoint, apiEndpointSuffix)
	}
	return base
}

// errFileDownload is a download that failed; its text names neither the
// file's address nor the token in it.
var errFileDownload = errors.New("download failed")

// downloadFiles fetches every file through the Bot API - getFile, then the
// file's address on the same origin - with the bot's own HTTP client, so the
// configured proxy applies, and returns them as the session takes
// attachments. The first failure stops it and names the file.
func (b *Bot) downloadFiles(ctx context.Context, bot *tgbotapi.BotAPI, files []inboundFile) ([]acp.ImagePartRef, string, error) {
	parts := make([]acp.ImagePartRef, 0, len(files))
	for _, f := range files {
		data, err := b.downloadFile(ctx, bot, f.fileID)
		if err != nil {
			return nil, f.name, err
		}
		mediaType := fileType(f.mime, data)
		name := f.name
		if path.Ext(name) == "" {
			name += pictureExtensions[mediaType]
		}
		parts = append(parts, acp.ImagePartRef{
			DataURL: "data:" + mediaType + ";base64," + base64.StdEncoding.EncodeToString(data),
			Name:    name,
		})
	}
	return parts, "", nil
}

func (b *Bot) downloadFile(ctx context.Context, bot *tgbotapi.BotAPI, fileID string) ([]byte, error) {
	file, err := bot.GetFile(tgbotapi.FileConfig{FileID: fileID})
	if err != nil {
		// Telegram's refusal names no address, but a request that failed on
		// the way returns the *url.Error of /bot<token>/getFile.
		return nil, fmt.Errorf("getFile: %s", withoutAddress(err))
	}
	if file.FileSize > maxInboundFileBytes {
		return nil, fmt.Errorf("getFile: the file is %d bytes, over the %d a bot downloads", file.FileSize, maxInboundFileBytes)
	}
	if path.IsAbs(file.FilePath) {
		// A self-hosted Bot API server started with --local answers with a
		// path on its own disk instead of a download address.
		return nil, fmt.Errorf("getFile: the Bot API server answered with a path on its own disk (a server in --local mode), not a download address")
	}
	ctx, cancel := context.WithTimeout(ctx, fileDownloadTimeout)
	defer cancel()
	address := b.apiOrigin() + "/file/bot" + bot.Token + "/" + file.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errFileDownload, withoutAddress(err))
	}
	resp, err := bot.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errFileDownload, withoutAddress(err))
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("%w: HTTP %d", errFileDownload, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxInboundFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("%w: %s", errFileDownload, withoutAddress(err))
	}
	if len(data) > maxInboundFileBytes {
		return nil, fmt.Errorf("%w: the file is over %d bytes", errFileDownload, maxInboundFileBytes)
	}
	return data, nil
}

// withoutAddress is the text of a request's error without the address it
// went to, which carries the bot's token: a *url.Error names it.
func withoutAddress(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + ": " + ue.Err.Error()
	}
	return err.Error()
}

// redactingClient is the bot's HTTP client with its token taken out of the
// errors of failed requests. Every Bot API address carries the token -
// /bot<token>/<method>, /file/bot<token>/<path> - and the library returns
// the *url.Error of a request that failed on the way as it is, which the
// adapter logs ("set commands", a reply that could not be sent, ...).
type redactingClient struct {
	inner tgbotapi.HTTPClient
	token string
}

func (c redactingClient) Do(req *http.Request) (*http.Response, error) {
	resp, err := c.inner.Do(req)
	var ue *url.Error
	if err != nil && c.token != "" && errors.As(err, &ue) {
		ue.URL = strings.ReplaceAll(ue.URL, c.token, "<token>")
		// A redirect the client could not follow names its target in the
		// wrapped error.
		if inner := ue.Err; inner != nil && strings.Contains(inner.Error(), c.token) {
			ue.Err = redactedError{text: strings.ReplaceAll(inner.Error(), c.token, "<token>"), inner: inner}
		}
	}
	return resp, err
}

// redactedError is an error whose text had the bot's token taken out; it
// still unwraps to the original, so a timeout is still told apart.
type redactedError struct {
	text  string
	inner error
}

func (e redactedError) Error() string { return e.text }
func (e redactedError) Unwrap() error { return e.inner }

// pendingAlbum is an album whose items are still arriving.
type pendingAlbum struct {
	chatID, userID int64
	msgs           []*tgbotapi.Message
	timer          *time.Timer
	// routed is closed once the album is in its worker's queue.
	routed chan struct{}
}

func (b *Bot) albumWaitOrDefault() time.Duration {
	if b.albumWait > 0 {
		return b.albumWait
	}
	return defaultAlbumWait
}

// albumID keys a pending album: an album's media_group_id within its chat.
func albumID(msg *tgbotapi.Message) string {
	return strconv.FormatInt(msg.Chat.ID, 10) + "/" + msg.MediaGroupID
}

// collectAlbum holds an item of an album until the rest has arrived, then
// routes the whole album as one message. The album counts as in flight from
// its first item, here in the polling goroutine, so a stop waits for it.
func (b *Bot) collectAlbum(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	id := albumID(msg)
	b.mu.Lock()
	album := b.albums[id]
	if album != nil && !album.timer.Stop() {
		// Its wait ran out and its turn is being routed: this item starts
		// another.
		album = nil
	}
	if album == nil {
		album = &pendingAlbum{chatID: msg.Chat.ID, userID: msg.From.ID, routed: make(chan struct{})}
		b.albums[id] = album
		b.inFlight.Add(1)
	}
	album.msgs = append(album.msgs, msg)
	if len(album.msgs) < maxAlbumItems {
		if album.timer == nil {
			album.timer = time.AfterFunc(b.albumWaitOrDefault(), func() { b.flushAlbum(ctx, bot, id, album) })
		} else {
			album.timer.Reset(b.albumWaitOrDefault())
		}
		b.mu.Unlock()
		return
	}
	// Telegram puts no more items in an album: it is complete.
	delete(b.albums, id)
	b.mu.Unlock()
	b.routeAlbum(ctx, bot, album)
}

// flushAlbum routes an album once no item came for the wait. The album stays
// listed until it is in its worker's queue, so a message of the same person
// arriving meanwhile waits for it (flushAlbumsBefore).
func (b *Bot) flushAlbum(ctx context.Context, bot *tgbotapi.BotAPI, id string, album *pendingAlbum) {
	b.routeAlbum(ctx, bot, album)
	b.mu.Lock()
	if b.albums[id] == album {
		delete(b.albums, id)
	}
	b.mu.Unlock()
}

// flushAlbumsBefore routes, ahead of msg, every album its sender still has
// pending in the chat - other than the one msg belongs to - and waits for one
// whose wait already ran out to be queued: a question asked right after an
// album, or a /clear, must not overtake the pictures.
func (b *Bot) flushAlbumsBefore(ctx context.Context, bot *tgbotapi.BotAPI, msg *tgbotapi.Message) {
	own := ""
	if msg.MediaGroupID != "" {
		own = albumID(msg)
	}
	var now, routing []*pendingAlbum
	b.mu.Lock()
	for id, a := range b.albums {
		if id == own || a.chatID != msg.Chat.ID || a.userID != msg.From.ID {
			continue
		}
		if a.timer.Stop() {
			delete(b.albums, id)
			now = append(now, a)
		} else {
			routing = append(routing, a)
		}
	}
	b.mu.Unlock()
	sort.Slice(now, func(i, j int) bool { return firstMessageID(now[i]) < firstMessageID(now[j]) })
	for _, a := range now {
		b.routeAlbum(ctx, bot, a)
	}
	for _, a := range routing {
		<-a.routed
	}
}

func firstMessageID(a *pendingAlbum) int {
	first := a.msgs[0].MessageID
	for _, m := range a.msgs {
		first = min(first, m.MessageID)
	}
	return first
}

// routeAlbum routes the items of an album in the order they were sent,
// headed by the one that carries the caption, and marks the album routed.
func (b *Bot) routeAlbum(ctx context.Context, bot *tgbotapi.BotAPI, album *pendingAlbum) {
	defer close(album.routed)
	// The items were appended under b.mu; reading them under it too keeps the
	// timer's goroutine ordered after the last append.
	b.mu.Lock()
	msgs := append([]*tgbotapi.Message(nil), album.msgs...)
	b.mu.Unlock()
	sort.Slice(msgs, func(i, j int) bool { return msgs[i].MessageID < msgs[j].MessageID })
	lead := msgs[0]
	for _, m := range msgs {
		if strings.TrimSpace(m.Caption) != "" {
			lead = m
			break
		}
	}
	b.route(ctx, bot, lead, msgs, true)
}
