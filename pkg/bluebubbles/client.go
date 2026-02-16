package bluebubbles

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

type Client struct {
	baseURL  string
	password string
	http     *http.Client
}

type ClientOptions struct {
	Timeout time.Duration
}

func NewClient(serverURL, password string, opts ClientOptions) (*Client, error) {
	base := strings.TrimSpace(serverURL)
	if base == "" {
		return nil, fmt.Errorf("bluebubbles: server_url is required")
	}
	if !strings.HasPrefix(strings.ToLower(base), "http://") && !strings.HasPrefix(strings.ToLower(base), "https://") {
		base = "http://" + base
	}
	base = strings.TrimRight(base, "/")

	to := opts.Timeout
	if to == 0 {
		to = 10 * time.Second
	}

	return &Client{
		baseURL:  base,
		password: strings.TrimSpace(password),
		http:     &http.Client{Timeout: to},
	}, nil
}

func (c *Client) buildURL(path string) (string, error) {
	u, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(strings.TrimPrefix(path, "/"))
	if err != nil {
		return "", err
	}
	u = u.ResolveReference(ref)
	if c.password != "" {
		q := u.Query()
		q.Set("password", c.password)
		u.RawQuery = q.Encode()
	}
	return u.String(), nil
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) (*http.Response, []byte, error) {
	u, err := c.buildURL(path)
	if err != nil {
		return nil, nil, err
	}
	var reader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, nil, err
		}
		reader = bytes.NewReader(data)
	}

	req, err := http.NewRequestWithContext(ctx, method, u, reader)
	if err != nil {
		return nil, nil, err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		// Caller reads body only via returned bytes; ensure it's drained.
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, b, fmt.Errorf("bluebubbles: %s %s failed (%d): %s", method, path, resp.StatusCode, truncate(string(b), 300))
	}
	if out != nil && len(b) > 0 {
		if err := json.Unmarshal(b, out); err != nil {
			return resp, b, err
		}
	}
	return resp, b, nil
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "..."
}

func extractMessageID(payload any) string {
	if payload == nil {
		return "ok"
	}
	m, ok := payload.(map[string]any)
	if !ok {
		return "ok"
	}
	dataAny, _ := m["data"]
	data, _ := dataAny.(map[string]any)
	candidates := []any{
		m["messageId"], m["messageGuid"], m["message_guid"], m["guid"], m["id"],
	}
	if data != nil {
		candidates = append(candidates, data["messageId"], data["messageGuid"], data["message_guid"], data["message_id"], data["guid"], data["id"])
	}
	for _, c := range candidates {
		switch v := c.(type) {
		case string:
			if strings.TrimSpace(v) != "" {
				return strings.TrimSpace(v)
			}
		case float64:
			if v == float64(int64(v)) {
				return fmt.Sprintf("%.0f", v)
			}
		}
	}
	return "ok"
}

// ResolveChatGUID resolves various targets (handle/chat_id/chat_guid/chat_identifier) to a chat GUID.
// For chat_guid targets, it returns the GUID directly.
func (c *Client) ResolveChatGUID(ctx context.Context, target string) (string, error) {
	t, err := ParseTarget(target)
	if err != nil {
		return "", err
	}
	if t.Kind == TargetChatGUID {
		return t.ChatGUID, nil
	}

	// Query chats in pages and attempt to match.
	type chatRecord map[string]any
	type queryResp struct {
		Data []chatRecord `json:"data"`
	}

	normalizedHandle := ""
	if t.Kind == TargetHandle {
		normalizedHandle = NormalizeHandle(t.Handle)
	}

	const limit = 500
	for offset := 0; offset < 5000; offset += limit {
		var resp queryResp
		_, _, err := c.doJSON(ctx, http.MethodPost, "/api/v1/chat/query", map[string]any{
			"limit":  limit,
			"offset": offset,
			"with":   []string{"participants"},
		}, &resp)
		if err != nil {
			// If query fails, stop early.
			break
		}
		if len(resp.Data) == 0 {
			break
		}

		for _, chat := range resp.Data {
			chatGUID := firstString(chat, "chatGuid", "guid", "chat_guid", "identifier", "chatIdentifier", "chat_identifier")
			chatIdentifier := firstString(chat, "identifier", "chatIdentifier", "chat_identifier")
			chatID := firstInt64(chat, "chatId", "id", "chat_id")

			switch t.Kind {
			case TargetChatID:
				if chatID != 0 && chatID == t.ChatID && chatGUID != "" {
					return chatGUID, nil
				}
			case TargetChatIdentifier:
				if t.ChatIdentifier != "" && chatIdentifier != "" && strings.EqualFold(chatIdentifier, t.ChatIdentifier) && chatGUID != "" {
					return chatGUID, nil
				}
			case TargetHandle:
				// For DMs, prefer a chatGuid with ";-;" separator and participant match.
				if chatGUID != "" && strings.Contains(chatGUID, ";-;") {
					if participantIncludes(chat, normalizedHandle) {
						return chatGUID, nil
					}
				}
			}
		}
	}

	return "", nil
}

