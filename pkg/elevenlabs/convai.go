package elevenlabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// ConvAIClient is a minimal client for ElevenLabs Conversational AI (ConvAI) endpoints.
// It is intentionally small and focused on the "agent_call" flow used in OpenClaw.
type ConvAIClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type ConvAIClientOptions struct {
	Timeout time.Duration
}

func NewConvAIClient(baseURL, apiKey string, opts ConvAIClientOptions) (*ConvAIClient, error) {
	base := strings.TrimSpace(baseURL)
	if base == "" {
		base = "https://api.elevenlabs.io"
	}
	if !strings.HasPrefix(strings.ToLower(base), "http://") && !strings.HasPrefix(strings.ToLower(base), "https://") {
		base = "https://" + base
	}
	base = strings.TrimRight(base, "/")

	key := strings.TrimSpace(apiKey)
	if key == "" {
		return nil, fmt.Errorf("elevenlabs: api key is required")
	}

	to := opts.Timeout
	if to == 0 {
		to = 20 * time.Second
	}

	return &ConvAIClient{
		baseURL: base,
		apiKey:  key,
		http:    &http.Client{Timeout: to},
	}, nil
}

type TranscriptItem struct {
	Role    string `json:"role"`
	Message string `json:"message"`
}

type ConversationMetadata struct {
	CallDurationSecs float64 `json:"call_duration_secs"`
}

type Conversation struct {
	ConversationID string               `json:"conversation_id"`
	Status         string               `json:"status"`
	Transcript     []TranscriptItem     `json:"transcript"`
	Analysis       map[string]any       `json:"analysis"`
	Metadata       ConversationMetadata `json:"metadata"`
}

func (c *ConvAIClient) buildURL(path string) (string, error) {
	u, err := url.Parse(c.baseURL + "/")
	if err != nil {
		return "", err
	}
	ref, err := url.Parse(strings.TrimPrefix(path, "/"))
	if err != nil {
		return "", err
	}
	return u.ResolveReference(ref).String(), nil
}

func (c *ConvAIClient) doJSON(ctx context.Context, method, path string, body any, out any) (*http.Response, []byte, error) {
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
	req.Header.Set("xi-api-key", c.apiKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer func() {
		io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
	}()

	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return resp, nil, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return resp, b, fmt.Errorf("elevenlabs: %s %s failed (%d): %s", method, path, resp.StatusCode, truncate(string(b), 500))
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

func (c *ConvAIClient) OutboundAgentCall(ctx context.Context, agentID, agentPhoneNumberID, toNumber, firstMessage, callPlan string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	agentPhoneNumberID = strings.TrimSpace(agentPhoneNumberID)
	toNumber = strings.TrimSpace(toNumber)
	firstMessage = strings.TrimSpace(firstMessage)
	callPlan = strings.TrimSpace(callPlan)

	if agentID == "" || agentPhoneNumberID == "" {
		return "", fmt.Errorf("elevenlabs: agent_id and agent_phone_number_id are required")
	}
	if toNumber == "" {
		return "", fmt.Errorf("elevenlabs: to_number is required")
	}
	if firstMessage == "" || callPlan == "" {
		return "", fmt.Errorf("elevenlabs: first_message and call_plan are required")
	}

	type promptObj struct {
		Prompt string `json:"prompt"`
	}
	type agentOverride struct {
		FirstMessage string    `json:"first_message"`
		Prompt       promptObj `json:"prompt"`
	}
	type conversationOverride struct {
		Agent agentOverride `json:"agent"`
	}
	type initClientData struct {
		ConversationConfigOverride conversationOverride `json:"conversation_config_override"`
	}
	type outboundReq struct {
		AgentID                          string         `json:"agent_id"`
		AgentPhoneNumberID               string         `json:"agent_phone_number_id"`
		ToNumber                         string         `json:"to_number"`
		ConversationInitiationClientData initClientData `json:"conversation_initiation_client_data"`
	}
	type outboundResp struct {
		ConversationID string `json:"conversation_id"`
	}

	reqBody := outboundReq{
		AgentID:            agentID,
		AgentPhoneNumberID: agentPhoneNumberID,
		ToNumber:           toNumber,
		ConversationInitiationClientData: initClientData{
			ConversationConfigOverride: conversationOverride{
				Agent: agentOverride{
					FirstMessage: firstMessage,
					Prompt:       promptObj{Prompt: callPlan},
				},
			},
		},
	}

	var out outboundResp
	if _, _, err := c.doJSON(ctx, "POST", "/v1/convai/twilio/outbound-call", reqBody, &out); err != nil {
		return "", err
	}
	if strings.TrimSpace(out.ConversationID) == "" {
		return "", fmt.Errorf("elevenlabs: missing conversation_id in response")
	}
	return strings.TrimSpace(out.ConversationID), nil
}

func (c *ConvAIClient) GetConversation(ctx context.Context, conversationID string) (*Conversation, error) {
	conversationID = strings.TrimSpace(conversationID)
	if conversationID == "" {
		return nil, fmt.Errorf("elevenlabs: conversation id is required")
	}

	var out Conversation
	if _, _, err := c.doJSON(ctx, "GET", "/v1/convai/conversations/"+url.PathEscape(conversationID), nil, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
