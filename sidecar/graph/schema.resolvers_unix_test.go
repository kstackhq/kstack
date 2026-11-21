//go:build !windows

package graph_test

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/kstackhq/kstack/sidecar/internal/agent/chat"
	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
)

// newFolderServer is a chat server on a machine with a sandbox, whose home is
// a fresh folder holding a never-readable .ssh and a code folder.
func newFolderServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	return newFolderServerWith(t, "")
}

// newFolderServerWith is newFolderServer over a settings file
// holding body ("" for none).
func newFolderServerWith(t *testing.T, body string) (*httptest.Server, string) {
	t.Helper()
	home := testutil.GrantableDir(t)
	for _, d := range []string{".ssh", "code", "Documents"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, d), 0o755))
	}
	file := filepath.Join(t.TempDir(), "settings.json")
	if body != "" {
		require.NoError(t, os.WriteFile(file, []byte(body), 0o600))
	}
	store, err := settings.Open(file)
	require.NoError(t, err)
	security := settings.NewService(store, func() settings.Zones {
		return settings.Zones{
			Never: []string{filepath.Join(home, ".ssh")},
			Open:  sandbox.FilePolicy{Deny: []string{filepath.Join(home, "Documents")}},
			Home:  home,
		}
	}, nil, "")
	srv, _, _ := newChatServerOn(t, sandbox.Status{Available: true}, security)
	return srv, home
}

// gqlResult is one response: its data, and its errors' messages and extensions.
type gqlResult struct {
	Data   map[string]any
	Errors []struct {
		Message    string
		Extensions map[string]any
	}
}

func post(t *testing.T, srv *httptest.Server, query string) gqlResult {
	t.Helper()
	body, err := json.Marshal(map[string]string{"query": query})
	require.NoError(t, err)
	raw := postGQL(t, srv.URL, string(body))
	var res gqlResult
	require.NoError(t, json.Unmarshal(raw, &res), "%s", raw)
	return res
}

// newChatOn sends a first question, so the server holds a chat, and answers
// its id.
func newChatOn(t *testing.T, srv *httptest.Server) string {
	t.Helper()
	sent := mutate(t, srv, `mutation { chatSend(mode: Chat, clusterID: "1", sandboxDisabled: false, networkEnabled: false, networkThisTurn: false, providerID: "fake", modelID: "fake", effort: "high",
		requestID: "`+appdb.NewID()+`", content: "hi") { chatID } }`)
	return sent["chatSend"].(map[string]any)["chatID"].(string)
}

const foldersFields = `{ always { id path write refused } chat { id path write refused } never closed wide rulesHeld }`

