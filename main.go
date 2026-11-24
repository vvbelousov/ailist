package main

import (
	"context"
	"crypto/subtle"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"
)

type app struct {
	db    *sql.DB
	token string
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}
func decode(r io.Reader, v any) error {
	d := json.NewDecoder(r)
	d.DisallowUnknownFields()
	if d.Decode(v) != nil || d.Decode(new(any)) != io.EOF {
		return invalid
	}
	return nil
}
func reply(w http.ResponseWriter, v any, err error) {
	w.Header().Set("Content-Type", "application/json")
	if err != nil {
		code := 500
		msg := "internal error"
		switch {
		case errors.Is(err, invalid):
			code = 422
			msg = err.Error()
		case errors.Is(err, upstream):
			code = 503
			msg = "model service unavailable or invalid response"
		}
		w.WriteHeader(code)
		v = map[string]string{"error": msg}
	}
	json.NewEncoder(w).Encode(v)
}
func (a app) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, r *http.Request) {
		ctx, c := context.WithTimeout(r.Context(), 2*time.Second)
		defer c()
		reply(w, true, a.db.PingContext(ctx))
	})
	api := http.NewServeMux()
	api.HandleFunc("GET /api/items", func(w http.ResponseWriter, r *http.Request) {
		v, e := items(r.Context(), a.db, r.URL.Query().Get("mode"), r.URL.Query().Get("q"))
		reply(w, v, e)
	})
	api.HandleFunc("POST /api/apply", func(w http.ResponseWriter, r *http.Request) {
		var b Batch
		e := decode(http.MaxBytesReader(w, r.Body, 65536), &b)
		if e == nil {
			e = apply(r.Context(), a.db, b)
		}
		reply(w, true, e)
	})
	api.HandleFunc("POST /api/interpret", func(w http.ResponseWriter, r *http.Request) {
		var b struct {
			Text string `json:"text"`
		}
		if decode(http.MaxBytesReader(w, r.Body, 16384), &b) != nil {
			reply(w, nil, invalid)
			return
		}
		p, e := a.interpret(r.Context(), b.Text)
		reply(w, p, e)
	})
	api.HandleFunc("POST /api/voice", func(w http.ResponseWriter, r *http.Request) {
		v, e := transcribe(r.Context(), http.MaxBytesReader(w, r.Body, maxAudio))
		reply(w, map[string]string{"text": v}, e)
	})
	api.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		rows, e := a.db.QueryContext(r.Context(), "SELECT created,ops FROM events ORDER BY created DESC,id LIMIT 100")
		if e != nil {
			reply(w, nil, e)
			return
		}
		defer rows.Close()
		type entry struct {
			At  time.Time       `json:"at"`
			Ops json.RawMessage `json:"ops"`
		}
		v := []entry{}
		for rows.Next() {
			var x entry
			if e = rows.Scan(&x.At, &x.Ops); e != nil {
				break
			}
			v = append(v, x)
		}
		reply(w, v, errors.Join(e, rows.Err()))
	})
	mux.Handle("/api/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
		if !ok || subtle.ConstantTimeCompare([]byte(h), []byte(a.token)) != 1 {
			http.Error(w, "unauthorized", 401)
			return
		}
		w.Header().Set("Cache-Control", "no-store")
		api.ServeHTTP(w, r)
	}))
	mux.Handle("/", http.FileServer(http.Dir(env("WEB_ROOT", "web/dist"))))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; frame-ancestors 'none'; base-uri 'none'")
		mux.ServeHTTP(w, r)
	})
}
func main() {
	token := os.Getenv("APP_TOKEN")
	if len(token) < 24 {
		log.Fatal("APP_TOKEN needs at least 24 characters")
	}
	db, e := sql.Open("postgres", os.Getenv("DATABASE_URL"))
	if e != nil {
		log.Fatal("invalid database configuration")
	}
	defer db.Close()
	db.SetMaxOpenConns(8)
	if _, e = db.Exec(schema); e != nil {
		log.Fatal("database initialization failed: ", e)
	}
	a := app{db, token}
	if os.Getenv("TELEGRAM_TOKEN") != "" {
		go a.poll()
	}
	s := http.Server{Addr: env("LISTEN", ":8080"), Handler: a.handler(), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 45 * time.Second, WriteTimeout: 3 * time.Minute, IdleTimeout: 60 * time.Second}
	log.Fatal(s.ListenAndServe())
}
