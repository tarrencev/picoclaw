package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/sipeed/picoclaw/pkg/config"
	"github.com/sipeed/picoclaw/pkg/elevenlabs"
)

// VoiceCallTool supports ElevenLabs ConvAI agent calls ("agent_call") plus transcript retrieval.
// This mirrors the custom OpenClaw voice-call extension behavior used in your deployment.
type VoiceCallTool struct {
	cfg config.VoiceCallsConfig
}

func NewVoiceCallTool(cfg config.VoiceCallsConfig) *VoiceCallTool {
	return &VoiceCallTool{cfg: cfg}
}

func (t *VoiceCallTool) Name() string {
	return "voice_call"
}

func (t *VoiceCallTool) Description() string {
	return "Make phone calls via ElevenLabs ConvAI agents. Preferred: action='agent_call' with first_message and call_plan. Use action='get_call_transcript' with conversation_id to retrieve results."
}

func (t *VoiceCallTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action": map[string]interface{}{
				"type":        "string",
				"description": "Action to perform: agent_call, get_call_transcript. (Other OpenClaw voice-call actions are not implemented in PicoClaw yet.)",
			},
			"to": map[string]interface{}{
				"type":        "string",
				"description": "Phone number to call (defaults to voice_calls.default_to_number).",
			},
			"first_message": map[string]interface{}{
				"type":        "string",
				"description": "What to say when the person picks up (agent_call).",
			},
			"call_plan": map[string]interface{}{
				"type":        "string",
				"description": "System prompt / plan for the AI agent to follow during the call (agent_call).",
			},
			"conversation_id": map[string]interface{}{
				"type":        "string",
				"description": "ElevenLabs conversation ID (get_call_transcript).",
			},
			// OpenClaw compatibility fields (not implemented yet)
			"message": map[string]interface{}{
				"type":        "string",
				"description": "OpenClaw compatibility: initiate_call intro message (not implemented).",
			},
			"mode": map[string]interface{}{
				"type":        "string",
				"description": "OpenClaw compatibility: call/status modes (not implemented).",
			},
			"sid": map[string]interface{}{
				"type":        "string",
				"description": "OpenClaw compatibility: call SID (not implemented).",
			},
			"callId": map[string]interface{}{
				"type":        "string",
				"description": "OpenClaw compatibility: callId for continue/end/status (not implemented).",
			},
		},
	}
}

func (t *VoiceCallTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	action := argString(args, "action")
	if action == "" {
		// Heuristic fallbacks for LLMs that omit "action".
		if argString(args, "conversation_id", "conversationId") != "" {
			action = "get_call_transcript"
		} else if argString(args, "first_message", "firstMessage") != "" || argString(args, "call_plan", "callPlan") != "" {
			action = "agent_call"
		}
	}

	switch strings.ToLower(strings.TrimSpace(action)) {
	case "agent_call":
		return t.agentCall(ctx, args)
	case "get_call_transcript":
		return t.getCallTranscript(ctx, args)
	case "":
		return ErrorResult("voice_call: action required (agent_call, get_call_transcript)")
	default:
		return ErrorResult(fmt.Sprintf("voice_call: unsupported action %q (supported: agent_call, get_call_transcript)", action))
	}
}

