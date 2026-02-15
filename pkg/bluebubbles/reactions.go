package bluebubbles

import (
	"fmt"
	"strings"
)

var reactionAliases = map[string]string{
	// General
	"heart":     "love",
	"love":      "love",
	"❤":         "love",
	"❤️":        "love",
	"red_heart": "love",

	"thumbs_up":   "like",
	"thumbsup":    "like",
	"thumbs-up":   "like",
	"like":        "like",
	"thumb":       "like",
	"ok":          "like",
	"thumbs_down": "dislike",
	"thumbsdown":  "dislike",
	"thumbs-down": "dislike",
	"dislike":     "dislike",
	"boo":         "dislike",
	"no":          "dislike",

	// Laugh
	"haha":  "laugh",
	"lol":   "laugh",
	"lmao":  "laugh",
	"rofl":  "laugh",
	"😂":     "laugh",
	"🤣":     "laugh",
	"xd":    "laugh",
	"laugh": "laugh",

	// Emphasize / exclaim
	"emphasis":  "emphasize",
	"emphasize": "emphasize",
	"exclaim":   "emphasize",
	"!!":        "emphasize",
	"‼":         "emphasize",
	"‼️":        "emphasize",
	"❗":         "emphasize",
	"important": "emphasize",
	"bang":      "emphasize",

	// Question
	"question": "question",
	"?":        "question",
	"❓":        "question",
	"❔":        "question",
	"ask":      "question",

	// Apple/Messages names
	"loved":      "love",
	"liked":      "like",
	"disliked":   "dislike",
	"laughed":    "laugh",
	"emphasized": "emphasize",
	"questioned": "question",

	// Colloquial
	"fire": "love",
	"🔥":    "love",
	"wow":  "emphasize",
	"!":    "emphasize",
}

var reactionEmojis = map[string]string{
	// Love
	"❤️": "love",
	"❤":  "love",
	"♥️": "love",
	"♥":  "love",
	"😍":  "love",
	"💕":  "love",
	// Like
	"👍": "like",
	"👌": "like",
	// Dislike
	"👎": "dislike",
	"🙅": "dislike",
	// Laugh
	"😂": "laugh",
	"🤣": "laugh",
	"😆": "laugh",
	"😁": "laugh",
	"😹": "laugh",
	// Emphasize
	"‼️": "emphasize",
	"‼":  "emphasize",
	"!!": "emphasize",
	"❗":  "emphasize",
	"❕":  "emphasize",
	"!":  "emphasize",
	// Question
	"❓": "question",
	"❔": "question",
	"?": "question",
}

func NormalizeReactionInput(raw string, remove bool) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", fmt.Errorf("reaction requires emoji or name")
	}

	// Allow callers to prefix "-" to indicate removal.
	normalizedRemove := remove
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "-") {
		normalizedRemove = true
		trimmed = strings.TrimSpace(trimmed[1:])
		lower = strings.ToLower(trimmed)
	}

	aliased, ok := reactionAliases[lower]
	if !ok {
		aliased = lower
	}
	mapped, ok := reactionEmojis[trimmed]
	if !ok {
		mapped, ok = reactionEmojis[lower]
		if !ok {
			mapped = aliased
		}
	}

	switch mapped {
	case "love", "like", "dislike", "laugh", "emphasize", "question":
		if normalizedRemove {
			return "-" + mapped, nil
		}
		return mapped, nil
	default:
		return "", fmt.Errorf("unsupported reaction: %q", raw)
	}
}
