package channels

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/bluebubbles"
	"github.com/sipeed/picoclaw/pkg/bus"
	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/httpserver"
	"github.com/sipeed/picoclaw/pkg/logger"
)

type BlueBubblesChannel struct {
	*BaseChannel
	config     config.BlueBubblesConfig
	client     *bluebubbles.Client
	httpServer *httpserver.Server
}

func NewBlueBubblesChannel(cfg config.BlueBubblesConfig, bus *bus.MessageBus, httpServer *httpserver.Server) (*BlueBubblesChannel, error) {
	if httpServer == nil {
		return nil, fmt.Errorf("shared HTTP server not configured")
	}
	if strings.TrimSpace(cfg.ServerURL) == "" {
		return nil, fmt.Errorf("bluebubbles server_url is required")
	}
	if strings.TrimSpace(cfg.Password) == "" {
		return nil, fmt.Errorf("bluebubbles password is required")
	}

	client, err := bluebubbles.NewClient(cfg.ServerURL, cfg.Password, bluebubbles.ClientOptions{})
	if err != nil {
		return nil, err
	}

	// We do access control checks ourselves (DM/group policies), so BaseChannel allowlist is empty.
	base := NewBaseChannel("bluebubbles", cfg, bus, []string{})

	return &BlueBubblesChannel{
		BaseChannel: base,
		config:      cfg,
		client:      client,
		httpServer:  httpServer,
	}, nil
}

func (c *BlueBubblesChannel) Start(ctx context.Context) error {
	path := strings.TrimSpace(c.config.WebhookPath)
	if path == "" {
		path = "/bluebubbles-webhook"
	}
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}

	if err := c.httpServer.HandleFunc(path, c.webhookHandler); err != nil {
		return err
	}

	logger.InfoCF("bluebubbles", "BlueBubbles webhook registered", map[string]interface{}{"path": path})
	c.setRunning(true)
	return nil
}

func (c *BlueBubblesChannel) Stop(ctx context.Context) error {
	c.setRunning(false)
	return nil
}

func (c *BlueBubblesChannel) Send(ctx context.Context, msg bus.OutboundMessage) error {
	_, err := c.client.SendText(ctx, msg.ChatID, msg.Content, bluebubbles.SendTextOptions{
		PartIndex: 0,
	})
	return err
}

