package daguito

import "testing"

// toInbound is the wire mapping the streaming surface depends on; these tests
// lock the media envelope shape (key/mime_type/size_bytes, plus the optional
// client-owned `url`) so a refactor can't silently drop a field.

func TestToInbound_MediaKeyOnly(t *testing.T) {
	msg := MediaKeyMessage(MediaKindAudio, "org/aud/abc.m4a", "audio/mp4", 1234, "transcribe this")
	got := toInbound(msg)

	if got["kind"] != "audio" {
		t.Fatalf("kind = %v, want audio", got["kind"])
	}
	if got["text"] != "transcribe this" {
		t.Fatalf("text = %v", got["text"])
	}
	media, ok := got["media"].(map[string]any)
	if !ok {
		t.Fatalf("media missing or wrong type: %T", got["media"])
	}
	if media["key"] != "org/aud/abc.m4a" {
		t.Fatalf("media.key = %v", media["key"])
	}
	if media["mime_type"] != "audio/mp4" {
		t.Fatalf("media.mime_type = %v", media["mime_type"])
	}
	if media["size_bytes"] != int64(1234) {
		t.Fatalf("media.size_bytes = %v", media["size_bytes"])
	}
	if _, present := media["url"]; present {
		t.Fatalf("media.url must be absent for a key-only message, got %v", media["url"])
	}
}

func TestToInbound_MediaURL_ClientOwned(t *testing.T) {
	const presigned = "https://wasabi.example.com/consultations/u/chatbot/1_a.ogg?X-Amz-Signature=deadbeef"
	msg := MediaURLMessage(MediaKindAudio, "consultations/u/chatbot/1_a.ogg", presigned, "audio/ogg", 4096, "qué dice esto?")
	got := toInbound(msg)

	media, ok := got["media"].(map[string]any)
	if !ok {
		t.Fatalf("media missing: %T", got["media"])
	}
	// key stays the stable identity (NOT the URL) so the description cache keys by reference.
	if media["key"] != "consultations/u/chatbot/1_a.ogg" {
		t.Fatalf("media.key = %v, want stable key", media["key"])
	}
	if media["url"] != presigned {
		t.Fatalf("media.url = %v, want the presigned GET", media["url"])
	}
	if media["mime_type"] != "audio/ogg" || media["size_bytes"] != int64(4096) {
		t.Fatalf("media mime/size wrong: %v / %v", media["mime_type"], media["size_bytes"])
	}
}
