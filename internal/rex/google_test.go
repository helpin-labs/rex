package rex

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime"
	"mime/quotedprintable"
	"net/http"
	"net/http/httptest"
	"net/mail"
	"net/url"
	"strings"
	"sync"
	"testing"

	protocol "github.com/modelcontextprotocol/go-sdk/mcp"
)

type protocolSession = protocol.ClientSession

func connectTestMCP(ctx context.Context, endpoint, token string) (*protocol.ClientSession, error) {
	client := &http.Client{Transport: bearerTransport{token: token, base: http.DefaultTransport}}
	return protocol.NewClient(&protocol.Implementation{Name: "test", Version: "1"}, nil).
		Connect(ctx, &protocol.StreamableClientTransport{Endpoint: endpoint, HTTPClient: client, MaxRetries: -1}, nil)
}

func callTestTool(t *testing.T, session *protocol.ClientSession, name string, arguments map[string]any) string {
	t.Helper()
	result, err := session.CallTool(context.Background(), &protocol.CallToolParams{Name: name, Arguments: arguments})
	if err != nil {
		t.Fatal(err)
	}
	text := result.Content[0].(*protocol.TextContent).Text
	if result.IsError {
		t.Fatalf("%s failed: %s", name, text)
	}
	return text
}

const testGoogleClientID = "test-client.apps.googleusercontent.com"

// fakeGoogle stands in for Google's sign-in and the Gmail, Sheets and Drive
// APIs. Only its current access token is accepted.
type fakeGoogle struct {
	*httptest.Server
	mu       sync.Mutex
	valid    string
	refreshs int
	revoked  []string
	drafts   []string
	paths    []string
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	fake := &fakeGoogle{}
	fake.Server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.Close)
	saved := []string{googleAuthURL, googleTokenURL, googleRevokeURL, gmailAPI, sheetsAPI, driveAPI}
	googleAuthURL, googleTokenURL, googleRevokeURL = fake.URL+"/auth", fake.URL+"/token", fake.URL+"/revoke"
	gmailAPI, sheetsAPI, driveAPI = fake.URL+"/gmail", fake.URL+"/sheets", fake.URL+"/drive"
	t.Cleanup(func() {
		googleAuthURL, googleTokenURL, googleRevokeURL, gmailAPI, sheetsAPI, driveAPI = saved[0], saved[1], saved[2], saved[3], saved[4], saved[5]
	})
	return fake
}

func testIDToken(email string) string {
	part := func(value any) string {
		payload, _ := json.Marshal(value)
		return base64.RawURLEncoding.EncodeToString(payload)
	}
	return part(map[string]string{"alg": "RS256"}) + "." + part(map[string]string{"email": email}) + ".signature"
}