func (c *BlueBubblesChannel) webhookHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	if !c.isAuthorized(r) {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	eventType, _ := payload["type"].(string)
	eventType = strings.TrimSpace(eventType)

	// Normalize payload into either message or reaction.
	reaction := normalizeWebhookReaction(payload)
	if reaction != nil {
		// Ignore self-generated reactions.
		if reaction.FromMe {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}
		if !c.allowedInbound(reaction.SenderID, reaction.IsGroup, reaction.ChatID, reaction.ChatGUID, reaction.ChatIdentifier) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
			return
		}

		chatID := reaction.SenderID
		if reaction.IsGroup {
			chatID = formatChatTarget(reaction.ChatGUID, reaction.ChatIdentifier, reaction.ChatID)
		}
		metadata := map[string]string{
			"event_type":      eventType,
			"message_id":      reaction.MessageID,
			"chat_guid":       reaction.ChatGUID,
			"chat_identifier": reaction.ChatIdentifier,
			"chat_id":         formatInt64Ptr(reaction.ChatID),
			"is_group":        strconv.FormatBool(reaction.IsGroup),
			"sender_name":     reaction.SenderName,
		}
		content := fmt.Sprintf("[reaction] %s %s %s", reaction.SenderID, reaction.Action, reaction.Emoji)
		c.HandleMessage(reaction.SenderID, chatID, content, nil, metadata)

		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	msg := normalizeWebhookMessage(payload)
	if msg == nil {
		// Best-effort: accept but do nothing
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	// Ignore messages sent by ourselves.
	if msg.FromMe {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	if !c.allowedInbound(msg.SenderID, msg.IsGroup, msg.ChatID, msg.ChatGUID, msg.ChatIdentifier) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
		return
	}

	chatID := msg.SenderID
	if msg.IsGroup {
		chatID = formatChatTarget(msg.ChatGUID, msg.ChatIdentifier, msg.ChatID)
	}

	mediaPaths := make([]string, 0, len(msg.Attachments))
	for _, att := range msg.Attachments {
		if strings.TrimSpace(att.GUID) == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		buf, ct, err := c.client.DownloadAttachment(ctx, att.GUID, 8*1024*1024)
		cancel()
		if err != nil {
			logger.WarnCF("bluebubbles", "Attachment download failed", map[string]interface{}{
				"guid":  att.GUID,
				"error": err.Error(),
			})
			continue
		}
		filename := strings.TrimSpace(att.TransferName)
		if filename == "" {
			filename = "attachment"
		}
		if ct != "" && strings.Contains(ct, "/") && !strings.Contains(filename, ".") {
			// Best-effort extension for unknown filenames.
			parts := strings.SplitN(ct, "/", 2)
			if len(parts) == 2 && parts[1] != "" {
				filename = filename + "." + parts[1]
			}
		}
		p, err := bluebubbles.SaveAttachmentToTemp(buf, filename)
		if err == nil && p != "" {
			mediaPaths = append(mediaPaths, p)
		}
	}

	content := strings.TrimSpace(msg.Text)
	if content == "" && len(mediaPaths) > 0 {
		content = "[attachment]"
	}

	metadata := map[string]string{
		"event_type":      eventType,
		"message_id":      msg.MessageID,
		"chat_guid":       msg.ChatGUID,
		"chat_identifier": msg.ChatIdentifier,
		"chat_id":         formatInt64Ptr(msg.ChatID),
		"is_group":        strconv.FormatBool(msg.IsGroup),
		"sender_name":     msg.SenderName,
	}

	c.HandleMessage(msg.SenderID, chatID, content, mediaPaths, metadata)

	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (c *BlueBubblesChannel) isAuthorized(r *http.Request) bool {
	token := strings.TrimSpace(c.config.Password)
	if token == "" {
		return true
	}

	// Accept either query param or a handful of header names (parity with OpenClaw).
	q := r.URL.Query()
	candidates := []string{
		q.Get("guid"),
		q.Get("password"),
		r.Header.Get("x-guid"),
		r.Header.Get("x-password"),
		r.Header.Get("x-bluebubbles-guid"),
		r.Header.Get("authorization"),
	}
	for _, v := range candidates {
		v = strings.TrimSpace(v)
		if v == "" {
			continue
		}
		// Allow "Bearer <token>" form.
		if strings.HasPrefix(strings.ToLower(v), "bearer ") {
			v = strings.TrimSpace(v[7:])
		}
		if v == token {
			return true
		}
	}
	return false
}

func (c *BlueBubblesChannel) allowedInbound(sender string, isGroup bool, chatID *int64, chatGUID, chatIdentifier string) bool {
	if isGroup {
		policy := strings.ToLower(strings.TrimSpace(c.config.GroupPolicy))
		if policy == "" {
			policy = "allowlist"
		}
		switch policy {
		case "disabled":
			return false
		case "open":
			return true
		default: // allowlist
			if len(c.config.GroupAllowFrom) == 0 {
				return false
			}
			return isAllowedBlueBubblesSender([]string(c.config.GroupAllowFrom), sender, chatID, chatGUID, chatIdentifier)
		}
	}

	policy := strings.ToLower(strings.TrimSpace(c.config.DmPolicy))
	if policy == "" {
		policy = "pairing"
	}
	switch policy {
	case "disabled":
		return false
	case "open":
		return true
	default: // allowlist or pairing
		if len(c.config.AllowFrom) == 0 {
			return true
		}
		allowed := isAllowedBlueBubblesSender([]string(c.config.AllowFrom), sender, chatID, chatGUID, chatIdentifier)
		if allowed {
			return true
		}
		if policy == "pairing" {
			// Best-effort pairing hint.
			go func() {
				ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer cancel()
				_, _ = c.client.SendText(ctx, sender, "Pairing required: ask the owner to allowlist your sender id in PicoClaw (channels.bluebubbles.allow_from).", bluebubbles.SendTextOptions{PartIndex: 0})
			}()
		}
		return false
	}
}

func isAllowedBlueBubblesSender(allowFrom []string, sender string, chatID *int64, chatGUID, chatIdentifier string) bool {
	if len(allowFrom) == 0 {
		return true
	}
	senderNorm := bluebubbles.NormalizeHandle(sender)
	for _, raw := range allowFrom {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			continue
		}
		if entry == "*" {
			return true
		}
		kind, value := parseAllowEntry(entry)
		switch kind {
		case "chat_id":
			if chatID != nil && *chatID != 0 {
				if n, err := strconv.ParseInt(value, 10, 64); err == nil && n == *chatID {
					return true
				}
			}
		case "chat_guid":
			if chatGUID != "" && value != "" && value == chatGUID {
				return true
			}
		case "chat_identifier":
			if chatIdentifier != "" && value != "" && strings.EqualFold(value, chatIdentifier) {
				return true
			}
		case "handle":
			if senderNorm != "" && bluebubbles.NormalizeHandle(value) == senderNorm {
				return true
			}
		}
	}
	return false
}

func parseAllowEntry(entry string) (kind string, value string) {
	trimmed := strings.TrimSpace(entry)
	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "chat_id:"):
		return "chat_id", strings.TrimSpace(trimmed[len("chat_id:"):])
	case strings.HasPrefix(lower, "chat_guid:"):
		return "chat_guid", strings.TrimSpace(trimmed[len("chat_guid:"):])
	case strings.HasPrefix(lower, "chat_identifier:"):
		return "chat_identifier", strings.TrimSpace(trimmed[len("chat_identifier:"):])
	default:
		trimmed = strings.TrimPrefix(trimmed, "bluebubbles:")
		return "handle", strings.TrimSpace(trimmed)
	}
}

