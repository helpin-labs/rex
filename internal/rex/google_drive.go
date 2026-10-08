package rex

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"regexp"
	"strings"
)

// Google Drive's tools. Searching and reading run on their own; creating
// files follows the bot's approvals. Rex asks for drive.readonly (find and
// read any file) and drive.file (create files, and change only the files
// Rex created), so a bot can't edit or delete the user's existing files.

const (
	driveReadScope  = "https://www.googleapis.com/auth/drive.readonly"
	driveFileScope  = "https://www.googleapis.com/auth/drive.file"
	driveReadLimit  = 40000
	driveFileFields = "id,name,mimeType,modifiedTime,size,webViewLink,owners(displayName),parents"
)

var driveService = &googleService{
	name:        "Google Drive",
	permissions: []string{driveReadScope, driveFileScope},
	tools: []googleTool{
		{
			name:        "search_files",
			description: "Search the user's Drive by words in file names and contents, most recently changed first. Narrow by kind or folder.",
			access:      "read",
			schema: objectSchema(nil, map[string]any{
				"query":       map[string]any{"type": "string", "description": "Words to find in names or contents. Empty lists recent files."},
				"kind":        map[string]any{"type": "string", "enum": driveKindNames()},
				"folder_id":   map[string]any{"type": "string", "description": "Only files directly in this folder (id or link)."},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 50, "description": "20 by default."},
				"page_token":  map[string]any{"type": "string"},
			}),
			call: driveSearch,
		},
		{
			name:        "list_folder",
			description: "List what is in a folder, folders first. Without a folder, the top of My Drive.",
			access:      "read",
			schema: objectSchema(nil, map[string]any{
				"folder_id":   map[string]any{"type": "string", "description": "A folder's id or link."},
				"max_results": map[string]any{"type": "integer", "minimum": 1, "maximum": 100, "description": "50 by default."},
				"page_token":  map[string]any{"type": "string"},
			}),
			call: driveListFolder,
		},
		{
			name:        "read_file",
			description: "Read a file's text: Google Docs as Markdown, Sheets as CSV (first sheet), Slides as text, and plain text files. PDFs, images and Office files can't be read here.",
			access:      "read",
			schema: objectSchema([]string{"file"}, map[string]any{
				"file": map[string]any{"type": "string", "description": "The file's id or any Google link to it."},
			}),
			call: driveRead,
		},
		{
			name:        "create_document",
			description: "Create a Google Doc from Markdown (headings, lists, bold, links and tables carry over).",
			access:      "write",
			schema: objectSchema([]string{"title", "content"}, map[string]any{
				"title":     map[string]any{"type": "string", "minLength": 1, "maxLength": 300},
				"content":   map[string]any{"type": "string", "maxLength": 500000},
				"folder_id": map[string]any{"type": "string", "description": "Where to put it (id or link); My Drive by default."},
			}),
			call: driveCreateDocument,
		},
		{
			name:        "create_folder",
			description: "Create a folder in the user's Drive.",
			access:      "write",
			schema: objectSchema([]string{"name"}, map[string]any{
				"name":      map[string]any{"type": "string", "minLength": 1, "maxLength": 300},
				"parent_id": map[string]any{"type": "string", "description": "The folder to put it in (id or link); My Drive by default."},
			}),
			call: driveCreateFolder,
		},
	},
}

const (
	googleDocType    = "application/vnd.google-apps.document"
	googleSheetType  = "application/vnd.google-apps.spreadsheet"
	googleSlidesType = "application/vnd.google-apps.presentation"
	googleFolderType = "application/vnd.google-apps.folder"
)

// driveKinds maps the kinds a bot can ask for to Drive search terms.
var driveKinds = map[string]string{
	"document":     "mimeType='" + googleDocType + "'",
	"spreadsheet":  "mimeType='" + googleSheetType + "'",
	"presentation": "mimeType='" + googleSlidesType + "'",
	"folder":       "mimeType='" + googleFolderType + "'",
	"pdf":          "mimeType='application/pdf'",
	"image":        "mimeType contains 'image/'",
}

func driveKindNames() []string {
	return []string{"document", "spreadsheet", "presentation", "folder", "pdf", "image"}
}

// kindOf names a file's type in plain words.
func kindOf(mimeType string) string {
	switch mimeType {
	case googleDocType:
		return "document"
	case googleSheetType:
		return "spreadsheet"
	case googleSlidesType:
		return "presentation"
	case googleFolderType:
		return "folder"
	case "application/pdf":
		return "pdf"
	}
	if strings.HasPrefix(mimeType, "image/") {
		return "image"
	}
	return mimeType
}

var (
	driveLinkID  = regexp.MustCompile(`/(?:d|folders)/([A-Za-z0-9_-]{10,})`)
	driveQueryID = regexp.MustCompile(`[?&]id=([A-Za-z0-9_-]{10,})`)
	driveBareID  = regexp.MustCompile(`^[A-Za-z0-9_-]{10,}$`)
)

