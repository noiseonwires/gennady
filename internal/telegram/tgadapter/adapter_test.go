// SPDX-License-Identifier: AGPL-3.0-only
// Copyright (c) 2025 Kirill aka Noiseonwires
package tgadapter

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	tgbot "github.com/go-telegram/bot"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"gennadium/internal/telegram"
)

// fakeTelegram is an httptest-backed stand-in for the Telegram Bot API. It maps
// a method name (the last path segment of /bot<token>/<method>) to a canned
// JSON response and records the form values of the last request per method.
type fakeTelegram struct {
	server    *httptest.Server
	responses map[string]string
	lastForm  map[string]url.Values
}

func newFakeTelegram(t *testing.T) *fakeTelegram {
	t.Helper()
	f := &fakeTelegram{
		responses: map[string]string{
			// getMe is called by the constructor.
			"getMe": `{"ok":true,"result":{"id":42,"is_bot":true,"username":"testbot","first_name":"Test"}}`,
		},
		lastForm: map[string]url.Values{},
	}
	f.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method := r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:]
		// The library sends requests as multipart/form-data; collect the
		// non-file form values so tests can inspect what was sent.
		vals := url.Values{}
		if err := r.ParseMultipartForm(1 << 20); err == nil && r.MultipartForm != nil {
			for k, v := range r.MultipartForm.Value {
				vals[k] = v
			}
		}
		f.lastForm[method] = vals
		resp, ok := f.responses[method]
		if !ok {
			resp = `{"ok":true,"result":{}}`
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, resp)
	}))
	t.Cleanup(f.server.Close)
	return f
}

func (f *fakeTelegram) set(method, response string) { f.responses[method] = response }

// adapter constructs an Adapter backed by the fake server.
func (f *fakeTelegram) adapter(t *testing.T) *Adapter {
	t.Helper()
	api, err := tgbot.New("test-token",
		tgbot.WithServerURL(f.server.URL),
		tgbot.WithSkipGetMe(),
	)
	require.NoError(t, err)
	return New(api)
}

func TestAdapter_SendMessage(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("sendMessage", `{"ok":true,"result":{"message_id":7,"chat":{"id":-100},"text":"hi"}}`)
	a := f.adapter(t)

	kb := telegram.NewKeyboard(
		telegram.NewRow(telegram.NewButton("Btn", "cb"), telegram.NewURLButton("Open", "https://x")),
	)
	msg, err := a.SendMessage(telegram.SendMessageParams{
		ChatID:                -100,
		Text:                  "hi",
		ParseMode:             telegram.ParseModeMarkdown,
		ReplyToMessageID:      3,
		DisableWebPagePreview: true,
		Keyboard:              &kb,
	})
	require.NoError(t, err)
	assert.Equal(t, 7, msg.MessageID)
	assert.Equal(t, int64(-100), msg.Chat.ID)

	form := f.lastForm["sendMessage"]
	assert.Equal(t, "-100", form.Get("chat_id"))
	assert.Equal(t, "hi", form.Get("text"))
	assert.Equal(t, "Markdown", form.Get("parse_mode"))
	assert.Contains(t, form.Get("reply_parameters"), "\"message_id\":3")
	assert.Contains(t, form.Get("reply_markup"), "cb")
	assert.Contains(t, form.Get("reply_markup"), "https://x")
}

func TestAdapter_SendMessage_Error(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("sendMessage", `{"ok":false,"error_code":403,"description":"Forbidden"}`)
	a := f.adapter(t)

	_, err := a.SendMessage(telegram.SendMessageParams{ChatID: -100, Text: "x"})
	require.Error(t, err)
}

func TestAdapter_SendMessage_Topic(t *testing.T) {
	f := newFakeTelegram(t)
	// The response echoes the thread id so the neutral message carries it back.
	f.set("sendMessage", `{"ok":true,"result":{"message_id":7,"chat":{"id":-100},"message_thread_id":50,"text":"hi"}}`)
	a := f.adapter(t)

	msg, err := a.SendMessage(telegram.SendMessageParams{
		ChatID:          -100,
		Text:            "hi",
		MessageThreadID: 50,
	})
	require.NoError(t, err)
	assert.Equal(t, 50, msg.MessageThreadID)

	// The outbound request carries message_thread_id and no reply_parameters.
	form := f.lastForm["sendMessage"]
	assert.Equal(t, "50", form.Get("message_thread_id"))
	assert.Empty(t, form.Get("reply_parameters"))
}

func TestAdapter_EditMessageText(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("editMessageText", `{"ok":true,"result":{"message_id":9,"chat":{"id":-100},"text":"new"}}`)
	a := f.adapter(t)

	kb := telegram.NewKeyboard(telegram.NewRow(telegram.NewButton("B", "c")))
	msg, err := a.EditMessageText(telegram.EditMessageTextParams{
		ChatID:    -100,
		MessageID: 9,
		Text:      "new",
		ParseMode: telegram.ParseModeMarkdown,
		Keyboard:  &kb,
	})
	require.NoError(t, err)
	assert.Equal(t, 9, msg.MessageID)
	assert.Equal(t, "new", f.lastForm["editMessageText"].Get("text"))
}

