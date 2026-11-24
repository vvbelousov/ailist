package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
)

func (b bot) preview(id string, ops []Op) string {
	var s strings.Builder
	for i, o := range ops {
		fmt.Fprintf(&s, "%d. %s\n", i+1, b.describe(o))
	}
	return s.String() + "\n/confirm " + id + "\n/cancel"
}
func (b bot) stale() string {
	return b.tr("Черновик уже изменён или закрыт. Откройте последнее сообщение.", "This draft changed or closed. Open the latest message.")
}

type draftReader interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// A transaction keeps the row locked through edit or confirmation.
func readDraft(ctx context.Context, q draftReader, chat int64, id string) (Batch, error) {
	var raw []byte
	batch := Batch{ID: id}
	err := q.QueryRowContext(ctx, "SELECT ops FROM drafts WHERE chat=$1 AND id=$2 FOR UPDATE", chat, id).Scan(&raw)
	if err == nil {
		err = json.Unmarshal(raw, &batch.Ops)
	}
	return batch, err
}
func (b bot) correct(ctx context.Context, chat, updateID int64, id, row, reply string) (string, error) {
	next := fmt.Sprintf("tg-%d-%d", chat, updateID)
	tx, e := b.db.BeginTx(ctx, nil)
	if e != nil {
		return "", e
	}
	defer tx.Rollback()
	batch, e := readDraft(ctx, tx, chat, id)
	if errors.Is(e, sql.ErrNoRows) {
		// Delivery can be retried after persisting the edit but before sending the new preview.
		prior, err := readDraft(ctx, tx, chat, next)
		if err == nil {
			return b.preview(next, prior.Ops), nil
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return "", err
		}
		return b.stale(), nil
	}
	if e != nil {
		return "", e
	}
	var saved bool
	if e = tx.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM events WHERE id=$1)", id).Scan(&saved); e != nil {
		return "", e
	}
	if saved {
		return b.stale(), nil
	}
	i, e := strconv.Atoi(row)
	if e != nil || i < 1 || i > len(batch.Ops) {
		return b.stale(), nil
	}
	parts := strings.SplitN(reply, "|", 3)
	hint := b.tr("Ответьте на запрос правки в формате: название | количество | единица", "Reply to the edit prompt with: name | quantity | unit")
	if len(parts) != 3 {
		return hint, nil
	}
	n, e := strconv.ParseFloat(strings.ReplaceAll(strings.TrimSpace(parts[1]), ",", "."), 64)
	if e != nil || math.IsNaN(n) || math.IsInf(n, 0) {
		return hint, nil
	}
	op := &batch.Ops[i-1]
	op.Name = parts[0]
	op.Quantity = &n
	op.Unit = parts[2]
	if e = checkOps(batch.Ops); e != nil {
		return hint, nil
	}
	msg := b.preview(next, batch.Ops)
	if len([]rune(msg)) > 3800 {
		return b.tr("Сократите название.", "Use a shorter name."), nil
	}
	raw, e := json.Marshal(batch.Ops)
	if e != nil {
		return "", e
	}
	if _, e = tx.ExecContext(ctx, "UPDATE drafts SET id=$2,ops=$3 WHERE chat=$1", chat, next, string(raw)); e != nil {
		return "", e
	}
	return msg, tx.Commit()
}

type button struct {
	Text string `json:"text"`
	Data string `json:"callback_data"`
}

func (b bot) deliver(ctx context.Context, u update) error {
	if c := u.Callback; c != nil {
		if !b.users[c.From.ID] || c.Message.Chat.ID != c.From.ID || c.Message.Chat.Type != "private" {
			return nil
		}
		// An expired callback acknowledgement must not block subsequent updates.
		_ = b.rpc(ctx, "answerCallbackQuery", map[string]string{"callback_query_id": c.ID}, nil)
		fields := strings.Fields(c.Data)
		if len(fields) == 2 && fields[0] == "confirm" {
			u.Message.Chat.ID = c.From.ID
			u.Message.Text = "/confirm " + fields[1]
			msg, err := b.handle(ctx, u)
			if err != nil {
				msg = b.tr("Не удалось сохранить. Проверьте остатки и повторите.", "Could not save. Check stock and retry.")
			}
			return b.rpc(ctx, "sendMessage", map[string]any{"chat_id": c.From.ID, "text": msg}, nil)
		}
		if len(fields) != 3 || fields[0] != "edit" {
			return nil
		}
		chat := c.From.ID
		batch, e := readDraft(ctx, b.db, chat, fields[1])
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		i, err := strconv.Atoi(fields[2])
		msg := map[string]any{"chat_id": chat, "text": b.stale()}
		if e == nil && err == nil && i >= 1 && i <= len(batch.Ops) {
			o := batch.Ops[i-1]
			msg["text"] = fmt.Sprintf("%s\n%s | %g | %s\n/edit %s %d", b.tr("Ответьте строкой: название | количество | единица", "Reply with: name | quantity | unit"), o.Name, *o.Quantity, o.Unit, batch.ID, i)
			msg["reply_markup"] = map[string]bool{"force_reply": true}
		}
		return b.rpc(ctx, "sendMessage", msg, nil)
	}
	m := u.Message
	if !b.users[m.From.ID] || m.Chat.Type != "private" || m.Chat.ID != m.From.ID {
		return nil
	}
	msg, e := b.handle(ctx, u)
	if e != nil {
		msg = b.tr("Ошибка. Проверьте остаток или доступность сервиса и повторите запрос.", "Error. Check stock or service availability and retry.")
	}
	rr := []rune(msg)
	if len(rr) > 3900 {
		msg = string(rr[:3850]) + b.tr("\n… Полный список в веб-интерфейсе.", "\n… Open the web app for the full list.")
	}
	out := map[string]any{"chat_id": m.Chat.ID, "text": msg}
	id := fmt.Sprintf("tg-%d-%d", m.Chat.ID, u.ID)
	if strings.HasSuffix(msg, "\n/confirm "+id+"\n/cancel") {
		batch, err := readDraft(ctx, b.db, m.Chat.ID, id)
		if err != nil {
			return err
		}
		var keyboard [][]button
		for i := range batch.Ops {
			if i%4 == 0 {
				keyboard = append(keyboard, nil)
			}
			row := len(keyboard) - 1
			keyboard[row] = append(keyboard[row], button{fmt.Sprintf("✎ %d", i+1), fmt.Sprintf("edit %s %d", id, i+1)})
		}
		keyboard = append(keyboard, []button{{b.tr("Подтвердить", "Confirm"), "confirm " + id}})
		out["reply_markup"] = map[string]any{"inline_keyboard": keyboard}
	}
	return b.rpc(ctx, "sendMessage", out, nil)
}