func formatChatTarget(chatGUID, chatIdentifier string, chatID *int64) string {
	if strings.TrimSpace(chatGUID) != "" {
		return "chat_guid:" + strings.TrimSpace(chatGUID)
	}
	if strings.TrimSpace(chatIdentifier) != "" {
		return "chat_identifier:" + strings.TrimSpace(chatIdentifier)
	}
	if chatID != nil && *chatID != 0 {
		return fmt.Sprintf("chat_id:%d", *chatID)
	}
	return ""
}

func formatInt64Ptr(v *int64) string {
	if v == nil {
		return ""
	}
	return fmt.Sprintf("%d", *v)
}

// -----------------------------------------------------------------------------
// Webhook payload normalization (subset of OpenClaw bluebubbles monitor)
// -----------------------------------------------------------------------------

type webhookMessage struct {
	Text           string
	SenderID       string
	SenderName     string
	MessageID      string
	IsGroup        bool
	ChatID         *int64
	ChatGUID       string
	ChatIdentifier string
	FromMe         bool
	Attachments    []bluebubbles.Attachment
}

type webhookReaction struct {
	Action         string
	Emoji          string
	SenderID       string
	SenderName     string
	MessageID      string
	IsGroup        bool
	ChatID         *int64
	ChatGUID       string
	ChatIdentifier string
	FromMe         bool
}

func asMap(v any) map[string]any {
	if v == nil {
		return nil
	}
	m, _ := v.(map[string]any)
	return m
}

func extractMessagePayload(payload map[string]any) map[string]any {
	dataRaw := payload["data"]
	if dataRaw == nil {
		dataRaw = payload["payload"]
	}
	if dataRaw == nil {
		dataRaw = payload["event"]
	}
	data := asMap(dataRaw)
	if data == nil {
		if s, ok := dataRaw.(string); ok && strings.TrimSpace(s) != "" {
			_ = json.Unmarshal([]byte(s), &data)
		}
	}
	messageRaw := payload["message"]
	if messageRaw == nil && data != nil {
		if v := data["message"]; v != nil {
			messageRaw = v
		} else {
			messageRaw = data
		}
	}
	msg := asMap(messageRaw)
	if msg == nil {
		if s, ok := messageRaw.(string); ok && strings.TrimSpace(s) != "" {
			_ = json.Unmarshal([]byte(s), &msg)
		}
	}
	// Some BlueBubbles webhook formats keep chat fields (chatGuid, isGroup, chatId)
	// alongside the message object rather than inside it (e.g. payload.data.chatGuid).
	// Merge those wrapper fields into the message map so normalization can reliably
	// determine group routing.
	if msg != nil {
		mergeMessageWrapperFields(msg, payload)
		mergeMessageWrapperFields(msg, data)
	}
	return msg
}

