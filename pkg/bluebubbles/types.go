package bluebubbles

// Attachment is a minimal representation of a BlueBubbles attachment record.
// Field names match common BlueBubbles webhook payloads.
type Attachment struct {
	GUID         string `json:"guid"`
	MimeType     string `json:"mimeType"`
	TransferName string `json:"transferName"`
	TotalBytes   int64  `json:"totalBytes"`
}

type SendResult struct {
	MessageID string
}
