// Package daguito is the official Go SDK for the Daguito conversational AI
// platform — text, voice, image, audio, document and video agent flows.
//
// It mirrors the public surface of the Python and TypeScript SDKs:
//
//   - RunWebhook            one-shot HTTP call to a flow.
//   - WebhookStreamSession  long-lived WebSocket; streams tokens and events.
//   - UploadFile            presigned upload for media attachments.
//   - KnowledgeSession      HTTP client for KB ingest + search.
//
// Every public function that performs I/O takes a context.Context as its
// first argument and honours cancellation. The wire protocol (routes, frame
// types, base_input shape, scope filter, media envelope) is identical to the
// Python and JS SDKs — they are interchangeable from the server's point of
// view.
package daguito