func mergeIfMissing(dst map[string]any, src map[string]any, keys ...string) {
	if dst == nil || src == nil {
		return
	}
	for _, k := range keys {
		if _, ok := dst[k]; ok && dst[k] != nil {
			continue
		}
		if v, ok := src[k]; ok && v != nil {
			dst[k] = v
		}
	}
}

func mergeMessageWrapperFields(msg map[string]any, wrapper map[string]any) {
	if msg == nil || wrapper == nil {
		return
	}
	mergeIfMissing(msg, wrapper,
		"chatGuid", "chat_guid",
		"chatIdentifier", "chat_identifier",
		"chatId", "chat_id",
		"isGroup", "is_group", "group",
	)
	mergeIfMissing(msg, wrapper, "chat", "conversation", "handle", "sender")
}

func readString(m map[string]any, keys ...string) string {
	for _, k := range keys {
		if m == nil {
			return ""
		}
		if v, ok := m[k]; ok {
			if s, ok := v.(string); ok && strings.TrimSpace(s) != "" {
				return strings.TrimSpace(s)
			}
		}
	}
	return ""
}

func readBool(m map[string]any, keys ...string) *bool {
	for _, k := range keys {
		if m == nil {
			return nil
		}
		if v, ok := m[k]; ok {
			if b, ok := v.(bool); ok {
				return &b
			}
			if s, ok := v.(string); ok {
				switch strings.ToLower(strings.TrimSpace(s)) {
				case "true":
					b := true
					return &b
				case "false":
					b := false
					return &b
				}
			}
		}
	}
	return nil
}

func readInt64(m map[string]any, keys ...string) *int64 {
	for _, k := range keys {
		if m == nil {
			return nil
		}
		if v, ok := m[k]; ok {
			switch n := v.(type) {
			case float64:
				i := int64(n)
				return &i
			case int64:
				i := n
				return &i
			case json.Number:
				i, err := n.Int64()
				if err == nil {
					return &i
				}
			}
		}
	}
	return nil
}

func resolveGroupFlagFromChatGUID(chatGUID string) *bool {
	if strings.Contains(chatGUID, ";+;") {
		b := true
		return &b
	}
	if strings.Contains(chatGUID, ";-;") {
		b := false
		return &b
	}
	return nil
}

func normalizeWebhookMessage(payload map[string]any) *webhookMessage {
	msg := extractMessagePayload(payload)
	if msg == nil {
		return nil
	}

	text := readString(msg, "text", "body", "subject")

	handleVal := msg["handle"]
	if handleVal == nil {
		handleVal = msg["sender"]
	}
	handle := asMap(handleVal)
	if handle == nil {
		if s, ok := handleVal.(string); ok && s != "" {
			handle = map[string]any{"address": s}
		}
	}

	senderID := readString(handle, "address", "handle", "id")
	if senderID == "" {
		senderID = readString(msg, "senderId", "sender", "from")
	}
	senderID = bluebubbles.NormalizeHandle(senderID)
	if senderID == "" {
		return nil
	}
	senderName := readString(handle, "displayName", "name")
	if senderName == "" {
		senderName = readString(msg, "senderName")
	}

	chat := asMap(msg["chat"])
	if chat == nil {
		chat = asMap(msg["conversation"])
	}

	chatGUID := readString(msg, "chatGuid", "chat_guid")
	if chatGUID == "" {
		chatGUID = readString(chat, "chatGuid", "chat_guid", "guid")
	}
	chatIdentifier := readString(msg, "chatIdentifier", "chat_identifier")
	if chatIdentifier == "" {
		chatIdentifier = readString(chat, "chatIdentifier", "chat_identifier", "identifier")
	}
	chatID := readInt64(msg, "chatId", "chat_id")
	if chatID == nil {
		chatID = readInt64(chat, "chatId", "chat_id", "id")
	}

	groupFlag := resolveGroupFlagFromChatGUID(chatGUID)
	explicitIsGroup := readBool(msg, "isGroup", "is_group", "group")
	if explicitIsGroup == nil {
		explicitIsGroup = readBool(chat, "isGroup")
	}
	isGroup := false
	if groupFlag != nil {
		isGroup = *groupFlag
	} else if explicitIsGroup != nil {
		isGroup = *explicitIsGroup
	}

	fromMe := false
	if b := readBool(msg, "isFromMe", "is_from_me"); b != nil {
		fromMe = *b
	}

	messageID := readString(msg, "guid", "id", "messageId")

	atts := extractAttachments(msg)

	return &webhookMessage{
		Text:           text,
		SenderID:       senderID,
		SenderName:     senderName,
		MessageID:      messageID,
		IsGroup:        isGroup,
		ChatID:         chatID,
		ChatGUID:       chatGUID,
		ChatIdentifier: chatIdentifier,
		FromMe:         fromMe,
		Attachments:    atts,
	}
}