func TestFolderGrantAndRevoke(t *testing.T) {
	srv, home := newFolderServer(t)
	code := filepath.Join(home, "code")

	got := mutate(t, srv, `mutation { folderGrant(path: "`+code+`", write: false, duration: Always) `+foldersFields+` }`)
	folders := got["folderGrant"].(map[string]any)
	always := folders["always"].([]any)
	require.Len(t, always, 1)
	first := always[0].(map[string]any)
	assert.Equal(t, code, first["path"])
	assert.Equal(t, false, first["write"])
	assert.Nil(t, first["refused"])
	assert.Equal(t, []any{filepath.Join(home, ".ssh")}, folders["never"])
	assert.Equal(t, []any{filepath.Join(home, "Documents")}, folders["closed"])
	assert.Contains(t, folders["wide"], home)

	got = mutate(t, srv, `mutation { folderGrant(path: "`+code+`", write: true, duration: Always) `+foldersFields+` }`)
	always = got["folderGrant"].(map[string]any)["always"].([]any)
	require.Len(t, always, 1, "a grant in the same place changes its mode")
	assert.Equal(t, first["id"], always[0].(map[string]any)["id"])
	assert.Equal(t, true, always[0].(map[string]any)["write"])

	got = mutate(t, srv, `{ permissionSettings { rules { folder line } } }`)
	assert.Equal(t, []any{map[string]any{"folder": code, "line": "Allow reads and writes of " + code}},
		got["permissionSettings"].(map[string]any)["rules"], "the Permissions section lists it by its line")

	got = mutate(t, srv, `mutation { folderRevoke(id: "`+first["id"].(string)+`") `+foldersFields+` }`)
	assert.Empty(t, got["folderRevoke"].(map[string]any)["always"])
	res := post(t, srv, `mutation { folderRevoke(id: "`+first["id"].(string)+`") { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", res.Errors[0].Extensions["code"])
}

func TestAChatsFolderGrantIsListedAndRemoved(t *testing.T) {
	srv, home := newFolderServer(t)
	code := filepath.Join(home, "code")
	chat := newChatOn(t, srv)

	got := mutate(t, srv, `mutation { folderGrant(chatID: "`+chat+`", path: "`+code+`", write: false, duration: Chat) `+foldersFields+` }`)
	assert.Empty(t, got["folderGrant"].(map[string]any)["always"])
	chatFolders := got["folderGrant"].(map[string]any)["chat"].([]any)
	require.Len(t, chatFolders, 1)
	id := chatFolders[0].(map[string]any)["id"].(string)

	got = mutate(t, srv, `{ chatGrants(chatID: "`+chat+`") { id line folder } }`)
	assert.Equal(t, []any{map[string]any{"id": id, "line": "Allow reads of " + code, "folder": code}}, got["chatGrants"])
	got = mutate(t, srv, `{ sandboxFolders(chatID: "`+chat+`") `+foldersFields+` }`)
	assert.Len(t, got["sandboxFolders"].(map[string]any)["chat"], 1, "the chat's folders are listed for it")

	got = mutate(t, srv, `mutation { chatGrantRemove(chatID: "`+chat+`", id: "`+id+`") { id } }`)
	assert.Equal(t, []any{}, got["chatGrantRemove"])
	for name, query := range map[string]string{
		"an unknown id":  `mutation { chatGrantRemove(chatID: "` + chat + `", id: "` + id + `") { id } }`,
		"a deleted chat": `mutation { chatGrantRemove(chatID: "` + appdb.NewID() + `", id: "` + id + `") { id } }`,
		"a chat's grant": `mutation { folderGrant(chatID: "` + appdb.NewID() + `", path: "` + code + `", write: false, duration: Chat) { wide } }`,
	} {
		res := post(t, srv, query)
		require.Len(t, res.Errors, 1, name)
		assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", res.Errors[0].Extensions["code"], name)
	}
	for _, chatID := range []string{"", `chatID: "", `} {
		res := post(t, srv, `mutation { folderGrant(`+chatID+`path: "`+code+`", write: false, duration: Chat) { wide } }`)
		require.Len(t, res.Errors, 1)
		assert.Equal(t, "KSTACK_VALIDATION_ERROR", res.Errors[0].Extensions["code"], "a chat's grant names its chat")
	}
	got = mutate(t, srv, `{ sandboxFolders `+foldersFields+` }`)
	assert.Empty(t, got["sandboxFolders"].(map[string]any)["always"], "and never widens to every chat")
}

// foldersFrame is the first sandboxFoldersWatch frame on events whose always
// and chat lists hold always and chat grants: a gauge over two sources can say
// one thing twice, so a frame is never counted.
func foldersFrame(t *testing.T, events <-chan sseEvent, always, chat int) map[string]any {
	t.Helper()
	for {
		ev := testutil.Recv(t, events, "a sandboxFoldersWatch frame")
		var payload struct {
			Data struct {
				Folders map[string]any `json:"sandboxFoldersWatch"`
			} `json:"data"`
		}
		require.NoError(t, json.Unmarshal([]byte(ev.data), &payload), ev.data)
		folders := payload.Data.Folders
		if len(folders["always"].([]any)) == always && len(folders["chat"].([]any)) == chat {
			return folders
		}
	}
}

// The watch answers the folders on subscribe, and again after an always grant
// or revoke and after a write or revoke of the chat's own; one naming no chat
// follows the always grants alone.
func TestSandboxFoldersWatchFollowsEveryGrant(t *testing.T) {
	srv, home := newFolderServer(t)
	code := filepath.Join(home, "code")
	chatID := newChatOn(t, srv)

	resp := openSSESubscription(t, srv.URL, "", `subscription { sandboxFoldersWatch(chatID: "`+chatID+`") `+foldersFields+` }`)
	defer resp.Body.Close()
	events := sseEvents(t, resp)
	first := foldersFrame(t, events, 0, 0)
	assert.Equal(t, []any{filepath.Join(home, ".ssh")}, first["never"])
	assert.Contains(t, first["wide"], home)
	assert.Equal(t, false, first["rulesHeld"])

	unscoped := openSSESubscription(t, srv.URL, "", `subscription { sandboxFoldersWatch `+foldersFields+` }`)
	defer unscoped.Body.Close()
	alone := sseEvents(t, unscoped)
	foldersFrame(t, alone, 0, 0)

	got := mutate(t, srv, `mutation { folderGrant(path: "`+code+`", write: false, duration: Always) { always { id } } }`)
	alwaysID := got["folderGrant"].(map[string]any)["always"].([]any)[0].(map[string]any)["id"].(string)
	foldersFrame(t, events, 1, 0)
	foldersFrame(t, alone, 1, 0)

	got = mutate(t, srv, `mutation { folderGrant(chatID: "`+chatID+`", path: "`+code+`", write: true, duration: Chat) { chat { id } } }`)
	chatGrantID := got["folderGrant"].(map[string]any)["chat"].([]any)[0].(map[string]any)["id"].(string)
	granted := foldersFrame(t, events, 1, 1)
	assert.Equal(t, true, granted["chat"].([]any)[0].(map[string]any)["write"])

	mutate(t, srv, `mutation { chatGrantRemove(chatID: "`+chatID+`", id: "`+chatGrantID+`") { id } }`)
	foldersFrame(t, events, 1, 0)

	mutate(t, srv, `mutation { folderRevoke(id: "`+alwaysID+`") { wide } }`)
	foldersFrame(t, events, 0, 0)
	foldersFrame(t, alone, 0, 0)
}

func TestAFolderRefusalCarriesItsReason(t *testing.T) {
	srv, home := newFolderServer(t)
	link := filepath.Join(home, "link")
	require.NoError(t, os.Symlink(filepath.Join(home, "code"), link))

	res := post(t, srv, `mutation { folderGrant(path: "`+link+`", write: false, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	e := res.Errors[0]
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", e.Extensions["code"])
	assert.Equal(t, "link", e.Extensions["rule"])
	assert.Equal(t, filepath.Join(home, "code"), e.Extensions["target"])
	assert.Equal(t, link+" is a link to "+filepath.Join(home, "code")+"; grant "+filepath.Join(home, "code")+" instead.", e.Message)

	res = post(t, srv, `mutation { folderGrant(path: "`+home+`", write: true, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "Your home can be granted read-only.", res.Errors[0].Message)
	assert.Equal(t, "home", res.Errors[0].Extensions["rule"])
}

func TestARefusedFolderIsDrawnRefused(t *testing.T) {
	srv, home := newFolderServer(t)
	gone := filepath.Join(home, "gone")
	require.NoError(t, os.MkdirAll(gone, 0o755))
	mutate(t, srv, `mutation { folderGrant(path: "`+gone+`", write: false, duration: Always) { wide } }`)
	require.NoError(t, os.Remove(gone))

	got := mutate(t, srv, `{ sandboxFolders `+foldersFields+` }`)
	always := got["sandboxFolders"].(map[string]any)["always"].([]any)
	require.Len(t, always, 1)
	assert.Equal(t, "This folder does not exist.", always[0].(map[string]any)["refused"])
}

// A rule Kstack cannot read holds the rules field, which the folders say, so
// Settings can draw Remove disabled rather than offer a revoke it refuses.
func TestSandboxFoldersSayTheRulesAreHeld(t *testing.T) {
	srv, home := newFolderServerWith(t, `{"permissions": {"rules": [{"id": "b", "effect": "deny", "class": 9}]}}`)
	got := mutate(t, srv, `{ sandboxFolders `+foldersFields+` }`)
	assert.Equal(t, true, got["sandboxFolders"].(map[string]any)["rulesHeld"])

	res := post(t, srv, `mutation { folderGrant(path: "`+filepath.Join(home, "code")+`", write: false, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", res.Errors[0].Extensions["code"])
	assert.Equal(t, chat.RulesHeldReason, res.Errors[0].Message)
}

func TestSandboxFoldersWithNoSandbox(t *testing.T) {
	srv := newChatServer(t)
	got := mutate(t, srv, `{ sandboxFolders `+foldersFields+` }`)
	assert.Equal(t, map[string]any{"always": []any{}, "chat": []any{}, "never": []any{}, "closed": []any{}, "wide": []any{}, "rulesHeld": false}, got["sandboxFolders"])

	res := post(t, srv, `mutation { folderGrant(path: "/home/me/code", write: false, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", res.Errors[0].Extensions["code"])
}
