package rex

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkspaceFileReadRejectsSymlinkEscape(t *testing.T) {
	host := newSetupTestHost(t)
	root, outside := t.TempDir(), t.TempDir()
	bot := &Bot{ID: "files", Name: "Files", WorkspaceDir: root, Capabilities: Capabilities{FilesRead: true}}
	if err := host.Store.SaveBot(context.Background(), bot); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "inside.txt"), []byte("inside"), 0600); err != nil {
		t.Fatal(err)
	}
	secret := filepath.Join(outside, "secret.txt")
	if err := os.WriteFile(secret, []byte("outside fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(root, "escape.txt")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	for _, test := range []struct {
		path   string
		status int
	}{{"inside.txt", http.StatusOK}, {"escape.txt", http.StatusNotFound}} {
		request := httptest.NewRequest("GET", "/api/bots/files/file?path="+test.path, nil)
		request.SetPathValue("id", bot.ID)
		response := httptest.NewRecorder()
		host.readFile(response, request)
		if response.Code != test.status || strings.Contains(response.Body.String(), "outside fixture") {
			t.Fatalf("file %s: status=%d body=%s", test.path, response.Code, response.Body)
		}
	}
}

func TestWorkspaceUploadRejectsOutsideInbox(t *testing.T) {
	host := newSetupTestHost(t)
	root, outside := t.TempDir(), t.TempDir()
	bot := &Bot{ID: "files", Name: "Files", WorkspaceDir: root, Capabilities: Capabilities{FilesRead: true}}
	if err := host.Store.SaveBot(context.Background(), bot); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "inbox")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	file, err := writer.CreateFormFile("file", "attachment.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.Write([]byte("attachment")); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest("POST", "/api/bots/files/files", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	request.SetPathValue("id", bot.ID)
	response := httptest.NewRecorder()
	host.uploadFile(response, request)
	if response.Code < 400 {
		t.Fatalf("accepted outside inbox: %s", response.Body)
	}
	if _, err := os.Stat(filepath.Join(outside, "attachment.txt")); !os.IsNotExist(err) {
		t.Fatalf("upload escaped workspace: %v", err)
	}
}
