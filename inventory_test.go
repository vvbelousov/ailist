package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func qty(n float64) *float64 { return &n }
func TestValidation(t *testing.T) {
	for _, o := range []Op{{Action: "delete"}, {Action: "add", Name: "x", Unit: "kg", Quantity: qty(-1)}, {Action: "set", Name: "x", Unit: "kg", Quantity: qty(1), Expiry: "2026-02-31"}, {Action: "move", Name: "x", Unit: "kg", Quantity: qty(1)}} {
		if checkOps([]Op{o}) == nil {
			t.Fatalf("accepted %#v", o)
		}
	}
}
func TestHousehold(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("requires disposable TEST_DATABASE_URL")
	}
	db, e := sql.Open("postgres", url)
	if e != nil {
		t.Fatal(e)
	}
	defer db.Close()
	if _, e = db.Exec(schema); e != nil {
		t.Fatal(e)
	}
	if _, e = db.Exec("TRUNCATE stock,events,drafts,bot_cursor"); e != nil {
		t.Fatal(e)
	}
	a := app{db, "test-household-key"}
	ctx := context.Background()
	srv := httptest.NewServer(a.handler())
	defer srv.Close()
	call := func(method, path, token, body string, want int) []byte {
		t.Helper()
		r, _ := http.NewRequest(method, srv.URL+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w, e := http.DefaultClient.Do(r)
		if e != nil {
			t.Fatal(e)
		}
		defer w.Body.Close()
		var buf bytes.Buffer
		buf.ReadFrom(w.Body)
		if w.StatusCode != want {
			t.Fatalf("%s: %d %s", path, w.StatusCode, buf.String())
		}
		return buf.Bytes()
	}
	call("GET", "/api/items", "bad", "", 401)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/transcribe":
			fmt.Fprint(w, `{"text":"мука 2 кг"}`)
		case "/v1/chat/completions":
			fmt.Fprint(w, `{"choices":[{"message":{"content":"{\"mode\":\"write\",\"ops\":[{\"action\":\"set\",\"name\":\"мука\",\"unit\":\"kg\",\"quantity\":2}]}"}}]}`)
		default:
			t.Errorf("unexpected request %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer mock.Close()
	t.Setenv("LLM_BASE_URL", mock.URL+"/v1")
	t.Setenv("WHISPER_URL", mock.URL+"/transcribe")
	call("POST", "/api/voice", a.token, "mock audio", 200)
	plan := Plan{}
	if e = json.Unmarshal(call("POST", "/api/interpret", a.token, `{"text":"мука 2 кг"}`, 200), &plan); e != nil || len(plan.Ops) != 1 {
		t.Fatal(plan, e)
	}
	b := Batch{"request-001", plan.Ops}
	data, _ := json.Marshal(b)
	for i := 0; i < 2; i++ {
		call("POST", "/api/apply", a.token, string(data), 200)
	}
	op := plan.Ops[0]
	op.Action = "consume"
	op.Quantity = qty(1)
	if apply(ctx, db, Batch{b.ID, []Op{op}}) == nil {
		t.Fatal("accepted different payload with reused id")
	}
	bad := op
	bad.Name = "missing"
	if !errors.Is(apply(ctx, db, Batch{"rollback-001", []Op{op, bad}}), invalid) {
		t.Fatal("rollback expected")
	}
	check := func(mode, q string, n int, quantity float64) {
		t.Helper()
		x, e := items(ctx, db, mode, q)
		if e != nil || len(x) != n || (n > 0 && x[0].Quantity != quantity) {
			t.Fatal(mode, x, e)
		}
	}
	check("list", "мук", 1, 2)
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			err := apply(ctx, db, Batch{fmt.Sprintf("parallel-%d", i), []Op{op}})
			if err != nil && !errors.Is(err, invalid) {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	check("shopping", "", 1, 0)
	op.Action = "add"
	op.Expiry = "2020-01-01"
	op.Quantity = qty(0.5)
	if e = apply(ctx, db, Batch{"restock-001", []Op{op}}); e != nil {
		t.Fatal(e)
	}
	check("shopping", "", 0, 0)
	check("expire", "", 1, 0.5)
	op.Action = "move"
	op.Destination = "shelf"
	if e = apply(ctx, db, Batch{"transfer-001", []Op{op}}); e != nil {
		t.Fatal(e)
	}
	check("expire", "", 1, 0.5)
	call("GET", "/api/history", a.token, "", 200)
	bot := bot{app: a}
	u := update{ID: 81}
	u.Message.Chat.ID = 101
	u.Message.Text = "мука 2 кг"
	msg, e := bot.handle(ctx, u)
	if e != nil || !strings.Contains(msg, "/confirm tg-101-81") {
		t.Fatal(msg, e)
	}
	u.Message.Text = "/confirm tg-101-81"
	for i := 0; i < 2; i++ {
		if _, e = bot.handle(ctx, u); e != nil {
			t.Fatal(e)
		}
	}
}
