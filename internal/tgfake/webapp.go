package tgfake

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// Mini Apps, as Telegram handles them around a bot: the web_app buttons and
// the menu button a bot sets, and the launch a tap on either starts - the
// address of the app with the launch parameters in its fragment, and the
// launch data signed with the bot's token, so that whatever checks it on the
// app's side (core.telegram.org/bots/webapps, "Validating data received via
// the Mini App") gets the answer it would get from Telegram.

const (
	// webAppVersion is the Bot API version the stand claims as the client's
	// (tgWebAppVersion): the latest the Mini App documentation names.
	webAppVersion = "10.1"
	// webAppPlatform is what tgWebAppPlatform says: an app that tells
	// platforms apart sees the stand as one of its own.
	webAppPlatform = "tgfake"
	// defaultColorScheme is the theme a launch carries when none is asked.
	defaultColorScheme = "dark"
)

// isLoopbackHost reports whether host names this machine: localhost or a
// loopback address. The stand lets a Mini App run there over plain http,
// which Telegram itself never does, so a Coddy started on a port of this
// machine can be opened from the chat page.
func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// webAppURLProblem says why Telegram would not open raw as a Mini App, or ""
// when it would: an absolute https address, or plain http on this machine.
func webAppURLProblem(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return fmt.Sprintf("Web App URL '%s' is invalid", raw)
	}
	if u.Scheme == "http" && !isLoopbackHost(u.Hostname()) {
		return fmt.Sprintf("Web App URL '%s' is invalid: Only HTTPS links are allowed", raw)
	}
	return ""
}

// webAppURLOf returns the address a web_app button opens, "" for any other
// button.
func webAppURLOf(b InlineKeyboardButton) string {
	if b.WebApp == nil {
		return ""
	}
	return b.WebApp.URL
}

// chatTypeLocked is the type of chat id: the one it has, or the one the
// stand gives a chat it has not seen (a negative id is a group). It creates
// nothing. Caller holds s.mu.
func (s *Server) chatTypeLocked(id int64) string {
	if c := s.chats[id]; c != nil {
		return c.typ
	}
	if id < 0 {
		return "group"
	}
	return "private"
}

// Token is the bot token the stand signs launch data with: the one it was
// started with, else the one in the path of the latest Bot API call.
func (s *Server) Token() string {
	if s.opts.Token != "" {
		return s.opts.Token
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.token
}

// MenuButton is the menu button chat shows: its own, else the bot's; chat 0
// asks for the bot's. A bot that set nothing shows its commands.
func (s *Server) MenuButton(chat int64) MenuButton {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.menuButtonLocked(chat)
}

func (s *Server) menuButtonLocked(chat int64) MenuButton {
	// Telegram shows a bot's menu button in private chats only.
	if chat != 0 && s.chatTypeLocked(chat) != "private" {
		return MenuButton{Type: "commands"}
	}
	if b := s.chatMenus[chat]; chat != 0 && b != nil {
		return cloneMenuButton(*b)
	}
	if s.defaultMenu != nil {
		return cloneMenuButton(*s.defaultMenu)
	}
	return MenuButton{Type: "commands"}
}

func cloneMenuButton(b MenuButton) MenuButton {
	if b.WebApp != nil {
		app := *b.WebApp
		b.WebApp = &app
	}
	return b
}

// menuButtonChat reads the chat_id of a menu button method the way the Bot
// API does: as a user, so anything but a positive id (a group's included) is
// refused. Without chat_id the method is about the bot's own button, chat 0.
func (s *Server) menuButtonChat(w http.ResponseWriter, method string, params url.Values) (int64, bool) {
	raw := strings.TrimSpace(params.Get("chat_id"))
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: Invalid chat_id specified", 0)
		return 0, false
	}
	return id, true
}

