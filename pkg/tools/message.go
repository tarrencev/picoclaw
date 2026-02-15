package tools

import (
	"context"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/sipeed/picoclaw/pkg/bluebubbles"
)

type SendCallback func(channel, chatID, content string) error

type MessageTool struct {
	sendCallback   SendCallback
	defaultChannel string
	defaultChatID  string
	sentInRound    bool // Tracks whether a message was sent in the current processing round

	bluebubbles *bluebubbles.Client
}

func NewMessageTool() *MessageTool {
	return &MessageTool{}
}

func (t *MessageTool) Name() string {
	return "message"
}

func (t *MessageTool) Description() string {
	return "Send a message to user on a chat channel. Use this when you want to communicate something."
}

func (t *MessageTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"content": map[string]interface{}{
				"type":        "string",
				"description": "The message content to send",
			},
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Optional: perform a channel-specific action (e.g., bluebubbles send/react/edit/unsend/reply/sendAttachment/sendWithEffect)",
			},
			"channel": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target channel (telegram, whatsapp, etc.)",
			},
			"chat_id": map[string]interface{}{
				"type":        "string",
				"description": "Optional: target chat/user ID",
			},
			"target": map[string]interface{}{
				"type":        "string",
				"description": "Optional: alias for chat_id (used by some channel skills, e.g. bluebubbles)",
			},
			"message": map[string]interface{}{
				"type":        "string",
				"description": "Optional: alias for content (used by some channel skills, e.g. bluebubbles)",
			},
			"text": map[string]interface{}{
				"type":        "string",
				"description": "Optional: alias for content/message (used by some channel skills)",
			},
			"messageId": map[string]interface{}{
				"type":        "string",
				"description": "Optional: message ID for react/edit/unsend/reply (bluebubbles)",
			},
			"replyTo": map[string]interface{}{
				"type":        "string",
				"description": "Optional: message ID to reply to (bluebubbles)",
			},
			"emoji": map[string]interface{}{
				"type":        "string",
				"description": "Optional: emoji/name for reactions (bluebubbles)",
			},
			"remove": map[string]interface{}{
				"type":        "boolean",
				"description": "Optional: remove reaction (bluebubbles)",
			},
			"effect": map[string]interface{}{
				"type":        "string",
				"description": "Optional: iMessage effect (e.g. balloons, slam) for sendWithEffect (bluebubbles)",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "Optional: local file path for sendAttachment (bluebubbles)",
			},
			"buffer": map[string]interface{}{
				"type":        "string",
				"description": "Optional: base64-encoded attachment buffer for sendAttachment (bluebubbles)",
			},
			"filename": map[string]interface{}{
				"type":        "string",
				"description": "Optional: attachment filename for sendAttachment (bluebubbles)",
			},
			"contentType": map[string]interface{}{
				"type":        "string",
				"description": "Optional: attachment content type for sendAttachment (bluebubbles)",
			},
			"caption": map[string]interface{}{
				"type":        "string",
				"description": "Optional: attachment caption for sendAttachment (bluebubbles)",
			},
			"partIndex": map[string]interface{}{
				"type":        "number",
				"description": "Optional: message part index for reply/react/edit/unsend (bluebubbles)",
			},
			"backwardsCompatMessage": map[string]interface{}{
				"type":        "string",
				"description": "Optional: backwards compatibility message for edit (bluebubbles)",
			},
			"asVoice": map[string]interface{}{
				"type":        "boolean",
				"description": "Optional: mark attachment as voice memo when sending audio (bluebubbles)",
			},
		},
	}
}

func (t *MessageTool) SetContext(channel, chatID string) {
	t.defaultChannel = channel
	t.defaultChatID = chatID
	t.sentInRound = false // Reset send tracking for new processing round
}

// HasSentInRound returns true if the message tool sent a message during the current round.
func (t *MessageTool) HasSentInRound() bool {
	return t.sentInRound
}

func (t *MessageTool) SetSendCallback(callback SendCallback) {
	t.sendCallback = callback
}

func (t *MessageTool) SetBlueBubblesClient(client *bluebubbles.Client) {
	t.bluebubbles = client
}

func (t *MessageTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	// Action-mode (channel-specific).
	if action, ok := args["action"].(string); ok && strings.TrimSpace(action) != "" {
		channel, _ := args["channel"].(string)
		chatID, _ := args["chat_id"].(string)
		target, _ := args["target"].(string)

		if channel == "" {
			channel = t.defaultChannel
		}
		if chatID == "" {
			chatID = t.defaultChatID
		}
		if target == "" {
			target = chatID
		}

		action = strings.TrimSpace(action)
		if strings.EqualFold(channel, "bluebubbles") {
			return t.executeBlueBubblesAction(ctx, action, target, args)
		}
		return &ToolResult{ForLLM: fmt.Sprintf("unsupported action channel: %s", channel), IsError: true}
	}

	// Plain send-mode (backwards compatible).
	content, ok := args["content"].(string)
	if !ok {
		// Allow callers to use message/text aliases without providing content.
		if v, ok := args["message"].(string); ok {
			content = v
		} else if v, ok := args["text"].(string); ok {
			content = v
		} else {
			return &ToolResult{ForLLM: "content is required", IsError: true}
		}
	}

	channel, _ := args["channel"].(string)
	chatID, _ := args["chat_id"].(string)
	if chatID == "" {
		if target, _ := args["target"].(string); target != "" {
			chatID = target
		}
	}

	if channel == "" {
		channel = t.defaultChannel
	}
	if chatID == "" {
		chatID = t.defaultChatID
	}

	if channel == "" || chatID == "" {
		return &ToolResult{ForLLM: "No target channel/chat specified", IsError: true}
	}

	if t.sendCallback == nil {
		return &ToolResult{ForLLM: "Message sending not configured", IsError: true}
	}

	if err := t.sendCallback(channel, chatID, content); err != nil {
		return &ToolResult{
			ForLLM:  fmt.Sprintf("sending message: %v", err),
			IsError: true,
			Err:     err,
		}
	}

	t.sentInRound = true
	// Silent: user already received the message directly
	return &ToolResult{
		ForLLM: fmt.Sprintf("Message sent to %s:%s", channel, chatID),
		Silent: true,
	}
}

