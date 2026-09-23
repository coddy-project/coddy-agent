package llm

// The wire layer of the devin provider: the handful of protobuf shapes the
// Devin (Windsurf/Codeium "Cascade") API server speaks over Connect-RPC,
// encoded by hand. The API publishes no schema, so only the fields Coddy
// sends or reads are known here, by number; everything else a response
// carries is skipped. Proto3 rules apply on the way out: a zero scalar and an
// empty string are omitted.

import (
	"bytes"
	"compress/gzip"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"math"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const (
	pbVarint  = 0
	pbFixed64 = 1
	pbBytes   = 2
	pbFixed32 = 5
)

// pbWriter appends protobuf fields to a buffer.
type pbWriter struct {
	buf []byte
}

func (w *pbWriter) varint(v uint64) {
	w.buf = binary.AppendUvarint(w.buf, v)
}

func (w *pbWriter) tag(field, wire int) {
	w.varint(uint64(field)<<3 | uint64(wire))
}

// str writes a string field; an empty string is omitted. Proto3 strings
// must be valid UTF-8 and the API server rejects the whole request with an
// opaque invalid_argument otherwise, so bytes that are not UTF-8 (a binary
// file or a legacy-encoded one read by a tool) become U+FFFD, the same
// replacement encoding/json makes for the JSON providers.
func (w *pbWriter) str(field int, s string) {
	if s == "" {
		return
	}
	s = strings.ToValidUTF8(s, "\uFFFD")
	w.tag(field, pbBytes)
	w.varint(uint64(len(s)))
	w.buf = append(w.buf, s...)
}

// uint writes a varint field; zero is omitted.
func (w *pbWriter) uint(field int, v uint64) {
	if v == 0 {
		return
	}
	w.tag(field, pbVarint)
	w.varint(v)
}

// boolean writes a bool field; false is omitted.
func (w *pbWriter) boolean(field int, v bool) {
	if v {
		w.uint(field, 1)
	}
}

// double writes a fixed64 IEEE-754 field; zero is omitted.
func (w *pbWriter) double(field int, v float64) {
	if v == 0 {
		return
	}
	w.tag(field, pbFixed64)
	w.buf = binary.LittleEndian.AppendUint64(w.buf, math.Float64bits(v))
}

// msg writes an embedded message field, always (an empty message still says
// the field is present).
func (w *pbWriter) msg(field int, body []byte) {
	w.tag(field, pbBytes)
	w.varint(uint64(len(body)))
	w.buf = append(w.buf, body...)
}

// pbField is one decoded field. For varints the value is in num; for fixed32
// and fixed64 the raw little-endian bytes are in raw, as they are for the
// length-delimited kind.
type pbField struct {
	field int
	wire  int
	num   uint64
	raw   []byte
}

func (f pbField) text() string { return string(f.raw) }

// errPBTruncated reports a protobuf message that ends inside a field.
var errPBTruncated = errors.New("devin: truncated protobuf message")

// pbFields splits a protobuf message into its top-level fields. A message
// cut short is an error rather than a silently shorter list: a frame the
// server sent whole never ends inside a field.
func pbFields(buf []byte) ([]pbField, error) {
	var out []pbField
	for len(buf) > 0 {
		key, n := binary.Uvarint(buf)
		if n <= 0 {
			return nil, errPBTruncated
		}
		buf = buf[n:]
		f := pbField{field: int(key >> 3), wire: int(key & 7)}
		switch f.wire {
		case pbVarint:
			v, m := binary.Uvarint(buf)
			if m <= 0 {
				return nil, errPBTruncated
			}
			f.num = v
			buf = buf[m:]
		case pbFixed64:
			if len(buf) < 8 {
				return nil, errPBTruncated
			}
			f.raw = buf[:8]
			buf = buf[8:]
		case pbFixed32:
			if len(buf) < 4 {
				return nil, errPBTruncated
			}
			f.raw = buf[:4]
			buf = buf[4:]
		case pbBytes:
			l, m := binary.Uvarint(buf)
			if m <= 0 || l > uint64(len(buf)-m) {
				return nil, errPBTruncated
			}
			f.raw = buf[m : m+int(l)]
			buf = buf[m+int(l):]
		default:
			return nil, fmt.Errorf("devin: unsupported protobuf wire type %d", f.wire)
		}
		out = append(out, f)
	}
	return out, nil
}

// Connect envelope flags (https://connectrpc.com/docs/protocol#streaming-rpcs).
const (
	connectFlagCompressed = 0x01
	connectFlagEndStream  = 0x02
	// connectMaxFrame bounds one envelope, so a corrupt length prefix cannot
	// make the reader allocate gigabytes.
	connectMaxFrame = 16 << 20
)

// connectEnvelope frames one streaming request message, gzip-compressed the
// way the Devin clients send it.
func connectEnvelope(payload []byte) ([]byte, error) {
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	if _, err := zw.Write(payload); err != nil {
		return nil, err
	}
	if err := zw.Close(); err != nil {
		return nil, err
	}
	out := make([]byte, 5, 5+gz.Len())
	out[0] = connectFlagCompressed
	binary.BigEndian.PutUint32(out[1:5], uint32(gz.Len()))
	return append(out, gz.Bytes()...), nil
}

// connectFrame is one decoded envelope of a streaming response.
type connectFrame struct {
	endStream bool
	payload   []byte
}

// readConnectFrame reads the next envelope. io.EOF means the body ended on a
// frame boundary; a body that ends inside a frame is io.ErrUnexpectedEOF.
func readConnectFrame(r io.Reader) (connectFrame, error) {
	var hdr [5]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return connectFrame{}, err
	}
	n := binary.BigEndian.Uint32(hdr[1:5])
	if n > connectMaxFrame {
		return connectFrame{}, fmt.Errorf("devin: stream frame of %d bytes exceeds the %d byte limit", n, connectMaxFrame)
	}
	payload := make([]byte, n)
	if _, err := io.ReadFull(r, payload); err != nil {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return connectFrame{}, err
	}
	if hdr[0]&connectFlagCompressed != 0 {
		zr, err := gzip.NewReader(bytes.NewReader(payload))
		if err != nil {
			return connectFrame{}, fmt.Errorf("devin: stream frame: %w", err)
		}
		plain, err := io.ReadAll(io.LimitReader(zr, connectMaxFrame+1))
		if err != nil {
			return connectFrame{}, fmt.Errorf("devin: stream frame: %w", err)
		}
		if len(plain) > connectMaxFrame {
			return connectFrame{}, fmt.Errorf("devin: stream frame inflates past the %d byte limit", connectMaxFrame)
		}
		payload = plain
	}
	return connectFrame{endStream: hdr[0]&connectFlagEndStream != 0, payload: payload}, nil
}

// Client identity sent in every request's Metadata. The API server gates
// some models on the IDE name: chat must present itself as Devin Desktop
// ("This model is only in Devin Local" otherwise), while the model catalog
// only returns the real family list to a Windsurf client.
const (
	devinChatIDE       = "devin-desktop"
	devinCatalogIDE    = "windsurf"
	devinClientVersion = "3.6.27"
)

// devinMetadata is the Metadata message (field 1 of every request).
type devinMetadata struct {
	ide       string
	apiKey    string
	userJWT   string
	sessionID string
	requestID uint64
	triggerID string
}

func devinOSName() string {
	switch runtime.GOOS {
	case "darwin":
		return "darwin"
	case "windows":
		return "windows"
	default:
		return "linux"
	}
}

func (m devinMetadata) encode() []byte {
	var w pbWriter
	w.str(1, m.ide)
	w.str(2, devinClientVersion)
	w.str(3, m.apiKey)
	w.str(4, "en")
	w.str(5, devinOSName())
	w.str(7, devinClientVersion)
	w.uint(9, m.requestID)
	w.str(10, m.sessionID)
	w.str(12, m.ide)
	now := time.Now()
	var ts pbWriter
	ts.uint(1, uint64(now.Unix()))
	ts.uint(2, uint64(now.Nanosecond()))
	w.msg(16, ts.buf)
	w.str(21, m.userJWT)
	w.str(25, m.triggerID)
	w.str(26, "Unset")
	w.str(28, m.ide)
	return w.buf
}

// devinPrompt sources (ChatMessagePrompt.source).
const (
	devinSourceUser      = 1
	devinSourceAssistant = 2
	devinSourceTool      = 4
)

// devinToolCall is a ChatToolCall.
type devinToolCall struct {
	id, name, args string
}

// devinImage is an ImageData attached to a user prompt.
type devinImage struct {
	base64, mime string
}

// devinPrompt is one ChatMessagePrompt of the conversation.
type devinPrompt struct {
	source     int
	text       string
	toolCalls  []devinToolCall
	toolCallID string
	images     []devinImage
	// thinking, signature, signatureType and redacted replay a signed
	// reasoning block of an earlier assistant turn.
	thinking      string
	signature     string
	signatureType string
	redacted      bool
}

func (p devinPrompt) encode() []byte {
	var w pbWriter
	w.uint(2, uint64(p.source))
	w.str(3, p.text)
	// A rough token count and the "safe for code telemetry" flag, both of
	// which the Devin clients always send.
	w.uint(4, uint64(max(1, len(p.text)/4)))
	w.uint(5, 1)
	for _, tc := range p.toolCalls {
		var t pbWriter
		t.str(1, tc.id)
		t.str(2, tc.name)
		t.str(3, tc.args)
		w.msg(6, t.buf)
	}
	w.str(7, p.toolCallID)
	for _, img := range p.images {
		var iw pbWriter
		iw.str(1, img.base64)
		iw.str(2, img.mime)
		w.msg(10, iw.buf)
	}
	if p.signature != "" {
		w.str(11, p.thinking)
		w.str(12, p.signature)
		w.boolean(13, p.redacted)
		w.str(18, p.signatureType)
	}
	return w.buf
}

