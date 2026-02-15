package bluebubbles

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

type TargetKind string

const (
	TargetHandle         TargetKind = "handle"
	TargetChatID         TargetKind = "chat_id"
	TargetChatGUID       TargetKind = "chat_guid"
	TargetChatIdentifier TargetKind = "chat_identifier"
)

type Target struct {
	Kind           TargetKind
	Handle         string
	ChatID         int64
	ChatGUID       string
	ChatIdentifier string
}

var (
	reChatDigits = regexp.MustCompile(`(?i)^chat\d+$`)
	reHexID      = regexp.MustCompile(`(?i)^[0-9a-f]{8,64}$`)
)

func NormalizeHandle(raw string) string {
	s := strings.TrimSpace(raw)
	lower := strings.ToLower(s)
	if strings.HasPrefix(lower, "bluebubbles:") {
		s = s[len("bluebubbles:"):]
	}
	return strings.TrimSpace(s)
}

func ParseTarget(raw string) (Target, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return Target{}, fmt.Errorf("empty target")
	}

	lower := strings.ToLower(trimmed)
	switch {
	case strings.HasPrefix(lower, "chat_guid:"):
		return Target{Kind: TargetChatGUID, ChatGUID: strings.TrimSpace(trimmed[len("chat_guid:"):])}, nil
	case strings.HasPrefix(lower, "chat_id:"):
		v := strings.TrimSpace(trimmed[len("chat_id:"):])
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return Target{}, fmt.Errorf("invalid chat_id: %w", err)
		}
		return Target{Kind: TargetChatID, ChatID: n}, nil
	case strings.HasPrefix(lower, "chat_identifier:"):
		return Target{Kind: TargetChatIdentifier, ChatIdentifier: strings.TrimSpace(trimmed[len("chat_identifier:"):])}, nil
	}

	// Handle BlueBubbles "chat123..." identifiers as chat_identifier (not numeric chat_id).
	if reChatDigits.MatchString(trimmed) {
		return Target{Kind: TargetChatIdentifier, ChatIdentifier: trimmed}, nil
	}
	// Raw hex identifiers also treated as chat_identifier.
	if reHexID.MatchString(trimmed) {
		return Target{Kind: TargetChatIdentifier, ChatIdentifier: trimmed}, nil
	}

	return Target{Kind: TargetHandle, Handle: NormalizeHandle(trimmed)}, nil
}