func (t *MessageTool) executeBlueBubblesAction(ctx context.Context, action, target string, args map[string]interface{}) *ToolResult {
	if t.bluebubbles == nil {
		return &ToolResult{ForLLM: "bluebubbles not configured", IsError: true}
	}

	readString := func(key string) string {
		if v, ok := args[key].(string); ok {
			return v
		}
		return ""
	}
	readBool := func(key string) bool {
		if v, ok := args[key].(bool); ok {
			return v
		}
		if v, ok := args[key].(string); ok {
			switch strings.ToLower(strings.TrimSpace(v)) {
			case "true":
				return true
			case "false":
				return false
			}
		}
		return false
	}
	readInt := func(key string) int {
		if v, ok := args[key].(float64); ok {
			return int(v)
		}
		if v, ok := args[key].(int); ok {
			return v
		}
		return -1
	}

	// Common aliases
	text := readString("message")
	if text == "" {
		text = readString("text")
	}
	if text == "" {
		text = readString("content")
	}

	messageID := strings.TrimSpace(readString("messageId"))
	replyTo := strings.TrimSpace(readString("replyTo"))
	emoji := readString("emoji")
	remove := readBool("remove")
	effect := readString("effect")
	path := readString("path")
	bufB64 := readString("buffer")
	filename := readString("filename")
	contentType := readString("contentType")
	caption := readString("caption")
	partIndex := readInt("partIndex")
	backCompat := readString("backwardsCompatMessage")
	asVoice := readBool("asVoice")

	normalized := strings.ToLower(strings.TrimSpace(action))
	switch normalized {
	case "send", "sendmessage":
		res, err := t.bluebubbles.SendText(ctx, target, text, bluebubbles.SendTextOptions{PartIndex: 0})
		if err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: fmt.Sprintf("sent bluebubbles message (id=%s)", res.MessageID), Silent: true}

	case "react":
		chatGUID, err := t.bluebubbles.ResolveChatGUID(ctx, target)
		if err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		if chatGUID == "" {
			return &ToolResult{ForLLM: "bluebubbles react requires a resolvable chat_guid target", IsError: true}
		}
		if messageID == "" {
			return &ToolResult{ForLLM: "bluebubbles react requires messageId", IsError: true}
		}
		if err := t.bluebubbles.React(ctx, chatGUID, messageID, emoji, remove, partIndex); err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: "reaction sent", Silent: true}

	case "edit":
		if messageID == "" {
			return &ToolResult{ForLLM: "bluebubbles edit requires messageId", IsError: true}
		}
		if text == "" {
			return &ToolResult{ForLLM: "bluebubbles edit requires text/message", IsError: true}
		}
		if err := t.bluebubbles.Edit(ctx, messageID, text, partIndex, backCompat); err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: "edited", Silent: true}

	case "unsend":
		if messageID == "" {
			return &ToolResult{ForLLM: "bluebubbles unsend requires messageId", IsError: true}
		}
		if err := t.bluebubbles.Unsend(ctx, messageID, partIndex); err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: "unsent", Silent: true}

	case "reply":
		replyToID := replyTo
		if replyToID == "" {
			replyToID = messageID
		}
		if replyToID == "" {
			return &ToolResult{ForLLM: "bluebubbles reply requires replyTo or messageId", IsError: true}
		}
		res, err := t.bluebubbles.SendText(ctx, target, text, bluebubbles.SendTextOptions{
			ReplyToMessageGUID: replyToID,
			PartIndex:          partIndex,
		})
		if err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: fmt.Sprintf("replied (id=%s)", res.MessageID), Silent: true}

	case "sendattachment":
		var buf []byte
		if strings.TrimSpace(bufB64) != "" {
			decoded, err := base64.StdEncoding.DecodeString(bufB64)
			if err != nil {
				return &ToolResult{ForLLM: fmt.Sprintf("invalid base64 buffer: %v", err), IsError: true, Err: err}
			}
			buf = decoded
		}
		replyToID := replyTo
		if replyToID == "" {
			replyToID = messageID
		}
		res, err := t.bluebubbles.SendAttachment(ctx, target, path, buf, filename, contentType, caption, bluebubbles.SendAttachmentOptions{
			ReplyToMessageGUID: replyToID,
			PartIndex:          partIndex,
			AsVoice:            asVoice,
		})
		if err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: fmt.Sprintf("attachment sent (id=%s)", res.MessageID), Silent: true}

	case "sendwitheffect", "send_with_effect":
		res, err := t.bluebubbles.SendText(ctx, target, text, bluebubbles.SendTextOptions{
			PartIndex: 0,
			Effect:    effect,
		})
		if err != nil {
			return &ToolResult{ForLLM: err.Error(), IsError: true, Err: err}
		}
		t.sentInRound = true
		return &ToolResult{ForLLM: fmt.Sprintf("sent with effect (id=%s)", res.MessageID), Silent: true}

	default:
		return &ToolResult{ForLLM: fmt.Sprintf("unsupported bluebubbles action: %s", action), IsError: true}
	}
}