// setChatMenuButton answers setChatMenuButton: a private chat's own button
// with chat_id, the bot's without. Type "default" (or no menu_button at all)
// removes the chat's own button, or puts the bot's back to its commands. The
// button is checked before the chat, as the Bot API does.
func (s *Server) setChatMenuButton(w http.ResponseWriter, method string, params url.Values) {
	button := MenuButton{Type: "default"}
	if raw := strings.TrimSpace(params.Get("menu_button")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &button); err != nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: can't parse menu button JSON object", 0)
			return
		}
	}
	switch button.Type {
	case "commands", "default":
		button = MenuButton{Type: button.Type}
	case "web_app":
		if strings.TrimSpace(button.Text) == "" {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button text is empty", 0)
			return
		}
		if button.WebApp == nil {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button Web App URL '' is invalid", 0)
			return
		}
		if problem := webAppURLProblem(button.WebApp.URL); problem != "" {
			s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: menu button "+problem, 0)
			return
		}
		button = cloneMenuButton(button)
	default:
		s.writeError(w, method, params, http.StatusBadRequest, "Bad Request: unsupported menu button type", 0)
		return
	}
	chat, ok := s.menuButtonChat(w, method, params)
	if !ok {
		return
	}
	s.mu.Lock()
	switch {
	case chat != 0 && button.Type == "default":
		delete(s.chatMenus, chat)
	case chat != 0:
		s.chatMenus[chat] = &button
	case button.Type == "default":
		s.defaultMenu = nil
	default:
		s.defaultMenu = &button
	}
	s.mu.Unlock()
	s.writeResult(w, method, params, true)
}

// getChatMenuButton answers getChatMenuButton with the button the chat shows.
func (s *Server) getChatMenuButton(w http.ResponseWriter, method string, params url.Values) {
	chat, ok := s.menuButtonChat(w, method, params)
	if !ok {
		return
	}
	s.writeResult(w, method, params, s.MenuButton(chat))
}

// WebAppLaunch is a person opening a Mini App: from a web_app button (URL
// set) or from the menu button of the chat (URL empty). Zero fields take the
// defaults of a message: user 4242 "alice".
type WebAppLaunch struct {
	ChatID      int64  `json:"chat_id,omitempty"`
	UserID      int64  `json:"user_id,omitempty"`
	Username    string `json:"username,omitempty"`
	FirstName   string `json:"first_name,omitempty"`
	URL         string `json:"url,omitempty"`
	StartParam  string `json:"start_param,omitempty"`
	ColorScheme string `json:"color_scheme,omitempty"` // light or dark (default)
	Platform    string `json:"platform,omitempty"`
	Version     string `json:"version,omitempty"`
}

// LaunchedWebApp is what the client opens: the app's address with the launch
// parameters in its fragment, and the signed launch data among them.
type LaunchedWebApp struct {
	URL         string            `json:"url"`
	InitData    string            `json:"init_data"`
	ThemeParams map[string]string `json:"theme_params"`
	Version     string            `json:"version"`
	Platform    string            `json:"platform"`
}

// LaunchWebApp builds a launch the way Telegram does when a person opens a
// Mini App: the launch data (query_id, user, auth_date, start_param when
// there is one) signed with the bot's token, and the address of the app with
// tgWebAppData, tgWebAppVersion, tgWebAppPlatform and tgWebAppThemeParams in
// its fragment - after the fragment the address already has, behind a
// question mark, the form the official SDK parses - and a start parameter in
// its query as tgWebAppStartParam. The Ed25519 signature Telegram adds for
// third parties is left out: only Telegram holds that key.
func (s *Server) LaunchWebApp(req WebAppLaunch) (LaunchedWebApp, error) {
	token := s.Token()
	if token == "" {
		return LaunchedWebApp{}, errors.New("no bot token to sign with yet: let the bot call the stand first, or start it with --token")
	}
	app := strings.TrimSpace(req.URL)
	if app == "" {
		menu := s.MenuButton(req.ChatID)
		if menu.Type != "web_app" || menu.WebApp == nil {
			return LaunchedWebApp{}, errors.New("no Mini App to open: give a url, or let the bot set a web_app menu button")
		}
		app = menu.WebApp.URL
	}
	if problem := webAppURLProblem(app); problem != "" {
		return LaunchedWebApp{}, errors.New(problem)
	}
	in := IncomingMessage{ChatID: req.ChatID, UserID: req.UserID, Username: req.Username, FirstName: req.FirstName, Text: "-"}
	in.normalize()
	user, _ := json.Marshal(map[string]any{
		"id": in.UserID, "first_name": in.FirstName, "username": in.Username,
		"language_code": "en", "allows_write_to_pm": true,
	})
	s.mu.Lock()
	s.nextQuery++
	query := fmt.Sprintf("tgfake-%d", s.nextQuery)
	authDate := s.now().Unix()
	s.mu.Unlock()

	data := url.Values{}
	data.Set("query_id", query)
	data.Set("user", string(user))
	data.Set("auth_date", strconv.FormatInt(authDate, 10))
	if req.StartParam != "" {
		data.Set("start_param", req.StartParam)
	}
	data.Set("hash", signInitData(token, data))
	initData := data.Encode()

	scheme := strings.ToLower(strings.TrimSpace(req.ColorScheme))
	if scheme != "light" {
		scheme = defaultColorScheme
	}
	theme := ThemeParams(scheme)
	themeJSON, _ := json.Marshal(theme)
	version := strings.TrimSpace(req.Version)
	if version == "" {
		version = webAppVersion
	}
	platform := strings.TrimSpace(req.Platform)
	if platform == "" {
		platform = webAppPlatform
	}
	launch := url.Values{}
	launch.Set("tgWebAppData", initData)
	launch.Set("tgWebAppVersion", version)
	launch.Set("tgWebAppPlatform", platform)
	launch.Set("tgWebAppThemeParams", string(themeJSON))

	if req.StartParam != "" {
		u, err := url.Parse(app)
		if err != nil {
			return LaunchedWebApp{}, err
		}
		q := u.Query()
		q.Set("tgWebAppStartParam", req.StartParam)
		u.RawQuery = q.Encode()
		app = u.String()
	}
	return LaunchedWebApp{
		URL:         appendLaunchParams(app, launch.Encode()),
		InitData:    initData,
		ThemeParams: theme,
		Version:     version,
		Platform:    platform,
	}, nil
}

