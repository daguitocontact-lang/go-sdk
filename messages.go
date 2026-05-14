package daguito

// MediaKind enumerates the message kinds that carry an uploaded attachment.
// The values match the canonical MessageKind enum in @daguito/core
// (packages/core/src/messages/kinds.ts).
type MediaKind string

const (
	MediaKindImage    MediaKind = "image"
	MediaKindAudio    MediaKind = "audio"
	MediaKindDocument MediaKind = "document"
	MediaKindVideo    MediaKind = "video"
)

// SendableMessage is the wire shape accepted by WebhookStreamSession.Send.
// In TypeScript this is a discriminated union; in Go we model it as a struct
// with optional fields and a Kind tag. Use the helper constructors below —
// they fill the correct subset of fields for each kind.
//
// Wire kinds:
//
//   - "text"
//   - "image"        with ImageURL or MediaKey (pre-uploaded)
//   - "image-multi"  with ImageURLs
//   - "audio" / "document" / "video"   with MediaKey
//   - "form-response"                  with FormID + Payload
type SendableMessage struct {
	Kind string `json:"kind"`
	Text string `json:"text,omitempty"`

	ImageURL  string   `json:"image_url,omitempty"`
	ImageURLs []string `json:"image_urls,omitempty"`

	MediaKey  string `json:"media_key,omitempty"`
	MimeType  string `json:"mime_type,omitempty"`
	SizeBytes int64  `json:"size_bytes,omitempty"`

	FormID  string         `json:"form_id,omitempty"`
	Payload map[string]any `json:"payload,omitempty"`
}

// TextMessage builds a plain text message.
func TextMessage(text string) SendableMessage {
	return SendableMessage{Kind: "text", Text: text}
}

// ImageURLMessage attaches a single public image URL. The flow's vision tool
// fetches it server-side — no upload happens here.
func ImageURLMessage(imageURL, text string) SendableMessage {
	return SendableMessage{Kind: "image", ImageURL: imageURL, Text: text}
}

// ImageMultiMessage attaches several public image URLs for comparison /
// multi-image prompts.
func ImageMultiMessage(imageURLs []string, text string) SendableMessage {
	return SendableMessage{Kind: "image-multi", ImageURLs: imageURLs, Text: text}
}

// MediaKeyMessage attaches a previously uploaded media object identified by
// the key returned from UploadFile. kind is one of MediaKindImage,
// MediaKindAudio, MediaKindDocument, MediaKindVideo.
func MediaKeyMessage(kind MediaKind, mediaKey, mimeType string, sizeBytes int64, text string) SendableMessage {
	return SendableMessage{
		Kind:      string(kind),
		MediaKey:  mediaKey,
		MimeType:  mimeType,
		SizeBytes: sizeBytes,
		Text:      text,
	}
}

// FormResponseMessage resumes a flow that paused on a form node.
func FormResponseMessage(formID string, payload map[string]any) SendableMessage {
	return SendableMessage{Kind: "form-response", FormID: formID, Payload: payload}
}
