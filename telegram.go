package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"
)

type bot struct {
	app
	token string
	users map[int64]bool
	en    bool
}
type update struct {
	ID      int64 `json:"update_id"`
	Message struct {
		Chat struct {
			ID   int64
			Type string
		}
		From  struct{ ID int64 }
		Text  string
		Voice struct {
			FileID string `json:"file_id"`
			Size   int64  `json:"file_size"`
		}
	}
}

func (b bot) tr(ru, en string) string {
	if b.en {
		return en
	}
	return ru
}
func (b bot) describe(o Op) string {
	actions := map[string]string{"set": b.tr("Осталось", "Set quantity"), "add": b.tr("Добавить", "Add"), "consume": b.tr("Израсходовать", "Consume"), "move": b.tr("Переместить", "Move")}
	categories := map[string]string{"food": b.tr("Продукты", "Food"), "medicine": b.tr("Аптечка", "Medicine"), "supplies": b.tr("Расходники", "Supplies"), "other": b.tr("Прочее", "Other")}
	parts := []string{actions[o.Action], o.Name, fmt.Sprintf("%g %s", *o.Quantity, o.Unit)}
	for _, field := range []string{o.Location, o.Destination, o.Expiry, categories[o.Category]} {
		if field != "" {
			parts = append(parts, field)
		}
	}
	return strings.Join(parts, " · ")
}

func (b bot) rpc(ctx context.Context, method string, args any, out any) error {
	data, _ := json.Marshal(args)
	var wrap struct {
		OK     bool
		Result json.RawMessage
	}
	e := remote(ctx, "https://api.telegram.org/bot"+b.token+"/"+method, "application/json", "", bytes.NewReader(data), &wrap)
	if e != nil || !wrap.OK {
		return upstream
	}
	if out != nil {
		return json.Unmarshal(wrap.Result, out)
	}
	return nil
}
func (a app) poll() {
	b := bot{a, os.Getenv("TELEGRAM_TOKEN"), map[int64]bool{}, os.Getenv("BOT_LANG") == "en"}
	for _, s := range strings.Split(os.Getenv("TELEGRAM_USERS"), ",") {
		n, e := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
		if e == nil && n > 0 {
			b.users[n] = true
		}
	}
	if len(b.users) == 0 {
		log.Print("Telegram disabled: empty allowlist")
		return
	}
	ctx := context.Background()
	for {
		var offset int64
		e := a.db.QueryRow("SELECT COALESCE((SELECT next_id FROM bot_cursor),0)").Scan(&offset)
		var updates []update
		if e == nil {
			e = b.rpc(ctx, "getUpdates", map[string]any{"offset": offset, "timeout": 25, "allowed_updates": []string{"message"}}, &updates)
		}
		for _, u := range updates {
			m := u.Message
			if b.users[m.From.ID] && m.Chat.Type == "private" && m.Chat.ID == m.From.ID {
				msg, err := b.handle(ctx, u)
				if err != nil {
					msg = b.tr("Ошибка. Проверьте остаток или доступность сервиса и повторите запрос.", "Error. Check stock or service availability and retry.")
				}
				rr := []rune(msg)
				if len(rr) > 3900 {
					msg = string(rr[:3850]) + b.tr("\n… Полный список в веб-интерфейсе.", "\n… Open the web app for the full list.")
				}
				e = b.rpc(ctx, "sendMessage", map[string]any{"chat_id": m.Chat.ID, "text": msg}, nil)
				if e != nil {
					break
				}
			}
			_, e = a.db.Exec("INSERT INTO bot_cursor(next_id) VALUES($1) ON CONFLICT(singleton) DO UPDATE SET next_id=excluded.next_id", u.ID+1)
			if e != nil {
				break
			}
		}
		if e != nil {
			log.Print("Telegram polling paused after an error")
			time.Sleep(5 * time.Second)
		}
	}
}
func (b bot) handle(ctx context.Context, u update) (string, error) {
	m := u.Message
	t := strings.TrimSpace(m.Text)
	chat := m.Chat.ID
	if m.Voice.FileID != "" {
		if m.Voice.Size > maxAudio {
			return "", invalid
		}
		var f struct {
			Path string `json:"file_path"`
		}
		if b.rpc(ctx, "getFile", map[string]string{"file_id": m.Voice.FileID}, &f) != nil || path.Clean(f.Path) != f.Path || !strings.HasPrefix(f.Path, "voice/") {
			return "", upstream
		}
		req, e := http.NewRequestWithContext(ctx, "GET", "https://api.telegram.org/file/bot"+b.token+"/"+f.Path, nil)
		if e != nil {
			return "", upstream
		}
		resp, e := client.Do(req)
		if e != nil {
			return "", upstream
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return "", upstream
		}
		t, e = transcribe(ctx, io.LimitReader(resp.Body, maxAudio+1))
		if e != nil {
			return "", e
		}
	}
	if strings.HasPrefix(t, "/confirm ") {
		id := strings.TrimSpace(strings.TrimPrefix(t, "/confirm "))
		var raw []byte
		e := b.db.QueryRowContext(ctx, "SELECT ops FROM drafts WHERE chat=$1 AND id=$2", chat, id).Scan(&raw)
		if e != nil {
			return "", e
		}
		batch := Batch{ID: id}
		if json.Unmarshal(raw, &batch.Ops) != nil {
			return "", errors.New("draft")
		}
		if e = apply(ctx, b.db, batch); e != nil {
			return "", e
		}
		return b.tr("Сохранено.", "Saved."), nil
	}
	var p Plan
	switch t {
	case "/list":
		p.Mode = "list"
	case "/shopping":
		p.Mode = "shopping"
	case "/expire":
		p.Mode = "expire"
	case "/cancel":
		_, e := b.db.ExecContext(ctx, "DELETE FROM drafts WHERE chat=$1", chat)
		return b.tr("Черновик удалён.", "Draft discarded."), e
	case "/start", "/help", "":
		return b.tr("Пришлите текст или голосовое. /list — остатки, /shopping — покупки, /expire — сроки, /cancel — отмена.", "Send text or voice. /list inventory, /shopping shopping list, /expire expiring lots, /cancel discard draft."), nil
	default:
		var e error
		p, e = b.interpret(ctx, t)
		if e != nil {
			return "", e
		}
	}
	if p.Mode == "clarify" {
		return p.Question, nil
	}
	if p.Mode == "write" {
		id := fmt.Sprintf("tg-%d-%d", chat, u.ID)
		var s strings.Builder
		for _, o := range p.Ops {
			fmt.Fprintln(&s, b.describe(o))
		}
		msg := s.String() + "\n/confirm " + id + "\n/cancel"
		if len([]rune(msg)) > 3800 {
			return b.tr("Разбейте команду на несколько сообщений.", "Split this command into shorter messages."), nil
		}
		raw, _ := json.Marshal(p.Ops)
		_, e := b.db.ExecContext(ctx, "INSERT INTO drafts VALUES($1,$2,$3) ON CONFLICT(chat) DO UPDATE SET id=excluded.id,ops=excluded.ops", chat, id, string(raw))
		return msg, e
	}
	rows, e := items(ctx, b.db, p.Mode, p.Query)
	if e != nil {
		return "", e
	}
	if len(rows) == 0 {
		return b.tr("Список пуст.", "No matching items."), nil
	}
	var s strings.Builder
	for _, x := range rows {
		fmt.Fprintf(&s, "%s · %g %s · %s %s\n", x.Name, x.Quantity, x.Unit, x.Location, x.Expiry)
	}
	return s.String(), nil
}
