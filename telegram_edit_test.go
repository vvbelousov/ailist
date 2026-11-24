package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
)

type telegramTransport func(*http.Request) (*http.Response, error)

func (f telegramTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestTelegramDraftCorrection(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("needs disposable PostgreSQL")
	}
	db, err := sql.Open("postgres", url)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err = db.Exec(schema); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec("TRUNCATE stock,events,drafts"); err != nil {
		t.Fatal(err)
	}
	b := bot{app: app{db, "test"}, token: "42:test", users: map[int64]bool{101: true}, en: true}
	ops := []Op{}
	for _, name := range []string{"rice", "milk", "tomato", "soap", "potato"} {
		ops = append(ops, Op{Action: "add", Name: name, Unit: "pcs", Quantity: qty(1), Location: "pantry", Category: "food"})
	}
	ctx := context.Background()
	var sent struct {
		Text   string
		Markup struct {
			Keys  [][]button `json:"inline_keyboard"`
			Force bool       `json:"force_reply"`
		} `json:"reply_markup"`
	}
	count := 0
	original := client.Transport
	defer func() { client.Transport = original }()
	client.Transport = telegramTransport(func(r *http.Request) (*http.Response, error) {
		result := `{"ok":true,"result":true}`
		switch r.URL.Path {
		case "/v1/chat/completions":
			plan, _ := json.Marshal(Plan{Mode: "write", Ops: ops})
			result = fmt.Sprintf(`{"choices":[{"message":{"content":%q}}]}`, plan)
		case "/bot42:test/sendMessage":
			sent.Markup.Keys = nil
			sent.Markup.Force = false
			if err := json.NewDecoder(r.Body).Decode(&sent); err != nil {
				t.Fatal(err)
			}
			count++
		case "/bot42:test/answerCallbackQuery":
		default:
			t.Fatalf("unexpected URL %s", r.URL.Path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(result)), Header: make(http.Header)}, nil
	})
	t.Setenv("LLM_BASE_URL", "http://localhost/v1")
	send := func(data string) {
		t.Helper()
		var u update
		if e := json.Unmarshal([]byte(data), &u); e != nil {
			t.Fatal(e)
		}
		if e := b.deliver(ctx, u); e != nil {
			t.Fatal(e)
		}
	}
	click := func(data string, user int) {
		send(fmt.Sprintf(`{"callback_query":{"id":"cb","from":{"id":%d},"message":{"chat":{"id":101,"type":"private"}},"data":%q}}`, user, data))
	}
	send(`{"update_id":11,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"text":"stock report"}}`)
	chosen := sent.Markup.Keys[0][2].Data
	oldConfirm := sent.Markup.Keys[2][0].Data
	click(chosen, 102)
	if count != 1 {
		t.Fatal("unauthorized callback")
	}
	click(chosen, 101)
	if !sent.Markup.Force {
		t.Fatal("expected reply prompt")
	}
	prompt := sent.Text
	reply := func(id int, value string) {
		send(fmt.Sprintf(`{"update_id":%d,"message":{"from":{"id":101},"chat":{"id":101,"type":"private"},"text":%q,"reply_to_message":{"text":%q,"from":{"id":42}}}}`, id, value, prompt))
	}
	reply(12, "invalid")
	if !strings.Contains(sent.Text, "Reply") {
		t.Fatal(sent.Text)
	}
	reply(13, "tomatoes | 2 | pcs")
	if !strings.Contains(sent.Text, "3. Add · tomatoes · 2 pcs") {
		t.Fatal(sent.Text)
	}
	newConfirm := sent.Markup.Keys[2][0].Data
	reply(13, "tomatoes | 2 | pcs")
	if sent.Markup.Keys[2][0].Data != newConfirm {
		t.Fatal("edit replay changed revision")
	}
	click(oldConfirm, 101)
	if sent.Text != b.stale() {
		t.Fatal("old preview accepted")
	}
	rows, e := items(ctx, db, "list", "")
	if e != nil || len(rows) != 0 {
		t.Fatal(rows, e)
	}
	click(newConfirm, 101)
	click(newConfirm, 101)
	reply(14, "mistake | 4 | pcs")
	if sent.Text != b.stale() {
		t.Fatal("old edit accepted")
	}
	rows, e = items(ctx, db, "list", "")
	if e != nil || len(rows) != 5 {
		t.Fatal(rows, e)
	}
	for _, r := range rows {
		want := 1.
		if r.Name == "tomatoes" {
			want = 2
		}
		if r.Name == "tomato" || r.Quantity != want || r.Location != "pantry" {
			t.Fatal(r)
		}
	}
}