// appendLaunchParams puts the encoded launch parameters into the fragment of
// app: the whole fragment when it has none, behind "?" after a fragment of
// its own, behind "&" after one that already carries a query.
func appendLaunchParams(app, params string) string {
	base, frag, ok := strings.Cut(app, "#")
	switch {
	case !ok || frag == "":
		return base + "#" + params
	case strings.Contains(frag, "?"):
		return app + "&" + params
	default:
		return app + "?" + params
	}
}

// signInitData is the hash Telegram puts into launch data: the hex
// HMAC-SHA256 of the data-check-string (every field but hash, sorted by key,
// as key=value lines) under the HMAC-SHA256 of the token keyed "WebAppData".
func signInitData(token string, data url.Values) string {
	keys := make([]string, 0, len(data))
	for k := range data {
		if k != "hash" {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	lines := make([]string, 0, len(keys))
	for _, k := range keys {
		lines = append(lines, k+"="+data.Get(k))
	}
	secret := hmac.New(sha256.New, []byte("WebAppData"))
	secret.Write([]byte(token))
	mac := hmac.New(sha256.New, secret.Sum(nil))
	mac.Write([]byte(strings.Join(lines, "\n")))
	return hex.EncodeToString(mac.Sum(nil))
}

// ThemeParams are the colours a Telegram client hands a Mini App
// (tgWebAppThemeParams and theme_changed) for its light or dark theme.
func ThemeParams(scheme string) map[string]string {
	if scheme == "light" {
		return map[string]string{
			"bg_color": "#ffffff", "secondary_bg_color": "#efeff3", "section_bg_color": "#ffffff",
			"header_bg_color": "#ffffff", "bottom_bar_bg_color": "#ffffff",
			"text_color": "#000000", "hint_color": "#999999", "subtitle_text_color": "#999999",
			"section_header_text_color": "#6d6d72", "link_color": "#2481cc", "accent_text_color": "#2481cc",
			"button_color": "#2481cc", "button_text_color": "#ffffff", "destructive_text_color": "#ff3b30",
		}
	}
	return map[string]string{
		"bg_color": "#212121", "secondary_bg_color": "#0f0f0f", "section_bg_color": "#212121",
		"header_bg_color": "#212121", "bottom_bar_bg_color": "#212121",
		"text_color": "#ffffff", "hint_color": "#aaaaaa", "subtitle_text_color": "#aaaaaa",
		"section_header_text_color": "#8774e1", "link_color": "#8774e1", "accent_text_color": "#8774e1",
		"button_color": "#8774e1", "button_text_color": "#ffffff", "destructive_text_color": "#ff595a",
	}
}