func (t *VoiceCallTool) agentCall(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !t.cfg.Enabled {
		return ErrorResult("voice_call: voice_calls is disabled (set voice_calls.enabled=true)")
	}

	firstMessage := argString(args, "first_message", "firstMessage")
	callPlan := argString(args, "call_plan", "callPlan")
	to := argString(args, "to")
	if to == "" {
		to = strings.TrimSpace(t.cfg.DefaultToNumber)
	}

	baseURL := strings.TrimSpace(t.cfg.TTS.ElevenLabs.BaseURL)
	apiKey := strings.TrimSpace(t.cfg.TTS.ElevenLabs.APIKey)

	client, err := elevenlabs.NewConvAIClient(baseURL, apiKey, elevenlabs.ConvAIClientOptions{Timeout: 20 * time.Second})
	if err != nil {
		return ErrorResult(fmt.Sprintf("voice_call: %v", err))
	}

	convID, err := client.OutboundAgentCall(ctx, t.cfg.ElevenLabsAgentID, t.cfg.ElevenLabsPhoneNumberID, to, firstMessage, callPlan)
	if err != nil {
		return ErrorResult(fmt.Sprintf("voice_call: %v", err))
	}

	// Poll for completion (matches the OpenClaw extension behavior).
	pollStart := time.Now()
	maxPoll := 10 * time.Minute
	pollInterval := 5 * time.Second
	initialDelay := 10 * time.Second

	callStatus := "initiated"
	var transcript []elevenlabs.TranscriptItem
	var analysis map[string]any
	var duration float64

	if !sleepCtx(ctx, initialDelay) {
		return NewToolResult(mustJSON(map[string]any{
			"success":         false,
			"status":          "cancelled",
			"conversation_id": convID,
			"message":         "Call started but polling was cancelled.",
		}))
	}

	for time.Since(pollStart) < maxPoll {
		conv, err := client.GetConversation(ctx, convID)
		if err == nil && conv != nil {
			if strings.TrimSpace(conv.Status) != "" {
				callStatus = strings.TrimSpace(conv.Status)
			}
			transcript = conv.Transcript
			analysis = conv.Analysis
			duration = conv.Metadata.CallDurationSecs

			if isTerminalCallStatus(callStatus) {
				break
			}
		}
		if !sleepCtx(ctx, pollInterval) {
			break
		}
	}

	formattedTranscript := formatTranscript(transcript)
	if formattedTranscript == "" {
		formattedTranscript = "(no transcript available)"
	}

	message := ""
	switch callStatus {
	case "done":
		message = "Call completed successfully."
	case "initiated", "in-progress":
		message = "Call is still in progress (polling timed out). Use get_call_transcript later to check the result."
	default:
		message = fmt.Sprintf("Call ended with status: %s", callStatus)
	}

	return NewToolResult(mustJSON(map[string]any{
		"success":          callStatus == "done",
		"status":           callStatus,
		"conversation_id":  convID,
		"duration_seconds": duration,
		"transcript":       formattedTranscript,
		"analysis":         analysis,
		"message":          message,
	}))
}

func (t *VoiceCallTool) getCallTranscript(ctx context.Context, args map[string]interface{}) *ToolResult {
	if !t.cfg.Enabled {
		return ErrorResult("voice_call: voice_calls is disabled (set voice_calls.enabled=true)")
	}

	convID := argString(args, "conversation_id", "conversationId")
	if convID == "" {
		return ErrorResult("voice_call: conversation_id required")
	}

	baseURL := strings.TrimSpace(t.cfg.TTS.ElevenLabs.BaseURL)
	apiKey := strings.TrimSpace(t.cfg.TTS.ElevenLabs.APIKey)
	client, err := elevenlabs.NewConvAIClient(baseURL, apiKey, elevenlabs.ConvAIClientOptions{Timeout: 20 * time.Second})
	if err != nil {
		return ErrorResult(fmt.Sprintf("voice_call: %v", err))
	}

	conv, err := client.GetConversation(ctx, convID)
	if err != nil {
		return ErrorResult(fmt.Sprintf("voice_call: %v", err))
	}

	transcript := make([]map[string]any, 0, len(conv.Transcript))
	for _, t := range conv.Transcript {
		transcript = append(transcript, map[string]any{
			"role":    t.Role,
			"message": t.Message,
		})
	}

	return NewToolResult(mustJSON(map[string]any{
		"status":           conv.Status,
		"transcript":       transcript,
		"analysis":         conv.Analysis,
		"duration_seconds": conv.Metadata.CallDurationSecs,
	}))
}

func argString(args map[string]interface{}, keys ...string) string {
	for _, k := range keys {
		if v, ok := args[k]; ok {
			switch x := v.(type) {
			case string:
				if s := strings.TrimSpace(x); s != "" {
					return s
				}
			case float64:
				// Common when LLMs emit numbers without quotes.
				if x == float64(int64(x)) {
					return fmt.Sprintf("%.0f", x)
				}
				return fmt.Sprintf("%v", x)
			case int:
				return fmt.Sprintf("%d", x)
			case int64:
				return fmt.Sprintf("%d", x)
			}
		}
	}
	return ""
}

func mustJSON(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		// Fall back to a best-effort string; still return something useful to the LLM.
		return fmt.Sprintf("{\"error\":\"json marshal failed: %s\"}", err.Error())
	}
	return string(b)
}

func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

func isTerminalCallStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "done", "failed", "error", "timeout", "no-answer", "busy":
		return true
	default:
		return false
	}
}

func formatTranscript(items []elevenlabs.TranscriptItem) string {
	if len(items) == 0 {
		return ""
	}
	lines := make([]string, 0, len(items))
	for _, it := range items {
		role := strings.TrimSpace(it.Role)
		name := "Caller"
		if role == "agent" {
			name = "Agent"
		} else if role != "" {
			// Keep any provider-sent role for debuggability.
			name = role
		}
		msg := strings.TrimSpace(it.Message)
		lines = append(lines, fmt.Sprintf("%s: %s", name, msg))
	}
	return strings.Join(lines, "\n")
}