// parseDriveID accepts a file or folder id, or any Google link to it.
func parseDriveID(value string) (string, error) {
	value = strings.TrimSpace(value)
	for _, pattern := range []*regexp.Regexp{driveLinkID, driveQueryID} {
		if match := pattern.FindStringSubmatch(value); match != nil {
			return match[1], nil
		}
	}
	if driveBareID.MatchString(value) || value == "root" {
		return value, nil
	}
	return "", fmt.Errorf("%q is not a Drive file id or link; use search_files", value)
}

type driveFile struct {
	ID           string   `json:"id"`
	Name         string   `json:"name"`
	MimeType     string   `json:"mimeType"`
	ModifiedTime string   `json:"modifiedTime"`
	Size         string   `json:"size"`
	WebViewLink  string   `json:"webViewLink"`
	Parents      []string `json:"parents"`
	Owners       []struct {
		DisplayName string `json:"displayName"`
	} `json:"owners"`
}

func (f driveFile) summary() map[string]any {
	item := map[string]any{"id": f.ID, "name": f.Name, "kind": kindOf(f.MimeType), "modified": f.ModifiedTime, "link": f.WebViewLink}
	if f.Size != "" {
		item["size"] = f.Size
	}
	if len(f.Owners) > 0 {
		item["owner"] = f.Owners[0].DisplayName
	}
	return item
}

// driveList runs a Drive search and reports its files.
func driveList(ctx context.Context, api *googleAPI, search, order string, limit int, pageToken string) (any, error) {
	query := url.Values{
		"q": {search}, "pageSize": {fmt.Sprint(limit)}, "fields": {"nextPageToken,files(" + driveFileFields + ")"},
		"supportsAllDrives": {"true"}, "includeItemsFromAllDrives": {"true"},
	}
	if order != "" {
		query.Set("orderBy", order)
	}
	if pageToken != "" {
		query.Set("pageToken", pageToken)
	}
	var list struct {
		Files         []driveFile `json:"files"`
		NextPageToken string      `json:"nextPageToken"`
	}
	if err := api.do(ctx, http.MethodGet, driveAPI+"/files?"+query.Encode(), nil, &list); err != nil {
		return nil, err
	}
	files := make([]map[string]any, 0, len(list.Files))
	for _, file := range list.Files {
		files = append(files, file.summary())
	}
	result := map[string]any{"files": files}
	if list.NextPageToken != "" {
		result["next_page_token"] = list.NextPageToken
	}
	return result, nil
}

func driveSearch(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Query      string `json:"query"`
		Kind       string `json:"kind"`
		FolderID   string `json:"folder_id"`
		MaxResults int    `json:"max_results"`
		PageToken  string `json:"page_token"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	terms := []string{"trashed=false"}
	if args.Kind != "" {
		term, ok := driveKinds[args.Kind]
		if !ok {
			return nil, fmt.Errorf("kind must be one of %s", strings.Join(driveKindNames(), ", "))
		}
		terms = append(terms, term)
	}
	if args.FolderID != "" {
		folder, err := parseDriveID(args.FolderID)
		if err != nil {
			return nil, err
		}
		terms = append(terms, driveQueryValue(folder)+" in parents")
	}
	if words := strings.TrimSpace(args.Query); words != "" {
		terms = append(terms, "(name contains "+driveQueryValue(words)+" or fullText contains "+driveQueryValue(words)+")")
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 20
	}
	// Drive can't order a full-text search; it ranks by relevance then.
	order := "modifiedTime desc"
	if strings.TrimSpace(args.Query) != "" {
		order = ""
	}
	return driveList(ctx, api, strings.Join(terms, " and "), order, min(args.MaxResults, 50), args.PageToken)
}

func driveListFolder(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		FolderID   string `json:"folder_id"`
		MaxResults int    `json:"max_results"`
		PageToken  string `json:"page_token"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	folder := "root"
	if args.FolderID != "" {
		var err error
		if folder, err = parseDriveID(args.FolderID); err != nil {
			return nil, err
		}
	}
	if args.MaxResults <= 0 {
		args.MaxResults = 50
	}
	return driveList(ctx, api, driveQueryValue(folder)+" in parents and trashed=false", "folder,name", min(args.MaxResults, 100), args.PageToken)
}

// readableText says whether a file's own bytes are text a model can read.
func readableText(mimeType string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	for _, kind := range []string{"json", "xml", "yaml", "javascript", "csv", "markdown", "x-sh", "sql"} {
		if strings.Contains(mimeType, kind) {
			return true
		}
	}
	return false
}

