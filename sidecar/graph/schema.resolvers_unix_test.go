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

	"github.com/kstackhq/kstack/sidecar/internal/appdb"
	"github.com/kstackhq/kstack/sidecar/internal/appdb/appdbtest"
	"github.com/kstackhq/kstack/sidecar/internal/lib/testutil"
	"github.com/kstackhq/kstack/sidecar/internal/run/sandbox"
	"github.com/kstackhq/kstack/sidecar/internal/services/settings"
)

// newFolderServer is a chat server on a machine with a sandbox, whose home is
// a fresh folder holding a never-readable .ssh and a code folder.
func newFolderServer(t *testing.T) (*httptest.Server, string) {
	t.Helper()
	home := testutil.GrantableDir(t)
	for _, d := range []string{".ssh", "code", "Documents"} {
		require.NoError(t, os.MkdirAll(filepath.Join(home, d), 0o755))
	}
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"), appdbtest.Open(t))
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

const foldersFields = `{ always { id path write refusal } chat { id path write refusal } never closed wide }`

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
	assert.Nil(t, first["refusal"])
	assert.Equal(t, []any{filepath.Join(home, ".ssh")}, folders["never"])
	assert.Equal(t, []any{filepath.Join(home, "Documents")}, folders["closed"])
	assert.Contains(t, folders["wide"], home)

	got = mutate(t, srv, `mutation { folderGrant(path: "`+code+`", write: true, duration: Always) `+foldersFields+` }`)
	always = got["folderGrant"].(map[string]any)["always"].([]any)
	require.Len(t, always, 1, "a grant in the same place changes its mode")
	assert.Equal(t, first["id"], always[0].(map[string]any)["id"])
	assert.Equal(t, true, always[0].(map[string]any)["write"])

	got = mutate(t, srv, `{ permissionSettings { kubeRules { id } } }`)
	assert.Empty(t, got["permissionSettings"].(map[string]any)["kubeRules"], "a folder grant is no rule")

	got = mutate(t, srv, `mutation { folderRevoke(id: "`+first["id"].(string)+`", duration: Always) `+foldersFields+` }`)
	assert.Empty(t, got["folderRevoke"].(map[string]any)["always"])
	res := post(t, srv, `mutation { folderRevoke(id: "`+first["id"].(string)+`", duration: Always) { wide } }`)
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

	got = mutate(t, srv, `{ chatKubeRules(chatID: "`+chat+`") { id } }`)
	assert.Equal(t, []any{}, got["chatKubeRules"], "a folder grant is no rule")
	got = mutate(t, srv, `{ sandboxFolders(chatID: "`+chat+`") `+foldersFields+` }`)
	assert.Len(t, got["sandboxFolders"].(map[string]any)["chat"], 1, "the chat's folders are listed for it")

	res := post(t, srv, `mutation { folderRevoke(id: "`+id+`", duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", res.Errors[0].Extensions["code"], "a chat's grant is no always one")
	got = mutate(t, srv, `mutation { folderRevoke(chatID: "`+chat+`", id: "`+id+`", duration: Chat) `+foldersFields+` }`)
	assert.Empty(t, got["folderRevoke"].(map[string]any)["chat"])
	for name, query := range map[string]string{
		"an unknown id":  `mutation { folderRevoke(chatID: "` + chat + `", id: "` + id + `", duration: Chat) { wide } }`,
		"a deleted chat": `mutation { folderRevoke(chatID: "` + appdb.NewID() + `", id: "` + id + `", duration: Chat) { wide } }`,
		"a chat's grant": `mutation { folderGrant(chatID: "` + appdb.NewID() + `", path: "` + code + `", write: false, duration: Chat) { wide } }`,
	} {
		res := post(t, srv, query)
		require.Len(t, res.Errors, 1, name)
		assert.Equal(t, "KSTACK_RECORD_NOT_FOUND", res.Errors[0].Extensions["code"], name)
	}
	for _, chatID := range []string{"", `chatID: "", `} {
		for _, query := range []string{
			`mutation { folderGrant(` + chatID + `path: "` + code + `", write: false, duration: Chat) { wide } }`,
			`mutation { folderRevoke(` + chatID + `id: "` + id + `", duration: Chat) { wide } }`,
		} {
			res := post(t, srv, query)
			require.Len(t, res.Errors, 1)
			assert.Equal(t, "KSTACK_VALIDATION_ERROR", res.Errors[0].Extensions["code"], "a chat's grant names its chat")
		}
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

	mutate(t, srv, `mutation { folderRevoke(chatID: "`+chatID+`", id: "`+chatGrantID+`", duration: Chat) { wide } }`)
	foldersFrame(t, events, 1, 0)

	mutate(t, srv, `mutation { folderRevoke(id: "`+alwaysID+`", duration: Always) { wide } }`)
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
	assert.Equal(t, "This folder does not exist.", always[0].(map[string]any)["refusal"])
}

func TestSandboxFoldersWithNoSandbox(t *testing.T) {
	srv := newChatServer(t)
	got := mutate(t, srv, `{ sandboxFolders `+foldersFields+` }`)
	assert.Equal(t, map[string]any{"always": []any{}, "chat": []any{}, "never": []any{}, "closed": []any{}, "wide": []any{}}, got["sandboxFolders"])

	res := post(t, srv, `mutation { folderGrant(path: "/home/me/code", write: false, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_VALIDATION_ERROR", res.Errors[0].Extensions["code"])
}

// A read of the folders that fails is the query's error, never empty lists,
// and ends the watch with its reason, so the client reconnects.
func TestSandboxFoldersFailWithTheirRead(t *testing.T) {
	home := testutil.GrantableDir(t)
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"), appdbtest.Open(t))
	require.NoError(t, err)
	security := settings.NewService(store, func() settings.Zones { return settings.Zones{Home: home} }, nil, "")
	srv, db, _ := newChatServerOn(t, sandbox.Status{Available: true}, security)
	chatID := newChatOn(t, srv)
	_, err = db.Write.Exec(`DROP TABLE folder_grants`)
	require.NoError(t, err)

	res := post(t, srv, `{ sandboxFolders(chatID: "`+chatID+`") { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Contains(t, res.Errors[0].Message, "folder_grants")

	resp := openSSESubscription(t, srv.URL, "", `subscription { sandboxFoldersWatch(chatID: "`+chatID+`") { wide } }`)
	defer resp.Body.Close()
	ev := testutil.Recv(t, sseEvents(t, resp), "the failure frame")
	assert.Contains(t, ev.data, `"watchFailed":true`)
	assert.Contains(t, ev.data, "read the sandbox folders")
}

// A grant or revoke whose write took, though the lists it answers could not be
// read after, says the write took, so no one reads it as a refusal.
func TestAFolderWriteThatTookSaysSoWhenTheListsFail(t *testing.T) {
	home := testutil.GrantableDir(t)
	code := filepath.Join(home, "code")
	require.NoError(t, os.MkdirAll(code, 0o755))
	settingsDB := appdbtest.Open(t)
	store, err := settings.Open(filepath.Join(t.TempDir(), "settings.json"), settingsDB)
	require.NoError(t, err)
	security := settings.NewService(store, func() settings.Zones { return settings.Zones{Home: home} }, nil, "")
	srv, _, _ := newChatServerOn(t, sandbox.Status{Available: true}, security)
	// A row whose folder no read can scan, beside the index the grant's upsert needs.
	for _, q := range []string{
		`DROP TABLE folder_grants`,
		`CREATE TABLE folder_grants (id, chat_id, folder, access, created_at)`,
		`CREATE UNIQUE INDEX folder_grants_folder ON folder_grants (ifnull(chat_id, ''), folder)`,
		`INSERT INTO folder_grants VALUES ('bad', NULL, NULL, 'read', 0)`,
	} {
		_, err := settingsDB.Write.Exec(q)
		require.NoError(t, err)
	}

	res := post(t, srv, `mutation { folderGrant(path: "`+code+`", write: false, duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_SAVED_UNREAD", res.Errors[0].Extensions["code"])
	assert.Contains(t, res.Errors[0].Message, "The folder was granted, but Kstack could not read the settings back")
	var id string
	require.NoError(t, settingsDB.Read.QueryRow(`SELECT id FROM folder_grants WHERE folder = ?`, code).Scan(&id))

	res = post(t, srv, `mutation { folderRevoke(id: "`+id+`", duration: Always) { wide } }`)
	require.Len(t, res.Errors, 1)
	assert.Equal(t, "KSTACK_SAVED_UNREAD", res.Errors[0].Extensions["code"])
	assert.Contains(t, res.Errors[0].Message, "The grant was removed, but Kstack could not read the settings back")
	var n int
	require.NoError(t, settingsDB.Read.QueryRow(`SELECT count(*) FROM folder_grants WHERE folder = ?`, code).Scan(&n))
	assert.Zero(t, n)
}