func normalizeWebhookReaction(payload map[string]any) *webhookReaction {
	msg := extractMessagePayload(payload)
	if msg == nil {
		return nil
	}

	associatedGUID := readString(msg, "associatedMessageGuid", "associated_message_guid", "associatedMessageId")
	associatedType := readInt64(msg, "associatedMessageType", "associated_message_type")
	if associatedGUID == "" || associatedType == nil {
		return nil
	}

	emoji := readString(msg, "associatedMessageEmoji", "associated_message_emoji", "reactionEmoji", "reaction_emoji")
	action := "added"
	if *associatedType >= 3000 && *associatedType < 4000 {
		action = "removed"
	}
	if emoji == "" {
		emoji = fmt.Sprintf("reaction:%d", *associatedType)
	}

	handleVal := msg["handle"]
	if handleVal == nil {
		handleVal = msg["sender"]
	}
	handle := asMap(handleVal)
	if handle == nil {
		if s, ok := handleVal.(string); ok && s != "" {
			handle = map[string]any{"address": s}
		}
	}
	senderID := readString(handle, "address", "handle", "id")
	if senderID == "" {
		senderID = readString(msg, "senderId", "sender", "from")
	}
	senderID = bluebubbles.NormalizeHandle(senderID)
	if senderID == "" {
		return nil
	}
	senderName := readString(handle, "displayName", "name")
	if senderName == "" {
		senderName = readString(msg, "senderName")
	}

	chat := asMap(msg["chat"])
	if chat == nil {
		chat = asMap(msg["conversation"])
	}
	chatGUID := readString(msg, "chatGuid", "chat_guid")
	if chatGUID == "" {
		chatGUID = readString(chat, "chatGuid", "chat_guid", "guid")
	}
	chatIdentifier := readString(msg, "chatIdentifier", "chat_identifier")
	if chatIdentifier == "" {
		chatIdentifier = readString(chat, "chatIdentifier", "chat_identifier", "identifier")
	}
	chatID := readInt64(msg, "chatId", "chat_id")
	if chatID == nil {
		chatID = readInt64(chat, "chatId", "chat_id", "id")
	}

	groupFlag := resolveGroupFlagFromChatGUID(chatGUID)
	isGroup := false
	if groupFlag != nil {
		isGroup = *groupFlag
	}

	fromMe := false
	if b := readBool(msg, "isFromMe", "is_from_me"); b != nil {
		fromMe = *b
	}

	return &webhookReaction{
		Action:         action,
		Emoji:          emoji,
		SenderID:       senderID,
		SenderName:     senderName,
		MessageID:      associatedGUID,
		IsGroup:        isGroup,
		ChatID:         chatID,
		ChatGUID:       chatGUID,
		ChatIdentifier: chatIdentifier,
		FromMe:         fromMe,
	}
}

func extractAttachments(msg map[string]any) []bluebubbles.Attachment {
	raw, ok := msg["attachments"]
	if !ok {
		return nil
	}
	arr, ok := raw.([]any)
	if !ok {
		return nil
	}
	out := make([]bluebubbles.Attachment, 0, len(arr))
	for _, entry := range arr {
		m, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		guid := readString(m, "guid")
		if guid == "" {
			continue
		}
		out = append(out, bluebubbles.Attachment{
			GUID:         guid,
			MimeType:     readString(m, "mimeType", "mime_type"),
			TransferName: readString(m, "transferName", "transfer_name", "name"),
		})
	}
	return out
}
