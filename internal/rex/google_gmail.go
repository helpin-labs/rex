package rex

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/mail"
	"net/url"
	"regexp"
	"strings"
	"sync"
)

// Gmail's tools. Reading, searching and drafting run on their own; sending
// and changing labels follow the bot's approvals. Rex asks for gmail.modify,
// which covers all of these but cannot delete mail for good.

const gmailModify = "https://www.googleapis.com/auth/gmail.modify"

// Limits that keep a tool result within what a model can use.
const (
	gmailMaxResults = 25
	gmailBodyLimit  = 12000
)

var gmailService = &googleService{
	name:        "Gmail",
	permissions: []string{gmailModify},
	tools: []googleTool{
		{
			name:        "search_emails",
			description: "Search the user's mailbox with Gmail search syntax (from:, to:, subject:, is:unread, newer_than:7d, has:attachment, label:…). Returns the sender, subject, date and a snippet of each message, newest first.",
			access:      "read",
			schema: objectSchema([]string{"query"}, map[string]any{
				"query":       map[string]any{"type": "string", "description": "Gmail search, e.g. \"from:alex is:unread newer_than:3d\". Empty lists the latest mail."},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": gmailMaxResults, "description": "At most 25; 10 by default."},
				"page_token":  map[string]any{"type": "string", "description": "next_page_token from an earlier search, for more results."},
			}),
			call: gmailSearch,
		},
		{
			name:        "read_thread",
			description: "Read a whole email conversation: every message's sender, recipients, date and text, and the names of attachments.",
			access:      "read",
			schema: objectSchema([]string{"thread_id"}, map[string]any{
				"thread_id": map[string]any{"type": "string", "description": "thread_id from search_emails."},
			}),
			call: gmailReadThread,
		},
		{
			name:        "list_labels",
			description: "List the mailbox's labels, with their ids, for modify_labels and label: searches.",
			access:      "read",
			schema:      objectSchema(nil, map[string]any{}),
			call:        gmailListLabels,
		},
		{
			name:        "create_draft",
			description: "Save an email as a draft in the user's Gmail for them to review and send. To reply, pass reply_to_message_id; the draft joins that conversation.",
			access:      "read",
			schema:      gmailMessageSchema(false),
			call: func(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
				return gmailCompose(ctx, api, input, false)
			},
		},
		{
			name:        "send_email",
			description: "Send an email from the user's Gmail account. To reply, pass reply_to_message_id; the reply joins that conversation.",
			access:      "write",
			schema:      gmailMessageSchema(true),
			call: func(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
				return gmailCompose(ctx, api, input, true)
			},
		},
		{
			name:        "modify_labels",
			description: "Add or remove labels on messages. Archive: remove INBOX. Mark as read: remove UNREAD. Star: add STARRED. Trash: add TRASH. User labels can be given by name.",
			access:      "write",
			schema: objectSchema([]string{"message_ids"}, map[string]any{
				"message_ids":   map[string]any{"type": "array", "minItems": 1, "maxItems": 100, "items": map[string]any{"type": "string"}},
				"add_labels":    map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
				"remove_labels": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
			}),
			call: gmailModifyLabels,
		},
	},
}

