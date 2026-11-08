package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"kan-db/internal/auth"
	"kan-db/internal/store"
)

type API struct {
	store       *store.Store
	tokens      *auth.Tokens
	allowLegacy bool
	dummyHash   string
	hashSlots   chan struct{}
}
type endpoint func(http.ResponseWriter, *http.Request, store.User)

func New(s *store.Store, secret string, allowLegacy bool) (http.Handler, error) {
	dummy, err := auth.HashPassword("dummy-password-for-missing-accounts")
	if err != nil {
		return nil, err
	}
	a := &API{store: s, tokens: auth.NewTokens(secret), allowLegacy: allowLegacy, dummyHash: dummy, hashSlots: make(chan struct{}, 4)}
	mux := http.NewServeMux()
	add := func(pattern string, level int32, h endpoint) { mux.Handle(pattern, a.protected(level, h)) }
	mux.HandleFunc("POST /api/v1/login/token", a.login)
	add("GET /api/v1/user/me", 0, a.me)
	add("GET /api/v1/user/get/{stud_id}", 2, a.user)
	add("POST /api/v1/user/create/{name}/{sname}/{stud_id}/{room}", 4, a.createLegacyUser)
	add("POST /api/v1/user/create", 4, a.createUser)
	add("POST /api/v1/user/switch/active/{stud_id}/{activate}", 4, a.setActive)
	add("POST /api/v1/user/switch/room/{stud_id}/{room_name}", 4, a.setRoom)
	add("GET /api/v1/kpd/my/all", 0, a.myEvents)
	add("GET /api/v1/kpd/get/all/{user_id}", 4, a.events)
	add("POST /api/v1/kpd/set", 2, a.addKPD)
	add("GET /api/v1/sanitary/room/get/{room_name}", 2, a.room)
	add("POST /api/v1/sanitary/mark/set", 2, a.addMark)
	for _, prefix := range []string{"/api/v1/sanitary/mark", "/api/v1/sanitarymark"} {
		add("GET "+prefix+"/get/{stud_id}", 2, a.marks)
		add("GET "+prefix+"/my", 0, a.myMarks)
		add("GET "+prefix+"/roomusers/{room_id}", 2, a.roomUsers)
	}
	add("POST /api/v1/notification/create", 4, a.createNotification)
	add("GET /api/v1/notification/all_not_notified", 4, a.pendingNotifications)
	add("GET /api/v1/notification/by_event_release_timestamp/start/{start}/end/{end}", 4, a.notificationsByDate)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"status": "ok"}) })
	mux.HandleFunc("GET /readyz", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.Pool.Ping(ctx); err != nil {
			fail(w, 503, "Database unavailable")
			return
		}
		writeJSON(w, 200, map[string]string{"status": "ok"})
	})
	mux.HandleFunc("GET /openapi.json", serveOpenAPI)
	mux.HandleFunc("GET /docs", serveDocs)
	registry := prometheus.NewRegistry()
	registry.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))
	requests := prometheus.NewCounterVec(prometheus.CounterOpts{Name: "http_requests_total", Help: "HTTP requests by route, method and status class."}, []string{"handler", "method", "status"})
	duration := prometheus.NewHistogramVec(prometheus.HistogramOpts{Name: "http_request_duration_seconds", Help: "HTTP request duration.", Buckets: prometheus.DefBuckets}, []string{"handler", "method"})
	registry.MustRegister(requests, duration)
	mux.Handle("GET /metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	// Use pattern labels, never raw URLs (which can contain IDs or credentials).
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &responseWriter{ResponseWriter: w, status: 200}
		ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
		defer cancel()
		r = r.WithContext(ctx)
		r.Body = http.MaxBytesReader(rec, r.Body, 1<<20)
		defer func() {
			if recover() != nil {
				slog.Error("HTTP handler panic", "method", r.Method)
				if !rec.written {
					fail(rec, 500, "Internal server error")
				}
			}
			pattern := r.Pattern
			if pattern == "" {
				pattern = "unmatched"
			}
			requests.WithLabelValues(pattern, r.Method, fmt.Sprintf("%dxx", rec.status/100)).Inc()
			duration.WithLabelValues(pattern, r.Method).Observe(time.Since(start).Seconds())
		}()
		if strings.HasSuffix(r.URL.Path, "/") && r.URL.Path != "/" {
			u := *r.URL
			u.Path = strings.TrimSuffix(u.Path, "/")
			u.RawPath = ""
			clone := r.Clone(r.Context())
			clone.URL = &u
			if _, pattern := mux.Handler(clone); pattern != "" {
				http.Redirect(rec, r, u.String(), http.StatusTemporaryRedirect)
				return
			}
		}
		mux.ServeHTTP(rec, r)
	}), nil
}