func TestAdapter_EditMessageReplyMarkup(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("editMessageReplyMarkup", `{"ok":true,"result":{"message_id":9,"chat":{"id":-100}}}`)
	a := f.adapter(t)

	// With a keyboard.
	kb := telegram.NewKeyboard(telegram.NewRow(telegram.NewButton("B", "c")))
	_, err := a.EditMessageReplyMarkup(telegram.EditMessageReplyMarkupParams{ChatID: -100, MessageID: 9, Keyboard: &kb})
	require.NoError(t, err)
	assert.Contains(t, f.lastForm["editMessageReplyMarkup"].Get("reply_markup"), "c")

	// With nil keyboard (clears markup) - must not panic and still sends.
	_, err = a.EditMessageReplyMarkup(telegram.EditMessageReplyMarkupParams{ChatID: -100, MessageID: 9})
	require.NoError(t, err)
}

func TestAdapter_DeleteMessage(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("deleteMessage", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	require.NoError(t, a.DeleteMessage(-100, 5))
	assert.Equal(t, "-100", f.lastForm["deleteMessage"].Get("chat_id"))
	assert.Equal(t, "5", f.lastForm["deleteMessage"].Get("message_id"))
}

func TestAdapter_DeleteMessage_WrapsAPIError(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("deleteMessage", `{"ok":false,"error_code":429,"description":"Too Many Requests","parameters":{"retry_after":5}}`)
	a := f.adapter(t)

	err := a.DeleteMessage(-100, 5)
	require.Error(t, err)
	var apiErr *telegram.APIError
	require.ErrorAs(t, err, &apiErr)
	assert.Equal(t, 429, apiErr.Code)
	assert.Equal(t, 5, apiErr.RetryAfter)
}

func TestAdapter_RestrictChatMember(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("restrictChatMember", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	err := a.RestrictChatMember(telegram.RestrictChatMemberParams{
		ChatID:      -100,
		UserID:      55,
		UntilDate:   123,
		Permissions: telegram.Permissions{CanSendMessages: true},
	})
	require.NoError(t, err)
	form := f.lastForm["restrictChatMember"]
	assert.Equal(t, "-100", form.Get("chat_id"))
	assert.Equal(t, "55", form.Get("user_id"))
	assert.Equal(t, "123", form.Get("until_date"))
}

func TestAdapter_BanUnban(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("banChatMember", `{"ok":true,"result":true}`)
	f.set("unbanChatMember", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	require.NoError(t, a.BanChatMember(-100, 7))
	assert.Equal(t, "7", f.lastForm["banChatMember"].Get("user_id"))

	require.NoError(t, a.UnbanChatMember(-100, 7, true))
	assert.Equal(t, "true", f.lastForm["unbanChatMember"].Get("only_if_banned"))
}

func TestAdapter_AnswerCallback(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("answerCallbackQuery", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	require.NoError(t, a.AnswerCallback("cbid", "done"))
	assert.Equal(t, "cbid", f.lastForm["answerCallbackQuery"].Get("callback_query_id"))
	assert.Equal(t, "done", f.lastForm["answerCallbackQuery"].Get("text"))
}

func TestAdapter_SetMessageReaction(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("setMessageReaction", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	// Non-empty emoji.
	require.NoError(t, a.SetMessageReaction(-100, 5, "👍"))
	assert.Contains(t, f.lastForm["setMessageReaction"].Get("reaction"), "👍")
	assert.Contains(t, f.lastForm["setMessageReaction"].Get("reaction"), "emoji")

	// Empty emoji clears reactions (the reaction field is omitted).
	require.NoError(t, a.SetMessageReaction(-100, 5, ""))
	assert.Empty(t, f.lastForm["setMessageReaction"].Get("reaction"))
}

func TestAdapter_GetChat(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChat", `{"ok":true,"result":{"id":-100,"title":"My Chat","type":"supergroup","username":"mychat"}}`)
	a := f.adapter(t)

	chat, err := a.GetChat(-100)
	require.NoError(t, err)
	assert.Equal(t, int64(-100), chat.ID)
	assert.Equal(t, "My Chat", chat.Title)
	assert.Equal(t, "supergroup", chat.Type)
	assert.Equal(t, "mychat", chat.Username)
}

func TestAdapter_GetChat_Error(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChat", `{"ok":false,"error_code":400,"description":"Bad Request"}`)
	a := f.adapter(t)

	_, err := a.GetChat(-100)
	require.Error(t, err)
}

func TestAdapter_GetChatFull(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChat", `{"ok":true,"result":{"id":42,"type":"private","first_name":"Jane","bio":"hi there","description":"my channel","personal_chat":{"id":-100500,"type":"channel"},"photo":{"big_file_id":"BIGID","small_file_id":"SMALLID"}}}`)
	a := f.adapter(t)

	full, err := a.GetChatFull(42)
	require.NoError(t, err)
	assert.Equal(t, int64(42), full.ID)
	assert.Equal(t, "private", full.Type)
	assert.Equal(t, "Jane", full.FirstName)
	assert.Equal(t, "hi there", full.Bio)
	assert.Equal(t, "my channel", full.Description)
	assert.Equal(t, int64(-100500), full.PersonalChatID)
	assert.Equal(t, "BIGID", full.PhotoBigFileID)
}

func TestAdapter_GetChatFull_NoExtras(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChat", `{"ok":true,"result":{"id":7,"type":"private","first_name":"Bob"}}`)
	a := f.adapter(t)

	full, err := a.GetChatFull(7)
	require.NoError(t, err)
	assert.Equal(t, int64(7), full.ID)
	assert.Equal(t, "Bob", full.FirstName)
	assert.Empty(t, full.Bio)
	assert.Zero(t, full.PersonalChatID)
	assert.Empty(t, full.PhotoBigFileID)
}

func TestAdapter_GetChatByUsername(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChat", `{"ok":true,"result":{"id":-200,"title":"Pub"}}`)
	a := f.adapter(t)

	chat, err := a.GetChatByUsername("pub")
	require.NoError(t, err)
	assert.Equal(t, int64(-200), chat.ID)
	// The @-prefixed username is sent as chat_id.
	assert.Equal(t, "@pub", f.lastForm["getChat"].Get("chat_id"))
}

func TestAdapter_GetChatMember(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChatMember", `{"ok":true,"result":{"status":"administrator","user":{"id":7,"username":"adm"}}}`)
	a := f.adapter(t)

	member, err := a.GetChatMember(-100, 7)
	require.NoError(t, err)
	assert.True(t, member.IsAdmin())
	require.NotNil(t, member.User)
	assert.Equal(t, int64(7), member.User.ID)
}

func TestAdapter_GetChatMember_Error(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getChatMember", `{"ok":false,"error_code":400,"description":"Bad Request"}`)
	a := f.adapter(t)

	_, err := a.GetChatMember(-100, 7)
	require.Error(t, err)
}

func TestAdapter_GetFile(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getFile", `{"ok":true,"result":{"file_id":"f1","file_path":"docs/file.jpg","file_size":1234}}`)
	a := f.adapter(t)

	file, err := a.GetFile("f1")
	require.NoError(t, err)
	assert.Equal(t, "f1", file.FileID)
	assert.Equal(t, "docs/file.jpg", file.FilePath)
	assert.Equal(t, 1234, file.FileSize)
	assert.Contains(t, file.DownloadURL, "test-token")
	assert.Contains(t, file.DownloadURL, "docs/file.jpg")
}

func TestAdapter_GetFile_Error(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getFile", `{"ok":false,"error_code":400,"description":"file not found"}`)
	a := f.adapter(t)

	_, err := a.GetFile("nope")
	require.Error(t, err)
}

func TestAdapter_GetUserProfilePhotos(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getUserProfilePhotos", `{"ok":true,"result":{"total_count":1,"photos":[[{"file_id":"p1","width":100,"height":100,"file_size":99}]]}}`)
	a := f.adapter(t)

	photos, err := a.GetUserProfilePhotos(7, 10)
	require.NoError(t, err)
	assert.Equal(t, 1, photos.TotalCount)
	require.Len(t, photos.Photos, 1)
	require.Len(t, photos.Photos[0], 1)
	assert.Equal(t, "p1", photos.Photos[0][0].FileID)
	assert.Equal(t, 100, photos.Photos[0][0].Width)
}

func TestAdapter_GetUserProfilePhotos_Error(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("getUserProfilePhotos", `{"ok":false,"error_code":400,"description":"bad"}`)
	a := f.adapter(t)

	_, err := a.GetUserProfilePhotos(7, 10)
	require.Error(t, err)
}

func TestAdapter_DeleteWebhook(t *testing.T) {
	f := newFakeTelegram(t)
	f.set("deleteWebhook", `{"ok":true,"result":true}`)
	a := f.adapter(t)

	require.NoError(t, a.DeleteWebhook(true))
	assert.Equal(t, "true", f.lastForm["deleteWebhook"].Get("drop_pending_updates"))
}

// TestAdapter_WrapErr_NonAPIError verifies a transport error is returned
// unchanged (not wrapped in telegram.APIError).
func TestAdapter_WrapErr_NonAPIError(t *testing.T) {
	f := newFakeTelegram(t)
	a := f.adapter(t)
	// Close the server so the next request fails at the transport layer.
	f.server.Close()

	err := a.DeleteMessage(-100, 5)
	require.Error(t, err)
	var apiErr *telegram.APIError
	assert.False(t, errors.As(err, &apiErr), "transport error must not be wrapped as APIError")
}