func objectSchema(required []string, properties map[string]any) map[string]any {
	schema := map[string]any{"type": "object", "additionalProperties": false, "properties": properties}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

func gmailMessageSchema(send bool) map[string]any {
	recipients := func(description string) map[string]any {
		return map[string]any{"type": "array", "maxItems": 50, "items": map[string]any{"type": "string"}, "description": description}
	}
	required := []string{"body"}
	if send {
		required = []string{"to", "body"}
	}
	return objectSchema(required, map[string]any{
		"to":                  recipients("Addresses, e.g. \"alex@example.com\" or \"Alex Kim <alex@example.com>\"."),
		"cc":                  recipients(""),
		"bcc":                 recipients(""),
		"subject":             map[string]any{"type": "string", "maxLength": 500, "description": "Leave empty when replying to keep \"Re: <subject>\"."},
		"body":                map[string]any{"type": "string", "maxLength": 100000, "description": "Plain text."},
		"reply_to_message_id": map[string]any{"type": "string", "description": "id of the message being answered, from search_emails or read_thread."},
	})
}

// --- reading ---

type gmailHeader struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

type gmailPart struct {
	MimeType string        `json:"mimeType"`
	Filename string        `json:"filename"`
	Headers  []gmailHeader `json:"headers"`
	Body     struct {
		Data         string `json:"data"`
		Size         int    `json:"size"`
		AttachmentID string `json:"attachmentId"`
	} `json:"body"`
	Parts []gmailPart `json:"parts"`
}

type gmailMessage struct {
	ID       string    `json:"id"`
	ThreadID string    `json:"threadId"`
	LabelIDs []string  `json:"labelIds"`
	Snippet  string    `json:"snippet"`
	Payload  gmailPart `json:"payload"`
}

func (p gmailPart) header(name string) string {
	for _, header := range p.Headers {
		if strings.EqualFold(header.Name, name) {
			return header.Value
		}
	}
	return ""
}

func gmailSearch(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Query      string `json:"query"`
		MaxResults int    `json:"max_results"`
		PageToken  string `json:"page_token"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 10
	}
	args.MaxResults = min(args.MaxResults, gmailMaxResults)
	query := url.Values{"maxResults": {fmt.Sprint(args.MaxResults)}}
	if q := strings.TrimSpace(args.Query); q != "" {
		query.Set("q", q)
	}
	if args.PageToken != "" {
		query.Set("pageToken", args.PageToken)
	}
	var list struct {
		Messages []struct {
			ID string `json:"id"`
		} `json:"messages"`
		NextPageToken string `json:"nextPageToken"`
	}
	if err := api.do(ctx, http.MethodGet, gmailAPI+"/messages?"+query.Encode(), nil, &list); err != nil {
		return nil, err
	}
	// Each message's headers come from its own request: a few at a time.
	found := make([]map[string]any, len(list.Messages))
	failures := make([]error, len(list.Messages))
	var wait sync.WaitGroup
	slots := make(chan struct{}, 5)
	for index, item := range list.Messages {
		wait.Add(1)
		go func() {
			defer wait.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			var message gmailMessage
			endpoint := gmailAPI + "/messages/" + url.PathEscape(item.ID) + "?format=metadata&metadataHeaders=From&metadataHeaders=To&metadataHeaders=Subject&metadataHeaders=Date"
			if err := api.do(ctx, http.MethodGet, endpoint, nil, &message); err != nil {
				failures[index] = err
				return
			}
			found[index] = map[string]any{
				"id": message.ID, "thread_id": message.ThreadID,
				"from": message.Payload.header("From"), "to": message.Payload.header("To"),
				"subject": message.Payload.header("Subject"), "date": message.Payload.header("Date"),
				"snippet": html.UnescapeString(message.Snippet), "labels": message.LabelIDs,
				"unread": containsString(message.LabelIDs, "UNREAD"),
			}
		}()
	}
	wait.Wait()
	messages := []map[string]any{}
	for _, item := range found {
		if item != nil {
			messages = append(messages, item)
		}
	}
	// A message deleted since the search is skipped; if none could be read,
	// the reason is reported.
	if len(list.Messages) > 0 && len(messages) == 0 {
		return nil, errors.Join(failures...)
	}
	result := map[string]any{"messages": messages}
	if list.NextPageToken != "" {
		result["next_page_token"] = list.NextPageToken
	}
	return result, nil
}

func gmailReadThread(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.ThreadID) == "" {
		return nil, errors.New("thread_id is required")
	}
	var thread struct {
		ID       string         `json:"id"`
		Messages []gmailMessage `json:"messages"`
	}
	if err := api.do(ctx, http.MethodGet, gmailAPI+"/threads/"+url.PathEscape(args.ThreadID)+"?format=full", nil, &thread); err != nil {
		return nil, err
	}
	messages := make([]map[string]any, 0, len(thread.Messages))
	for _, message := range thread.Messages {
		body, truncated := excerptRunes(messageText(message.Payload), gmailBodyLimit)
		item := map[string]any{
			"id": message.ID, "from": message.Payload.header("From"), "to": message.Payload.header("To"),
			"date": message.Payload.header("Date"), "subject": message.Payload.header("Subject"),
			"labels": message.LabelIDs, "body": body,
		}
		if cc := message.Payload.header("Cc"); cc != "" {
			item["cc"] = cc
		}
		if truncated {
			item["body_truncated"] = true
		}
		if attachments := messageAttachments(message.Payload); len(attachments) > 0 {
			item["attachments"] = attachments
		}
		messages = append(messages, item)
	}
	return map[string]any{"thread_id": thread.ID, "messages": messages}, nil
}

// messageText is a message's readable text: its plain part, or its HTML
// part reduced to text.
func messageText(part gmailPart) string {
	if plain := findPart(part, "text/plain"); plain != nil {
		return strings.TrimSpace(decodeBody(plain.Body.Data))
	}
	if rich := findPart(part, "text/html"); rich != nil {
		return htmlToText(decodeBody(rich.Body.Data))
	}
	return ""
}

func findPart(part gmailPart, mimeType string) *gmailPart {
	if strings.EqualFold(part.MimeType, mimeType) && part.Filename == "" && part.Body.Data != "" {
		return &part
	}
	for _, child := range part.Parts {
		if found := findPart(child, mimeType); found != nil {
			return found
		}
	}
	return nil
}

func messageAttachments(part gmailPart) []map[string]any {
	var attachments []map[string]any
	if part.Filename != "" {
		attachments = append(attachments, map[string]any{"filename": part.Filename, "mime_type": part.MimeType, "size": part.Body.Size})
	}
	for _, child := range part.Parts {
		attachments = append(attachments, messageAttachments(child)...)
	}
	return attachments
}

// decodeBody decodes Gmail's base64url body data, padded or not.
func decodeBody(data string) string {
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(data, "="))
	if err != nil {
		return ""
	}
	return string(decoded)
}

var (
	htmlDropped = regexp.MustCompile(`(?is)<(script|style|head)[^>]*>.*?</(script|style|head)>`)
	htmlBreaks  = regexp.MustCompile(`(?i)<(br|/p|/div|/tr|/li|/h[1-6])[^>]*>`)
	htmlTags    = regexp.MustCompile(`<[^>]+>`)
	blankLines  = regexp.MustCompile(`\n[ \t]*(\n[ \t]*)+`)
	spaceRuns   = regexp.MustCompile(`[ \t\x{00a0}]+`)
)

func htmlToText(markup string) string {
	text := htmlDropped.ReplaceAllString(markup, "")
	text = htmlBreaks.ReplaceAllString(text, "\n")
	text = htmlTags.ReplaceAllString(text, "")
	text = html.UnescapeString(text)
	text = spaceRuns.ReplaceAllString(text, " ")
	text = blankLines.ReplaceAllString(text, "\n\n")
	return strings.TrimSpace(text)
}

// excerptRunes cuts text to limit characters and says whether it did.
func excerptRunes(text string, limit int) (string, bool) {
	runes := []rune(text)
	if len(runes) <= limit {
		return text, false
	}
	return string(runes[:limit]) + "…", true
}

func gmailListLabels(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct{}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	labels, err := gmailLabels(ctx, api)
	if err != nil {
		return nil, err
	}
	return map[string]any{"labels": labels}, nil
}

type gmailLabel struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Type string `json:"type"`
}

func gmailLabels(ctx context.Context, api *googleAPI) ([]gmailLabel, error) {
	var list struct {
		Labels []gmailLabel `json:"labels"`
	}
	if err := api.do(ctx, http.MethodGet, gmailAPI+"/labels", nil, &list); err != nil {
		return nil, err
	}
	return list.Labels, nil
}

// --- writing ---

type gmailOutgoing struct {
	To               []string `json:"to"`
	Cc               []string `json:"cc"`
	Bcc              []string `json:"bcc"`
	Subject          string   `json:"subject"`
	Body             string   `json:"body"`
	ReplyToMessageID string   `json:"reply_to_message_id"`
	// Set from the message being answered.
	inReplyTo  string
	references string
	threadID   string
}

func gmailCompose(ctx context.Context, api *googleAPI, input json.RawMessage, send bool) (any, error) {
	var outgoing gmailOutgoing
	if err := decodeInput(input, &outgoing); err != nil {
		return nil, err
	}
	if send && len(outgoing.To)+len(outgoing.Cc)+len(outgoing.Bcc) == 0 {
		return nil, errors.New("an email needs at least one recipient")
	}
	if outgoing.ReplyToMessageID != "" {
		var original gmailMessage
		endpoint := gmailAPI + "/messages/" + url.PathEscape(outgoing.ReplyToMessageID) + "?format=metadata&metadataHeaders=Message-ID&metadataHeaders=References&metadataHeaders=Subject"
		if err := api.do(ctx, http.MethodGet, endpoint, nil, &original); err != nil {
			return nil, fmt.Errorf("could not find the message to reply to: %w", err)
		}
		outgoing.threadID = original.ThreadID
		outgoing.inReplyTo = original.Payload.header("Message-ID")
		outgoing.references = strings.TrimSpace(original.Payload.header("References") + " " + outgoing.inReplyTo)
		if strings.TrimSpace(outgoing.Subject) == "" {
			outgoing.Subject = replySubject(original.Payload.header("Subject"))
		}
	}
	raw, err := buildEmail(outgoing)
	if err != nil {
		return nil, err
	}
	message := map[string]any{"raw": raw}
	if outgoing.threadID != "" {
		message["threadId"] = outgoing.threadID
	}
	if send {
		var sent struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		}
		if err := api.do(ctx, http.MethodPost, gmailAPI+"/messages/send", message, &sent); err != nil {
			return nil, err
		}
		return map[string]any{"status": "sent", "message_id": sent.ID, "thread_id": sent.ThreadID}, nil
	}
	var draft struct {
		ID      string `json:"id"`
		Message struct {
			ID       string `json:"id"`
			ThreadID string `json:"threadId"`
		} `json:"message"`
	}
	if err := api.do(ctx, http.MethodPost, gmailAPI+"/drafts", map[string]any{"message": message}, &draft); err != nil {
		return nil, err
	}
	return map[string]any{"status": "draft_saved", "draft_id": draft.ID, "thread_id": draft.Message.ThreadID,
		"link": "https://mail.google.com/mail/#drafts?compose=" + draft.Message.ID, "message": "Saved in the user's Gmail drafts; it has not been sent."}, nil
}

func replySubject(subject string) string {
	subject = strings.TrimSpace(subject)
	if strings.HasPrefix(strings.ToLower(subject), "re:") {
		return subject
	}
	return "Re: " + subject
}

// buildEmail writes an RFC 5322 message, base64url-encoded for Gmail.
// Gmail adds From, Date and Message-ID itself.
func buildEmail(outgoing gmailOutgoing) (string, error) {
	var message bytes.Buffer
	for _, field := range []struct {
		name   string
		values []string
	}{{"To", outgoing.To}, {"Cc", outgoing.Cc}, {"Bcc", outgoing.Bcc}} {
		if len(field.values) == 0 {
			continue
		}
		addresses, err := parseRecipients(field.values)
		if err != nil {
			return "", fmt.Errorf("%s: %w", field.name, err)
		}
		fmt.Fprintf(&message, "%s: %s\r\n", field.name, addresses)
	}
	fmt.Fprintf(&message, "Subject: %s\r\n", mime.QEncoding.Encode("utf-8", oneLine(outgoing.Subject)))
	if outgoing.inReplyTo != "" {
		fmt.Fprintf(&message, "In-Reply-To: %s\r\nReferences: %s\r\n", oneLine(outgoing.inReplyTo), oneLine(outgoing.references))
	}
	message.WriteString("MIME-Version: 1.0\r\nContent-Type: text/plain; charset=\"UTF-8\"\r\nContent-Transfer-Encoding: quoted-printable\r\n\r\n")
	writer := quotedprintable.NewWriter(&message)
	body := strings.ReplaceAll(strings.ReplaceAll(outgoing.Body, "\r\n", "\n"), "\n", "\r\n")
	if _, err := writer.Write([]byte(body)); err != nil {
		return "", err
	}
	if err := writer.Close(); err != nil {
		return "", err
	}
	return base64.URLEncoding.EncodeToString(message.Bytes()), nil
}

// parseRecipients checks each address and writes them as one header value.
func parseRecipients(values []string) (string, error) {
	var formatted []string
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value == "" {
			continue
		}
		addresses, err := mail.ParseAddressList(value)
		if err != nil {
			return "", fmt.Errorf("%q is not an email address", value)
		}
		for _, address := range addresses {
			formatted = append(formatted, address.String())
		}
	}
	if len(formatted) == 0 {
		return "", errors.New("no addresses")
	}
	return strings.Join(formatted, ", "), nil
}

// oneLine keeps a header value from starting new header lines.
func oneLine(value string) string {
	return strings.Join(strings.Fields(strings.NewReplacer("\r", " ", "\n", " ").Replace(value)), " ")
}

var systemLabels = map[string]bool{"INBOX": true, "UNREAD": true, "STARRED": true, "IMPORTANT": true, "TRASH": true, "SPAM": true,
	"CATEGORY_PERSONAL": true, "CATEGORY_SOCIAL": true, "CATEGORY_PROMOTIONS": true, "CATEGORY_UPDATES": true, "CATEGORY_FORUMS": true}

func gmailModifyLabels(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		MessageIDs   []string `json:"message_ids"`
		AddLabels    []string `json:"add_labels"`
		RemoveLabels []string `json:"remove_labels"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if len(args.MessageIDs) == 0 {
		return nil, errors.New("message_ids is required")
	}
	if len(args.AddLabels)+len(args.RemoveLabels) == 0 {
		return nil, errors.New("give add_labels or remove_labels")
	}
	var labels []gmailLabel
	resolve := func(names []string) ([]string, error) {
		ids := []string{}
		for _, name := range names {
			if upper := strings.ToUpper(strings.TrimSpace(name)); systemLabels[upper] {
				ids = append(ids, upper)
				continue
			}
			if labels == nil {
				var err error
				if labels, err = gmailLabels(ctx, api); err != nil {
					return nil, err
				}
			}
			id := ""
			for _, label := range labels {
				if label.ID == name || strings.EqualFold(label.Name, strings.TrimSpace(name)) {
					id = label.ID
					break
				}
			}
			if id == "" {
				return nil, fmt.Errorf("no label called %q; use list_labels", name)
			}
			ids = append(ids, id)
		}
		return ids, nil
	}
	add, err := resolve(args.AddLabels)
	if err != nil {
		return nil, err
	}
	remove, err := resolve(args.RemoveLabels)
	if err != nil {
		return nil, err
	}
	body := map[string]any{"ids": args.MessageIDs, "addLabelIds": add, "removeLabelIds": remove}
	if err := api.do(ctx, http.MethodPost, gmailAPI+"/messages/batchModify", body, nil); err != nil {
		return nil, err
	}
	return map[string]any{"status": "updated", "messages": len(args.MessageIDs)}, nil
}