func (a *API) protected(level int32, next endpoint) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Fields(r.Header.Get("Authorization"))
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			unauthorized(w, "Not authenticated")
			return
		}
		login, err := a.tokens.Verify(parts[1])
		if err != nil {
			unauthorized(w, "Could not validate credentials")
			return
		}
		u, err := a.store.UserByLogin(r.Context(), login)
		if errors.Is(err, pgx.ErrNoRows) {
			unauthorized(w, "Could not validate credentials")
			return
		}
		if err != nil {
			dbError(w, err)
			return
		}
		if !u.Active {
			fail(w, 400, "Inactive user")
			return
		}
		if u.AccessLevel < level {
			fail(w, 403, "доступ к выполнению запрещен")
			return
		}
		next(w, r, u)
	})
}
func (a *API) hashSlot(w http.ResponseWriter) bool {
	select {
	case a.hashSlots <- struct{}{}:
		return true
	default:
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "Too many password operations; retry shortly")
		return false
	}
}
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
func fail(w http.ResponseWriter, status int, detail string) {
	writeJSON(w, status, map[string]string{"detail": detail})
}
func unauthorized(w http.ResponseWriter, detail string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	fail(w, 401, detail)
}
func dbError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrRoomNotFound):
		fail(w, 400, "Комната не найдена")
	case errors.Is(err, pgx.ErrNoRows):
		fail(w, 404, "Пользователь не найден")
	case errors.Is(err, context.DeadlineExceeded):
		fail(w, 504, "Database request timed out")
	default:
		var pg *pgconn.PgError
		if errors.As(err, &pg) {
			switch pg.Code {
			case "23505":
				fail(w, 409, "Запись с таким идентификатором или логином уже существует")
				return
			case "23503":
				fail(w, 400, "Пользователь, роль или тип события не найден")
				return
			case "22001", "22003", "23502":
				fail(w, 422, "Значение не соответствует ограничениям базы данных")
				return
			}
			slog.Error("database operation failed", "code", pg.Code)
		} else {
			slog.Error("database operation failed", "error_type", fmt.Sprintf("%T", err))
		}
		fail(w, 500, "Internal server error")
	}
}
func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	d := json.NewDecoder(r.Body)
	if err := d.Decode(v); err != nil {
		var size *http.MaxBytesError
		if errors.As(err, &size) {
			fail(w, 413, "Request body too large")
		} else {
			fail(w, 422, "Invalid JSON request body")
		}
		return false
	}
	if err := d.Decode(new(any)); err != io.EOF {
		fail(w, 422, "Expected exactly one JSON object")
		return false
	}
	return true
}
func idParam(w http.ResponseWriter, r *http.Request, key string) (int32, bool) {
	v, err := strconv.ParseInt(r.PathValue(key), 10, 32)
	if err != nil || v <= 0 {
		fail(w, 422, key+" must be a positive 32-bit integer")
		return 0, false
	}
	return int32(v), true
}
func validString(v string, max int) bool {
	return utf8.ValidString(v) && !strings.ContainsRune(v, 0) && utf8.RuneCountInString(v) <= max
}

type responseWriter struct {
	http.ResponseWriter
	status  int
	written bool
}

func (w *responseWriter) WriteHeader(status int) {
	if !w.written {
		w.status = status
		w.written = true
		w.ResponseWriter.WriteHeader(status)
	}
}
func (w *responseWriter) Write(b []byte) (int, error) {
	if !w.written {
		w.WriteHeader(200)
	}
	return w.ResponseWriter.Write(b)
}
func (w *responseWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }
