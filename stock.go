package main

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	_ "github.com/lib/pq"
	"strings"
	"time"
)

type Op struct {
	Action      string   `json:"action"`
	Name        string   `json:"name"`
	Unit        string   `json:"unit"`
	Quantity    *float64 `json:"quantity,omitempty"`
	Location    string   `json:"location"`
	Expiry      string   `json:"expiry"`
	Category    string   `json:"category"`
	Destination string   `json:"destination,omitempty"`
}
type Item struct {
	Name     string  `json:"name"`
	Unit     string  `json:"unit"`
	Quantity float64 `json:"quantity"`
	Location string  `json:"location"`
	Expiry   string  `json:"expiry"`
	Category string  `json:"category"`
}
type Batch struct {
	ID  string `json:"id"`
	Ops []Op   `json:"ops"`
}

var invalid = errors.New("invalid request")

func checkOps(ops []Op) error {
	if len(ops) == 0 || len(ops) > 50 {
		return invalid
	}
	for i := range ops {
		o := &ops[i]
		o.Name = strings.ToLower(strings.TrimSpace(o.Name))
		o.Unit = strings.ToLower(strings.TrimSpace(o.Unit))
		o.Location = strings.ToLower(strings.TrimSpace(o.Location))
		o.Destination = strings.ToLower(strings.TrimSpace(o.Destination))
		if o.Name == "" || len(o.Name) > 240 || len(o.Unit) > 40 || o.Unit == "" || len(o.Location) > 160 || len(o.Destination) > 160 {
			return invalid
		}
		switch o.Category {
		case "", "food", "medicine", "supplies", "other":
		default:
			return invalid
		}
		if o.Expiry != "" {
			if _, err := time.Parse("2006-01-02", o.Expiry); err != nil {
				return invalid
			}
		}
		switch o.Action {
		case "set", "add", "consume", "move":
		default:
			return invalid
		}
		if o.Quantity == nil || *o.Quantity < 0 || *o.Quantity > 1e9 || (o.Action != "set" && *o.Quantity == 0) {
			return invalid
		}
		if o.Action == "move" && (o.Destination == "" || o.Destination == o.Location) {
			return invalid
		}
	}
	return nil
}

const schema = `
CREATE TABLE IF NOT EXISTS stock(name text NOT NULL, unit text NOT NULL, location text NOT NULL, expiry text NOT NULL, category text NOT NULL, quantity numeric NOT NULL CHECK(quantity>=0), PRIMARY KEY(name,unit,location,expiry));
CREATE TABLE IF NOT EXISTS events(id text PRIMARY KEY, digest bytea NOT NULL, ops jsonb NOT NULL, created timestamptz NOT NULL DEFAULT now());
CREATE TABLE IF NOT EXISTS drafts(chat bigint PRIMARY KEY, id text NOT NULL, ops jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS bot_cursor(singleton boolean PRIMARY KEY DEFAULT TRUE CHECK(singleton), next_id bigint NOT NULL);
`

func apply(ctx context.Context, db *sql.DB, b Batch) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := applyTx(ctx, tx, b); err != nil {
		return err
	}
	return tx.Commit()
}

func applyTx(ctx context.Context, tx *sql.Tx, b Batch) error {
	if len(b.ID) < 8 || len(b.ID) > 128 {
		return invalid
	}
	if err := checkOps(b.Ops); err != nil {
		return err
	}
	data, err := json.Marshal(b.Ops)
	if err != nil {
		return invalid
	}
	hash := sha256.Sum256(data)
	// Serialize household writes, including requests touching the same lots in opposite orders.
	if _, err = tx.ExecContext(ctx, "SELECT pg_advisory_xact_lock(810151)"); err != nil {
		return err
	}
	res, err := tx.ExecContext(ctx, "INSERT INTO events(id,digest,ops) VALUES($1,$2,$3) ON CONFLICT DO NOTHING", b.ID, hash[:], string(data))
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		var same bool
		err = tx.QueryRowContext(ctx, "SELECT digest=$2 FROM events WHERE id=$1", b.ID, hash[:]).Scan(&same)
		if err != nil {
			return err
		}
		if !same {
			return invalid
		}
		return nil
	}
	for _, o := range b.Ops {
		if o.Action == "move" || o.Action == "consume" {
			res, err = tx.ExecContext(ctx, `UPDATE stock SET quantity=quantity-$5 WHERE name=$1 AND unit=$2 AND location=$3 AND expiry=$4 AND quantity >= $5`, o.Name, o.Unit, o.Location, o.Expiry, *o.Quantity)
			if err != nil {
				return err
			}
			n, _ = res.RowsAffected()
			if n != 1 {
				return fmt.Errorf("%w: insufficient stock", invalid)
			}
			if o.Action == "consume" {
				continue
			}
			o.Location = o.Destination
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO stock VALUES($1,$2,$3,$4,COALESCE(NULLIF($5,''),'other'),$6)
 ON CONFLICT(name,unit,location,expiry) DO UPDATE SET quantity=CASE WHEN $7='set' THEN excluded.quantity ELSE stock.quantity+excluded.quantity END,category=COALESCE(NULLIF($5,''),stock.category)`, o.Name, o.Unit, o.Location, o.Expiry, o.Category, *o.Quantity, o.Action)
		if err != nil {
			return err
		}
	}
	return nil
}
func items(ctx context.Context, db *sql.DB, mode, q string) ([]Item, error) {
	if mode == "" {
		mode = "list"
	}
	switch mode {
	case "list", "shopping", "expire":
	default:
		return nil, invalid
	}
	rows, err := db.QueryContext(ctx, `SELECT name,unit,location,expiry,category,quantity FROM stock s WHERE strpos(lower(name),lower($1))>0 AND
 (CASE $2 WHEN 'shopping' THEN quantity=0 AND NOT EXISTS(SELECT 1 FROM stock t WHERE t.name=s.name AND t.unit=s.unit AND t.quantity>0)
 WHEN 'expire' THEN quantity>0 AND NULLIF(expiry,'')::date<=CURRENT_DATE+7 ELSE true END)
 ORDER BY name,expiry,location,unit`, q, mode)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Item{}
	for rows.Next() {
		var x Item
		if err = rows.Scan(&x.Name, &x.Unit, &x.Location, &x.Expiry, &x.Category, &x.Quantity); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	return out, rows.Err()
}