// devinToolDef is a ChatToolDefinition.
type devinToolDef struct {
	name, description, schema string
}

// devinChatRequest is a GetChatMessageRequest.
type devinChatRequest struct {
	metadata     devinMetadata
	system       string
	prompts      []devinPrompt
	tools        []devinToolDef
	modelUID     string
	maxTokens    int
	temperature  float64
	cascadeID    string
	trajectoryID string
}

// devinRequestTypeCascade and devinPlannerModeDefault are the request type
// and planner mode every Devin chat client sends.
const (
	devinRequestTypeCascade = 5
	devinPlannerModeDefault = 1
)

func (r devinChatRequest) encode() []byte {
	var w pbWriter
	w.msg(1, r.metadata.encode())
	w.str(2, r.system)
	for _, p := range r.prompts {
		w.msg(3, p.encode())
	}
	w.uint(7, devinRequestTypeCascade)
	var conf pbWriter
	conf.uint(1, 1) // one completion
	conf.uint(2, uint64(max(1, r.maxTokens)))
	conf.uint(3, 400) // max newlines
	conf.double(5, r.temperature)
	conf.uint(7, 40)     // top_k
	conf.double(8, 0.95) // top_p
	w.msg(8, conf.buf)
	for _, t := range r.tools {
		var tw pbWriter
		tw.str(1, t.name)
		tw.str(2, t.description)
		tw.str(3, t.schema)
		w.msg(10, tw.buf)
	}
	var traj pbWriter
	traj.str(1, r.trajectoryID)
	traj.uint(3, 4)
	traj.uint(4, 14)
	w.msg(15, traj.buf)
	w.str(16, r.cascadeID)
	w.uint(20, devinPlannerModeDefault)
	w.str(21, r.modelUID)
	return w.buf
}

// devinChatDelta is what one GetChatMessageResponse frame contributes.
type devinChatDelta struct {
	text          string
	thinking      string
	signature     string
	signatureType string
	redacted      bool
	toolCalls     []devinToolCall
	stopReason    uint64
	usage         *devinUsage
}

// devinUsage is the ModelUsageStats a frame carries. Input counts only the
// uncached part of the prompt; the prefix cache is reported apart.
type devinUsage struct {
	input, output, cacheWrite, cacheRead int
}

func decodeDevinChatDelta(payload []byte) (devinChatDelta, error) {
	fields, err := pbFields(payload)
	if err != nil {
		return devinChatDelta{}, err
	}
	var d devinChatDelta
	for _, f := range fields {
		switch {
		case f.field == 3 && f.wire == pbBytes:
			d.text += f.text()
		case f.field == 5 && f.wire == pbVarint:
			d.stopReason = f.num
		case f.field == 6 && f.wire == pbBytes:
			tc, err := decodeDevinToolCall(f.raw)
			if err != nil {
				return devinChatDelta{}, err
			}
			d.toolCalls = append(d.toolCalls, tc)
		case f.field == 7 && f.wire == pbBytes:
			u, ok, err := decodeDevinUsage(f.raw)
			if err != nil {
				return devinChatDelta{}, err
			}
			if ok {
				d.usage = &u
			}
		case f.field == 9 && f.wire == pbBytes:
			d.thinking += f.text()
		case f.field == 10 && f.wire == pbBytes:
			d.signature += f.text()
		case f.field == 11 && f.wire == pbVarint:
			d.redacted = d.redacted || f.num != 0
		case f.field == 21 && f.wire == pbBytes:
			d.signatureType = f.text()
		}
	}
	return d, nil
}

func decodeDevinToolCall(raw []byte) (devinToolCall, error) {
	fields, err := pbFields(raw)
	if err != nil {
		return devinToolCall{}, err
	}
	var tc devinToolCall
	for _, f := range fields {
		if f.wire != pbBytes {
			continue
		}
		switch f.field {
		case 1:
			tc.id = f.text()
		case 2:
			tc.name = f.text()
		case 3:
			tc.args = f.text()
		}
	}
	return tc, nil
}