func driveRead(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		File string `json:"file"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	id, err := parseDriveID(args.File)
	if err != nil {
		return nil, err
	}
	var file driveFile
	if err := api.do(ctx, http.MethodGet, driveAPI+"/files/"+url.PathEscape(id)+"?supportsAllDrives=true&fields="+url.QueryEscape(driveFileFields), nil, &file); err != nil {
		return nil, err
	}
	export := func(mimeType string) ([]byte, error) {
		return api.send(ctx, http.MethodGet, driveAPI+"/files/"+url.PathEscape(id)+"/export?mimeType="+url.QueryEscape(mimeType), "", nil, driveReadLimit*4)
	}
	var content []byte
	format := ""
	switch {
	case file.MimeType == googleDocType:
		format = "markdown"
		if content, err = export("text/markdown"); err != nil {
			format = "text"
			content, err = export("text/plain")
		}
	case file.MimeType == googleSheetType:
		format = "csv (first sheet; use Google Sheets for the others)"
		content, err = export("text/csv")
	case file.MimeType == googleSlidesType:
		format = "text"
		content, err = export("text/plain")
	case file.MimeType == googleFolderType:
		return nil, errors.New("this is a folder; use list_folder")
	case readableText(file.MimeType):
		format = "text"
		content, err = api.send(ctx, http.MethodGet, driveAPI+"/files/"+url.PathEscape(id)+"?alt=media&supportsAllDrives=true", "", nil, driveReadLimit*4)
	default:
		return nil, fmt.Errorf("%s is a %s file, which can't be read as text here; share its link instead (%s)", file.Name, kindOf(file.MimeType), file.WebViewLink)
	}
	if err != nil {
		return nil, err
	}
	text, truncated := excerptRunes(strings.ToValidUTF8(string(content), ""), driveReadLimit)
	result := file.summary()
	result["format"], result["content"] = format, text
	if truncated {
		result["truncated"] = true
	}
	return result, nil
}

// driveCreate creates a file from metadata and, when content is given,
// uploads it in the same request (Drive converts Markdown to a Doc).
func driveCreate(ctx context.Context, api *googleAPI, metadata map[string]any, contentType string, content []byte) (*driveFile, error) {
	var created driveFile
	if content == nil {
		if err := api.do(ctx, http.MethodPost, driveAPI+"/files?supportsAllDrives=true&fields="+url.QueryEscape(driveFileFields), metadata, &created); err != nil {
			return nil, err
		}
		return &created, nil
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	meta, _ := json.Marshal(metadata)
	for _, part := range []struct {
		contentType string
		data        []byte
	}{{"application/json; charset=UTF-8", meta}, {contentType, content}} {
		section, err := writer.CreatePart(textproto.MIMEHeader{"Content-Type": {part.contentType}})
		if err != nil {
			return nil, err
		}
		_, _ = section.Write(part.data)
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	endpoint := driveUploadAPI + "/files?uploadType=multipart&supportsAllDrives=true&fields=" + url.QueryEscape(driveFileFields)
	raw, err := api.send(ctx, http.MethodPost, endpoint, "multipart/related; boundary="+writer.Boundary(), &body, 1<<20)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &created); err != nil {
		return nil, err
	}
	return &created, nil
}

func parentsFor(folder string) ([]string, error) {
	if strings.TrimSpace(folder) == "" {
		return nil, nil
	}
	id, err := parseDriveID(folder)
	if err != nil {
		return nil, err
	}
	return []string{id}, nil
}

func driveCreateDocument(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Title    string `json:"title"`
		Content  string `json:"content"`
		FolderID string `json:"folder_id"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Title) == "" {
		return nil, errors.New("title is required")
	}
	metadata := map[string]any{"name": strings.TrimSpace(args.Title), "mimeType": googleDocType}
	parents, err := parentsFor(args.FolderID)
	if err != nil {
		return nil, err
	}
	if parents != nil {
		metadata["parents"] = parents
	}
	created, err := driveCreate(ctx, api, metadata, "text/markdown; charset=UTF-8", []byte(args.Content))
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "created", "file": created.summary()}, nil
}

func driveCreateFolder(ctx context.Context, api *googleAPI, input json.RawMessage) (any, error) {
	var args struct {
		Name     string `json:"name"`
		ParentID string `json:"parent_id"`
	}
	if err := decodeInput(input, &args); err != nil {
		return nil, err
	}
	if strings.TrimSpace(args.Name) == "" {
		return nil, errors.New("name is required")
	}
	metadata := map[string]any{"name": strings.TrimSpace(args.Name), "mimeType": googleFolderType}
	parents, err := parentsFor(args.ParentID)
	if err != nil {
		return nil, err
	}
	if parents != nil {
		metadata["parents"] = parents
	}
	created, err := driveCreate(ctx, api, metadata, "", nil)
	if err != nil {
		return nil, err
	}
	return map[string]any{"status": "created", "folder": created.summary()}, nil
}