// MarkChatRead marks the given chat GUID as read.
// BlueBubbles endpoint: POST /api/v1/chat/:chatGuid/read
func (c *Client) MarkChatRead(ctx context.Context, chatGUID string) error {
	chatGUID = strings.TrimSpace(chatGUID)
	if chatGUID == "" {
		return fmt.Errorf("bluebubbles: chat_guid is required")
	}

	path := "/api/v1/chat/" + url.PathEscape(chatGUID) + "/read"
	_, _, err := c.doJSON(ctx, http.MethodPost, path, nil, nil)
	return err
}

func participantIncludes(chat map[string]any, normalizedHandle string) bool {
	raw, ok := chat["participants"]
	if !ok || normalizedHandle == "" {
		return false
	}
	arr, ok := raw.([]any)
	if !ok {
		return false
	}
	for _, entry := range arr {
		switch v := entry.(type) {
		case string:
			if NormalizeHandle(v) == normalizedHandle {
				return true
			}
		case map[string]any:
			if s, ok := v["address"].(string); ok && NormalizeHandle(s) == normalizedHandle {
				return true
			}
			if s, ok := v["handle"].(string); ok && NormalizeHandle(s) == normalizedHandle {
				return true
			}
			if s, ok := v["id"].(string); ok && NormalizeHandle(s) == normalizedHandle {
				return true
			}
		}
	}
	return false
}

func firstString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func firstInt64(m map[string]any, keys ...string) int64 {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				return int64(n)
			case int64:
				return n
			case int:
				return int64(n)
			case json.Number:
				i, _ := n.Int64()
				return i
			}
		}
	}
	return 0
}

// CreateNewChatWithMessage creates a new DM chat (requires BlueBubbles Private API).
func (c *Client) CreateNewChatWithMessage(ctx context.Context, address, message string) (SendResult, error) {
	var payload any = map[string]any{
		"addresses": []string{NormalizeHandle(address)},
		"message":   message,
		"tempGuid":  "temp-" + uuid.New().String(),
	}
	var out map[string]any
	_, _, err := c.doJSON(ctx, http.MethodPost, "/api/v1/chat/new", payload, &out)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: extractMessageID(out)}, nil
}

// SendText sends a message to a target. Target may be a handle or chat_* id.
// Optional: replyToMessageGUID/partIndex and effect (screen/bubble effect).
func (c *Client) SendText(ctx context.Context, target, text string, opts SendTextOptions) (SendResult, error) {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return SendResult{}, fmt.Errorf("bluebubbles: message text is required")
	}

	chatGUID, err := c.ResolveChatGUID(ctx, target)
	if err != nil {
		return SendResult{}, err
	}
	if chatGUID == "" {
		// For handles, attempt to create a new DM chat.
		t, err := ParseTarget(target)
		if err == nil && t.Kind == TargetHandle {
			return c.CreateNewChatWithMessage(ctx, t.Handle, trimmed)
		}
		return SendResult{}, fmt.Errorf("bluebubbles: chat_guid not found for target %q", target)
	}

	effectID := resolveEffectID(opts.Effect)
	needsPrivate := opts.ReplyToMessageGUID != "" || effectID != ""

	payload := map[string]any{
		"chatGuid": chatGUID,
		"tempGuid": uuid.New().String(),
		"message":  trimmed,
	}
	if needsPrivate {
		payload["method"] = "private-api"
	}
	if opts.ReplyToMessageGUID != "" {
		payload["selectedMessageGuid"] = strings.TrimSpace(opts.ReplyToMessageGUID)
		if opts.PartIndex >= 0 {
			payload["partIndex"] = opts.PartIndex
		} else {
			payload["partIndex"] = 0
		}
	}
	if effectID != "" {
		payload["effectId"] = effectID
	}

	var out map[string]any
	_, _, err = c.doJSON(ctx, http.MethodPost, "/api/v1/message/text", payload, &out)
	if err != nil {
		return SendResult{}, err
	}
	return SendResult{MessageID: extractMessageID(out)}, nil
}

type SendTextOptions struct {
	ReplyToMessageGUID string
	PartIndex          int
	Effect             string
}

