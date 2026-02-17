package channels

import "testing"

func TestNormalizeWebhookMessage_MergesWrapperChatFields(t *testing.T) {
	payload := map[string]any{
		"type": "new-message",
		"data": map[string]any{
			"chatGuid":       "iMessage;+;chat123",
			"chatIdentifier": "chat123",
			"message": map[string]any{
				"text":     "hello",
				"guid":     "MSG1",
				"isFromMe": false,
				"handle": map[string]any{
					"address": "+15551234567",
				},
			},
		},
	}

	msg := normalizeWebhookMessage(payload)
	if msg == nil {
		t.Fatal("normalizeWebhookMessage returned nil")
	}
	if msg.ChatGUID != "iMessage;+;chat123" {
		t.Errorf("ChatGUID = %q, want %q", msg.ChatGUID, "iMessage;+;chat123")
	}
	if !msg.IsGroup {
		t.Error("IsGroup = false, want true")
	}
}