// decodeDevinUsage reads a ModelUsageStats. Every frame carries one, most of
// them without token counts; ok is false for those.
func decodeDevinUsage(raw []byte) (devinUsage, bool, error) {
	fields, err := pbFields(raw)
	if err != nil {
		return devinUsage{}, false, err
	}
	var u devinUsage
	ok := false
	for _, f := range fields {
		if f.wire != pbVarint {
			continue
		}
		switch f.field {
		case 2:
			u.input, ok = int(f.num), true
		case 3:
			u.output, ok = int(f.num), true
		case 4:
			u.cacheWrite, ok = int(f.num), true
		case 5:
			u.cacheRead, ok = int(f.num), true
		}
	}
	return u, ok, nil
}

// devinCatalogEntry is one ClientModelConfig of GetCliModelConfigs.
type devinCatalogEntry struct {
	uid            string
	label          string
	family         string
	familyLabel    string
	familyDefault  bool
	legacy         bool
	disabled       bool
	supportsImages bool
	contextWindow  int
	maxOutput      int
}

func decodeDevinCatalog(payload []byte) ([]devinCatalogEntry, error) {
	fields, err := pbFields(payload)
	if err != nil {
		return nil, err
	}
	var out []devinCatalogEntry
	for _, f := range fields {
		if f.field != 1 || f.wire != pbBytes {
			continue
		}
		e, err := decodeDevinCatalogEntry(f.raw)
		if err != nil {
			return nil, err
		}
		out = append(out, e)
	}
	return out, nil
}

func decodeDevinCatalogEntry(raw []byte) (devinCatalogEntry, error) {
	fields, err := pbFields(raw)
	if err != nil {
		return devinCatalogEntry{}, err
	}
	var e devinCatalogEntry
	for _, f := range fields {
		switch {
		case f.field == 1 && f.wire == pbBytes:
			e.label = f.text()
		case f.field == 2:
			// The legacy model enum: only the retired MODEL_* entries the
			// Devin clients no longer offer carry it.
			e.legacy = true
		case f.field == 4 && f.wire == pbVarint:
			e.disabled = f.num != 0
		case f.field == 5 && f.wire == pbVarint:
			e.supportsImages = f.num != 0
		case f.field == 18 && f.wire == pbVarint:
			if e.contextWindow == 0 {
				e.contextWindow = int(f.num)
			}
		case f.field == 22 && f.wire == pbBytes:
			e.uid = f.text()
		case f.field == 23 && f.wire == pbBytes:
			if err := e.decodeModelInfo(f.raw); err != nil {
				return devinCatalogEntry{}, err
			}
		case f.field == 30 && f.wire == pbBytes:
			sub, err := pbFields(f.raw)
			if err != nil {
				return devinCatalogEntry{}, err
			}
			for _, s := range sub {
				if s.field == 1 && s.wire == pbBytes {
					e.familyLabel = s.text()
				}
			}
		case f.field == 31 && f.wire == pbVarint:
			e.familyDefault = f.num != 0
		}
	}
	return e, nil
}

// decodeModelInfo reads the ModelInfo of a catalog entry: the context window
// (4), the output cap (13) and the family slug (23).
func (e *devinCatalogEntry) decodeModelInfo(raw []byte) error {
	fields, err := pbFields(raw)
	if err != nil {
		return err
	}
	for _, f := range fields {
		switch {
		case f.field == 4 && f.wire == pbVarint && f.num > 0:
			e.contextWindow = int(f.num)
		case f.field == 13 && f.wire == pbVarint && f.num > 0:
			e.maxOutput = int(f.num)
		case f.field == 23 && f.wire == pbBytes:
			e.family = f.text()
		}
	}
	return nil
}

// decodeDevinUserJWT reads a GetUserJwtResponse: the JWT and, for accounts
// served by a dedicated deployment, the API server their chat goes to.
func decodeDevinUserJWT(payload []byte) (jwt, apiServer string, err error) {
	fields, err := pbFields(payload)
	if err != nil {
		return "", "", err
	}
	for _, f := range fields {
		if f.wire != pbBytes {
			continue
		}
		switch f.field {
		case 1:
			jwt = f.text()
		case 2:
			apiServer = f.text()
		}
	}
	return jwt, apiServer, nil
}

// connectCodeStatus maps a Connect error code to the HTTP status the retry
// classification of this package understands
// (https://connectrpc.com/docs/protocol#error-codes).
func connectCodeStatus(code string) int {
	switch code {
	case "canceled":
		return 499
	case "invalid_argument", "failed_precondition", "out_of_range":
		return 400
	case "unauthenticated":
		return 401
	case "permission_denied":
		return 403
	case "not_found":
		return 404
	case "already_exists", "aborted":
		return 409
	case "resource_exhausted":
		return 429
	case "unimplemented":
		return 501
	case "unavailable":
		return 503
	case "deadline_exceeded":
		return 504
	case "unknown", "internal", "data_loss":
		return 500
	}
	if n, err := strconv.Atoi(code); err == nil {
		return n
	}
	return 0
}