var effectMap = map[string]string{
	// Bubble effects
	"slam":          "com.apple.MobileSMS.expressivesend.impact",
	"loud":          "com.apple.MobileSMS.expressivesend.loud",
	"gentle":        "com.apple.MobileSMS.expressivesend.gentle",
	"invisibleink":  "com.apple.MobileSMS.expressivesend.invisibleink",
	"invisible-ink": "com.apple.MobileSMS.expressivesend.invisibleink",
	"invisible ink": "com.apple.MobileSMS.expressivesend.invisibleink",
	"invisible":     "com.apple.MobileSMS.expressivesend.invisibleink",
	// Screen effects
	"echo":        "com.apple.messages.effect.CKEchoEffect",
	"spotlight":   "com.apple.messages.effect.CKSpotlightEffect",
	"balloons":    "com.apple.messages.effect.CKHappyBirthdayEffect",
	"confetti":    "com.apple.messages.effect.CKConfettiEffect",
	"love":        "com.apple.messages.effect.CKHeartEffect",
	"heart":       "com.apple.messages.effect.CKHeartEffect",
	"hearts":      "com.apple.messages.effect.CKHeartEffect",
	"lasers":      "com.apple.messages.effect.CKLasersEffect",
	"fireworks":   "com.apple.messages.effect.CKFireworksEffect",
	"celebration": "com.apple.messages.effect.CKSparklesEffect",
}

func resolveEffectID(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	lower := strings.ToLower(trimmed)
	if v, ok := effectMap[lower]; ok {
		return v
	}
	normalized := strings.NewReplacer(" ", "-", "_", "-").Replace(lower)
	if v, ok := effectMap[normalized]; ok {
		return v
	}
	compact := regexp.MustCompile(`[\s_-]+`).ReplaceAllString(lower, "")
	if v, ok := effectMap[compact]; ok {
		return v
	}
	return trimmed
}

func (c *Client) React(ctx context.Context, chatGUID, messageGUID, emoji string, remove bool, partIndex int) error {
	reaction, err := NormalizeReactionInput(emoji, remove)
	if err != nil {
		return err
	}
	if strings.TrimSpace(chatGUID) == "" {
		return fmt.Errorf("bluebubbles: chat_guid is required")
	}
	if strings.TrimSpace(messageGUID) == "" {
		return fmt.Errorf("bluebubbles: message_guid is required")
	}
	if partIndex < 0 {
		partIndex = 0
	}
	payload := map[string]any{
		"chatGuid":            strings.TrimSpace(chatGUID),
		"selectedMessageGuid": strings.TrimSpace(messageGUID),
		"reaction":            reaction,
		"partIndex":           partIndex,
	}
	_, _, err = c.doJSON(ctx, http.MethodPost, "/api/v1/message/react", payload, nil)
	return err
}

func (c *Client) Edit(ctx context.Context, messageGUID, newText string, partIndex int, backwardsCompat string) error {
	trimmed := strings.TrimSpace(messageGUID)
	if trimmed == "" {
		return fmt.Errorf("bluebubbles: message_guid is required")
	}
	text := strings.TrimSpace(newText)
	if text == "" {
		return fmt.Errorf("bluebubbles: new text is required")
	}
	if partIndex < 0 {
		partIndex = 0
	}
	if backwardsCompat == "" {
		backwardsCompat = "Edited to: " + text
	}
	payload := map[string]any{
		"editedMessage":                 text,
		"backwardsCompatibilityMessage": backwardsCompat,
		"partIndex":                     partIndex,
	}
	path := fmt.Sprintf("/api/v1/message/%s/edit", url.PathEscape(trimmed))
	_, _, err := c.doJSON(ctx, http.MethodPost, path, payload, nil)
	return err
}

func (c *Client) Unsend(ctx context.Context, messageGUID string, partIndex int) error {
	trimmed := strings.TrimSpace(messageGUID)
	if trimmed == "" {
		return fmt.Errorf("bluebubbles: message_guid is required")
	}
	if partIndex < 0 {
		partIndex = 0
	}
	payload := map[string]any{
		"partIndex": partIndex,
	}
	path := fmt.Sprintf("/api/v1/message/%s/unsend", url.PathEscape(trimmed))
	_, _, err := c.doJSON(ctx, http.MethodPost, path, payload, nil)
	return err
}