func (f *fakeGoogle) serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	f.mu.Lock()
	defer f.mu.Unlock()
	f.paths = append(f.paths, r.URL.EscapedPath())
	switch r.URL.Path {
	case "/token":
		_ = r.ParseForm()
		form := r.Form
		if form.Get("client_id") != testGoogleClientID || form.Get("client_secret") != "not-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_client"})
			return
		}
		scopes := "openid https://www.googleapis.com/auth/userinfo.email " + gmailModify + " " + sheetsScope + " " + driveMetadataScope
		switch {
		case form.Get("grant_type") == "authorization_code" && form.Get("code_verifier") != "" && form.Get("code") == "good-code":
			f.valid = "access-1"
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-1", "refresh_token": "refresh-1", "expires_in": 3599, "scope": scopes, "id_token": testIDToken("sam@example.com")})
		case form.Get("grant_type") == "authorization_code" && form.Get("code") == "unticked":
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-x", "refresh_token": "refresh-x", "expires_in": 3599, "scope": "openid " + sheetsScope})
		case form.Get("grant_type") == "refresh_token" && form.Get("refresh_token") == "refresh-1":
			f.refreshs++
			f.valid = "access-2"
			_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "access-2", "expires_in": 3599, "scope": scopes})
		default:
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid_grant", "error_description": "Token has been expired or revoked."})
		}
		return
	case "/revoke":
		_ = r.ParseForm()
		f.revoked = append(f.revoked, r.Form.Get("token"))
		return
	}
	if r.Header.Get("Authorization") != "Bearer "+f.valid || f.valid == "" {
		w.WriteHeader(http.StatusUnauthorized)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"code": 401, "message": "Request had invalid authentication credentials."}})
		return
	}
	header := func(name, value string) map[string]string { return map[string]string{"name": name, "value": value} }
	switch {
	case r.URL.Path == "/gmail/messages" && r.Method == http.MethodGet:
		_ = json.NewEncoder(w).Encode(map[string]any{"messages": []map[string]string{{"id": "m1"}, {"id": "m2"}}, "nextPageToken": "page-2"})
	case strings.HasPrefix(r.URL.Path, "/gmail/messages/m"):
		id := strings.TrimPrefix(r.URL.Path, "/gmail/messages/")
		_ = json.NewEncoder(w).Encode(map[string]any{"id": id, "threadId": "t1", "labelIds": []string{"INBOX", "UNREAD"}, "snippet": "Lunch &amp; plans",
			"payload": map[string]any{"headers": []any{header("From", "Alex <alex@example.com>"), header("Subject", "Lunch"), header("Message-Id", "<m1@mail.example.com>")}}})
	case r.URL.Path == "/gmail/drafts":
		var body struct {
			Message struct {
				Raw      string `json:"raw"`
				ThreadID string `json:"threadId"`
			} `json:"message"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.drafts = append(f.drafts, body.Message.Raw+"|"+body.Message.ThreadID)
		_ = json.NewEncoder(w).Encode(map[string]any{"id": "d1", "message": map[string]string{"id": "m9", "threadId": body.Message.ThreadID}})
	case strings.HasPrefix(r.URL.Path, "/sheets/"):
		_ = json.NewEncoder(w).Encode(map[string]any{"range": "'Q3 plan'!A1:B2", "values": [][]any{{"Name", "Total"}, {"Ada", "12"}}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": "not found: " + r.URL.Path}})
	}
}

// signedInGoogleApp adds a Google app and signs in to it.
func signedInGoogleApp(t *testing.T, host *Host, app string) *MCPConnection {
	t.Helper()
	ctx := context.Background()
	if _, err := host.SetGoogleClient(ctx, GoogleClient{ClientID: testGoogleClientID, ClientSecret: "not-secret"}); err != nil {
		t.Fatal(err)
	}
	connection, err := host.SaveMCPConnection(ctx, MCPConnection{Name: googleServices[app].name, URL: app})
	if err != nil {
		t.Fatal(err)
	}
	signInURL, err := host.StartOAuth(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signInURL)
	connection, err = host.CompleteOAuth(ctx, parsed.Query().Get("state"), "good-code", "")
	if err != nil {
		t.Fatal(err)
	}
	return connection
}

func TestGoogleSignInNeedsAClientAndEveryPermission(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	newFakeGoogle(t)

	connection, err := host.SaveMCPConnection(ctx, MCPConnection{Name: "Gmail", URL: "google:gmail"})
	if err != nil {
		t.Fatal(err)
	}
	if connection.Auth != "google" || connection.SignedIn || connection.Status != "Sign in to connect" {
		t.Fatalf("a Google app is served by Rex and waits for sign-in: %+v", connection)
	}
	if _, err := host.StartOAuth(ctx, connection.ID); err == nil || !strings.Contains(err.Error(), "Google client") {
		t.Fatalf("signing in needs a Google client first, got %v", err)
	}
	if _, err := host.SetGoogleClient(ctx, GoogleClient{ClientID: "nope"}); err == nil {
		t.Fatal("a client ID that is not Google's is refused")
	}
	if status, err := host.SetGoogleClient(ctx, GoogleClient{ClientID: testGoogleClientID, ClientSecret: "not-secret"}); err != nil || !status.Configured || status.Source != "settings" {
		t.Fatalf("status %+v, %v", status, err)
	}

	signInURL, err := host.StartOAuth(ctx, connection.ID)
	if err != nil {
		t.Fatal(err)
	}
	parsed, _ := url.Parse(signInURL)
	query := parsed.Query()
	if parsed.Path != "/auth" || query.Get("client_id") != testGoogleClientID || query.Get("redirect_uri") != host.oauthRedirectURI() ||
		query.Get("code_challenge_method") != "S256" || query.Get("access_type") != "offline" || !strings.Contains(query.Get("scope"), gmailModify) {
		t.Fatalf("Google sign-in uses PKCE, Rex's callback and asks for a refresh token: %s", signInURL)
	}
	if _, err := host.CompleteOAuth(ctx, query.Get("state"), "unticked", ""); err == nil || !strings.Contains(err.Error(), "gmail.modify") {
		t.Fatalf("a sign-in without every permission is refused, got %v", err)
	}

	connection = signedInGoogleApp(t, host, "google:gmail")
	if !connection.SignedIn || connection.Account != "sam@example.com" || len(connection.Tools) != len(gmailService.tools) || !strings.HasPrefix(connection.Status, "Connected") {
		t.Fatalf("signed in with the account and tools: %+v", connection)
	}

	bot := Bot{Name: "Inbox", MCPConnections: []string{connection.ID}}
	servers, err := host.MCPServersForBot(ctx, bot)
	if err != nil || len(servers) != 1 {
		t.Fatalf("servers %+v, %v", servers, err)
	}
	if servers[0].URL != host.cfg.HostURL+"/mcp/google/"+connection.ID || servers[0].Credential.AccessToken != host.GoogleToken(connection.ID) {
		t.Fatalf("runs reach Rex's Google endpoint with Rex's own token, never Google's: %+v", servers[0])
	}

	// Another client cannot use this client's sign-ins.
	if _, err := host.SetGoogleClient(ctx, GoogleClient{ClientID: "other.apps.googleusercontent.com"}); err != nil {
		t.Fatal(err)
	}
	if current, _ := host.Store.GetMCPConnection(ctx, connection.ID); current.oauth.SignedIn {
		t.Fatal("changing the Google client signs Google apps out")
	}
}

func TestGoogleToolsRefreshARejectedToken(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	fake := newFakeGoogle(t)
	connection := signedInGoogleApp(t, host, "google:gmail")

	mcp := httptest.NewServer(host.googleMCP())
	t.Cleanup(mcp.Close)
	connect := func(token string) (*protocolSession, error) {
		return connectTestMCP(ctx, mcp.URL+"/mcp/google/"+connection.ID, token)
	}
	if _, err := connect("wrong"); err == nil {
		t.Fatal("the endpoint needs the connection's token")
	}
	session, err := connect(host.GoogleToken(connection.ID))
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil || len(tools.Tools) != len(gmailService.tools) {
		t.Fatalf("tools %+v, %v", tools, err)
	}

	// Google stops accepting the token early: Rex refreshes once and retries.
	fake.mu.Lock()
	fake.valid = "access-revoked"
	fake.mu.Unlock()
	result := callTestTool(t, session, "search_emails", map[string]any{"query": "is:unread", "max_results": 2})
	var found struct {
		Messages []struct {
			From    string `json:"from"`
			Snippet string `json:"snippet"`
			Unread  bool   `json:"unread"`
		} `json:"messages"`
		Next string `json:"next_page_token"`
	}
	_ = json.Unmarshal([]byte(result), &found)
	if len(found.Messages) != 2 || found.Messages[0].From != "Alex <alex@example.com>" || found.Messages[0].Snippet != "Lunch & plans" || !found.Messages[0].Unread || found.Next != "page-2" {
		t.Fatalf("search result %s", result)
	}
	if fake.refreshs != 1 {
		t.Fatalf("refreshed %d times, want 1", fake.refreshs)
	}

	// A reply draft joins the conversation and quotes its subject.
	callTestTool(t, session, "create_draft", map[string]any{"to": []string{"alex@example.com"}, "body": "Sounds good.", "reply_to_message_id": "m1"})
	raw, thread, _ := strings.Cut(fake.drafts[0], "|")
	decoded, _ := base64.URLEncoding.DecodeString(raw)
	message, err := mail.ReadMessage(strings.NewReader(string(decoded)))
	if err != nil {
		t.Fatal(err)
	}
	if thread != "t1" || message.Header.Get("Subject") != "Re: Lunch" || message.Header.Get("In-Reply-To") != "<m1@mail.example.com>" {
		t.Fatalf("reply draft thread %q headers %v", thread, message.Header)
	}

	// Turned-off tools are not offered.
	stored, _ := host.Store.GetMCPConnection(ctx, connection.ID)
	for index := range stored.Tools {
		stored.Tools[index].Enabled = stored.Tools[index].Name == "search_emails"
	}
	if err := host.Store.SaveMCPConnection(ctx, stored); err != nil {
		t.Fatal(err)
	}
	if tools, _ := session.ListTools(ctx, nil); len(tools.Tools) != 1 {
		t.Fatalf("only enabled tools are served, got %d", len(tools.Tools))
	}
}

func TestSheetsReadsAQuotedRange(t *testing.T) {
	ctx := context.Background()
	host := newSetupTestHost(t)
	fake := newFakeGoogle(t)
	connection := signedInGoogleApp(t, host, "google:sheets")
	tool := sheetsService.tools[2]
	result, err := host.callGoogle(ctx, *connection, tool, json.RawMessage(`{"spreadsheet":"https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz/edit#gid=0","range":"'Q3 plan'!A1:B2"}`))
	if err != nil {
		t.Fatal(err)
	}
	payload, _ := json.Marshal(result)
	if !strings.Contains(string(payload), `["Ada","12"]`) {
		t.Fatalf("read %s", payload)
	}
	fake.mu.Lock()
	defer fake.mu.Unlock()
	last := fake.paths[len(fake.paths)-1]
	if last != "/sheets/1AbCdEfGhIjKlMnOpQrStUvWxYz/values/%27Q3%20plan%27%21A1:B2" {
		t.Fatalf("range path %s", last)
	}
	if _, err := host.callGoogle(ctx, *connection, tool, json.RawMessage(`{"spreadsheet":"x","range":"A1","colour":"red"}`)); err == nil {
		t.Fatal("bad ids and unknown arguments are refused")
	}
}

func TestBuildEmail(t *testing.T) {
	raw, err := buildEmail(gmailOutgoing{
		To: []string{"Zoë Example <zoe@example.com>", "sam@example.com"}, Subject: "Café plans\r\nBcc: evil@example.com",
		Body: "Hi Zoë,\nSee you at 8.\n",
	})
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil {
		t.Fatal(err)
	}
	message, err := mail.ReadMessage(strings.NewReader(string(decoded)))
	if err != nil {
		t.Fatal(err)
	}
	to, err := message.Header.AddressList("To")
	if err != nil || len(to) != 2 || to[0].Name != "Zoë Example" || to[1].Address != "sam@example.com" {
		t.Fatalf("To %v, %v", to, err)
	}
	subject, _ := new(mime.WordDecoder).DecodeHeader(message.Header.Get("Subject"))
	if subject != "Café plans Bcc: evil@example.com" || message.Header.Get("Bcc") != "" {
		t.Fatalf("a subject cannot add headers: %q, Bcc %q", subject, message.Header.Get("Bcc"))
	}
	body, _ := io.ReadAll(quotedprintable.NewReader(message.Body))
	if string(body) != "Hi Zoë,\r\nSee you at 8.\r\n" {
		t.Fatalf("body %q", body)
	}
	if _, err := buildEmail(gmailOutgoing{To: []string{"not an address"}}); err == nil {
		t.Fatal("bad addresses are refused")
	}
}

func TestMessageText(t *testing.T) {
	encode := func(text string) string { return base64.URLEncoding.EncodeToString([]byte(text)) }
	var both gmailPart
	_ = json.Unmarshal([]byte(`{"mimeType":"multipart/mixed","parts":[
		{"mimeType":"multipart/alternative","parts":[
			{"mimeType":"text/html","body":{"data":"`+encode("<p>Rich</p>")+`"}},
			{"mimeType":"text/plain","body":{"data":"`+encode("Plain text")+`"}}]},
		{"mimeType":"application/pdf","filename":"invoice.pdf","body":{"attachmentId":"a1","size":2048}}]}`), &both)
	if got := messageText(both); got != "Plain text" {
		t.Fatalf("plain text is preferred, got %q", got)
	}
	if attachments := messageAttachments(both); len(attachments) != 1 || attachments[0]["filename"] != "invoice.pdf" {
		t.Fatalf("attachments %v", attachments)
	}
	if got := htmlToText(`<style>p{}</style><div>Hello&nbsp;<b>there</b></div><div>Line two<br>three</div>`); got != "Hello there\nLine two\nthree" {
		t.Fatalf("html to text %q", got)
	}
}

func TestSpreadsheetHelpers(t *testing.T) {
	for input, want := range map[string]string{
		"https://docs.google.com/spreadsheets/d/1AbCdEfGhIjKlMnOpQrStUvWxYz/edit#gid=0": "1AbCdEfGhIjKlMnOpQrStUvWxYz",
		"1AbCdEfGhIjKlMnOpQrStUvWxYz": "1AbCdEfGhIjKlMnOpQrStUvWxYz",
	} {
		if got, err := parseSpreadsheet(input); err != nil || got != want {
			t.Fatalf("parseSpreadsheet(%q) = %q, %v", input, got, err)
		}
	}
	if _, err := parseSpreadsheet("budget"); err == nil {
		t.Fatal("a name is not an id")
	}
	if got := driveQueryValue(`Bob's \ plan`); got != `'Bob\'s \\ plan'` {
		t.Fatalf("driveQueryValue = %s", got)
	}
	rows, truncated := limitCells([][]any{{1, 2}, {3, 4}, {5, 6}}, 5)
	if len(rows) != 2 || !truncated {
		t.Fatalf("limitCells kept %d rows", len(rows))
	}
}