// DownloadAttachment downloads an attachment by GUID.
func (c *Client) DownloadAttachment(ctx context.Context, guid string, maxBytes int64) ([]byte, string, error) {
	trimmed := strings.TrimSpace(guid)
	if trimmed == "" {
		return nil, "", fmt.Errorf("bluebubbles: attachment guid is required")
	}
	u, err := c.buildURL(fmt.Sprintf("/api/v1/attachment/%s/download", url.PathEscape(trimmed)))
	if err != nil {
		return nil, "", err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return nil, "", fmt.Errorf("bluebubbles: attachment download failed (%d): %s", resp.StatusCode, truncate(string(b), 200))
	}
	ct := resp.Header.Get("Content-Type")
	if maxBytes <= 0 {
		maxBytes = 8 * 1024 * 1024
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", err
	}
	if int64(len(b)) > maxBytes {
		return nil, "", fmt.Errorf("bluebubbles: attachment too large (%d bytes)", len(b))
	}
	return b, ct, nil
}

// SaveAttachmentToTemp saves an attachment to a temp directory and returns the path.
func SaveAttachmentToTemp(buf []byte, filename string) (string, error) {
	if filename == "" {
		filename = "attachment"
	}
	safe := sanitizeFilename(filename)
	dir := filepath.Join(os.TempDir(), "picoclaw_media")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return "", err
	}
	p := filepath.Join(dir, uuid.New().String()[:8]+"_"+safe)
	if err := os.WriteFile(p, buf, 0600); err != nil {
		return "", err
	}
	return p, nil
}

func sanitizeFilename(name string) string {
	base := filepath.Base(strings.TrimSpace(name))
	if base == "" {
		return "attachment"
	}
	// Prevent header injection/odd paths.
	base = strings.NewReplacer("\r", "_", "\n", "_", "\"", "_", "\\", "_").Replace(base)
	return base
}

// SendAttachment uploads an attachment to a target.
// If buffer is nil, it reads the file at path.
func (c *Client) SendAttachment(ctx context.Context, target string, path string, buffer []byte, filename, contentType, caption string, opts SendAttachmentOptions) (SendResult, error) {
	if buffer == nil {
		if strings.TrimSpace(path) == "" {
			return SendResult{}, fmt.Errorf("bluebubbles: attachment requires path or buffer")
		}
		b, err := os.ReadFile(path)
		if err != nil {
			return SendResult{}, err
		}
		buffer = b
		if filename == "" {
			filename = filepath.Base(path)
		}
	}
	if filename == "" {
		filename = "attachment"
	}

	chatGUID, err := c.ResolveChatGUID(ctx, target)
	if err != nil {
		return SendResult{}, err
	}
	if chatGUID == "" {
		return SendResult{}, fmt.Errorf("bluebubbles: chat_guid not found for target %q", target)
	}

	u, err := c.buildURL("/api/v1/message/attachment")
	if err != nil {
		return SendResult{}, err
	}

	var body bytes.Buffer
	w := multipart.NewWriter(&body)

	// Required file field
	fw, err := w.CreateFormFile("attachment", sanitizeFilename(filename))
	if err != nil {
		return SendResult{}, err
	}
	if _, err := fw.Write(buffer); err != nil {
		return SendResult{}, err
	}

	_ = w.WriteField("chatGuid", chatGUID)
	_ = w.WriteField("name", sanitizeFilename(filename))
	_ = w.WriteField("tempGuid", "temp-"+uuid.New().String())
	_ = w.WriteField("method", "private-api")
	if opts.AsVoice {
		_ = w.WriteField("isAudioMessage", "true")
	}
	if strings.TrimSpace(opts.ReplyToMessageGUID) != "" {
		_ = w.WriteField("selectedMessageGuid", strings.TrimSpace(opts.ReplyToMessageGUID))
		part := opts.PartIndex
		if part < 0 {
			part = 0
		}
		_ = w.WriteField("partIndex", fmt.Sprintf("%d", part))
	}
	if strings.TrimSpace(caption) != "" {
		_ = w.WriteField("message", caption)
		_ = w.WriteField("text", caption)
		_ = w.WriteField("caption", caption)
	}

	if err := w.Close(); err != nil {
		return SendResult{}, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, &body)
	if err != nil {
		return SendResult{}, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	if contentType != "" {
		// Best-effort: BlueBubbles uses the uploaded file content-type inferred by server;
		// we include a header in case proxy respects it.
		req.Header.Set("X-File-Content-Type", contentType)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return SendResult{}, err
	}
	defer resp.Body.Close()

	respBytes, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return SendResult{}, fmt.Errorf("bluebubbles: send attachment failed (%d): %s", resp.StatusCode, truncate(string(respBytes), 300))
	}

	var parsed any
	_ = json.Unmarshal(respBytes, &parsed)
	return SendResult{MessageID: extractMessageID(parsed)}, nil
}

type SendAttachmentOptions struct {
	ReplyToMessageGUID string
	PartIndex          int
	AsVoice            bool
}
