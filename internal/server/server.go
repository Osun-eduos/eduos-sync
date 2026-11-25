// Package server exposes the sync endpoints used by the Android and iOS apps.
package server

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Osun-eduos/eduos-sync/internal/apiclient"
	"github.com/Osun-eduos/eduos-sync/internal/auth"
	"github.com/Osun-eduos/eduos-sync/internal/store"
)

// Store is the part of the database the server uses (an interface so tests can supply a fake).
type Store interface {
	Ping(ctx context.Context) error
	FindEvent(ctx context.Context, id string) (*store.Event, error)
	SaveEvent(ctx context.Context, e store.Event) error
	Lock(ctx context.Context, id string) (func(), error)
	SheetID(ctx context.Context, classSubjectID string) (string, error)
	Snapshot(ctx context.Context, userID string, registerDays int, today time.Time, subjectName func(string) string) (*store.Snapshot, error)
}

// API applies changes through eduos-api with the teacher's own token.
type API interface {
	Do(ctx context.Context, method, path, token, clientIP string, body any) (apiclient.Response, error)
}

type References interface {
	Get(ctx context.Context, token string) (*apiclient.Reference, error)
}

type Server struct {
	Store        Store
	API          API
	Refs         References
	Verifier     *auth.Verifier
	RegisterDays int
	MaxEvents    int
	MaxBodyBytes int64
	Now          func() time.Time
	Log          *slog.Logger
}

func (s *Server) Routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.health)
	mux.HandleFunc("GET /v1/snapshot", s.authed(s.snapshot))
	mux.HandleFunc("POST /v1/sync", s.authed(s.sync))
	return s.recover(mux)
}

type ctxKey struct{}
type caller struct {
	user  auth.User
	token string
	ip    string
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"statusCode": status, "message": msg})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) authed(next func(http.ResponseWriter, *http.Request, caller)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		h := r.Header.Get("Authorization")
		if !strings.HasPrefix(h, "Bearer ") {
			fail(w, http.StatusUnauthorized, "Please sign in.")
			return
		}
		u, err := s.Verifier.Verify(strings.TrimPrefix(h, "Bearer "))
		if err != nil {
			fail(w, http.StatusUnauthorized, "Your session has expired. Please sign in again.")
			return
		}
		if u.MustChangePassword {
			writeJSON(w, http.StatusForbidden, map[string]any{"statusCode": 403, "message": "Change your temporary password to continue.", "code": "PASSWORD_CHANGE_REQUIRED"})
			return
		}
		if u.Role != "TEACHER" {
			fail(w, http.StatusForbidden, "The mobile app is for teachers.")
			return
		}
		next(w, r, caller{user: u, token: strings.TrimPrefix(h, "Bearer "), ip: clientIP(r)})
	}
}

func (s *Server) recover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if v := recover(); v != nil {
				s.Log.Error("panic", "path", r.URL.Path, "error", v)
				fail(w, http.StatusInternalServerError, "Something went wrong on the server. Try again shortly.")
			}
		}()
		next.ServeHTTP(w, r)
	})
}

func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if err := s.Store.Ping(ctx); err != nil {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{"status": "error", "database": "unreachable"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

// Today in Nigeria (West Africa Time, UTC+1).
func (s *Server) today() time.Time {
	t := s.Now().UTC().Add(time.Hour)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, time.UTC)
}

func (s *Server) snapshot(w http.ResponseWriter, r *http.Request, c caller) {
	refs, err := s.Refs.Get(r.Context(), c.token)
	if err != nil {
		s.Log.Error("reference data", "error", err)
		fail(w, http.StatusBadGateway, "Could not reach the EduOS service. Try again shortly.")
		return
	}
	snap, err := s.Store.Snapshot(r.Context(), c.user.ID, s.RegisterDays, s.today(), func(code string) string {
		if n, ok := refs.Subjects[code]; ok {
			return n
		}
		return code
	})
	if errors.Is(err, store.ErrInactive) {
		fail(w, http.StatusUnauthorized, "This account is no longer active.")
		return
	}
	if err != nil {
		s.Log.Error("snapshot", "user", c.user.ID, "error", err)
		fail(w, http.StatusInternalServerError, "Could not load your classes. Try again shortly.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"snapshot": snap, "grading": refs.Grading, "attendance": refs.Attendance, "today": s.today().Format("2006-01-02"),
	})
}

// Event is one change from a phone. ID is generated on the phone and is the same on every retry.
type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurredAt"`
	Payload    json.RawMessage `json:"payload"`
}

// Result tells the phone what happened to each event.
//   - applied: done (or already done earlier, with duplicate set); remove it from the queue.
//   - rejected: eduos-api refused it for a reason the teacher must see (message); remove it from the queue.
//   - retry: the service could not reach eduos-api; keep it and every later event, and try again later.
type Result struct {
	ID         string          `json:"id"`
	Status     string          `json:"status"`
	Duplicate  bool            `json:"duplicate,omitempty"`
	HTTPStatus int             `json:"httpStatus,omitempty"`
	Message    string          `json:"message,omitempty"`
	Code       string          `json:"code,omitempty"`
	Detail     json.RawMessage `json:"detail,omitempty"`
}

var uuidRe = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
var deviceRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{8,64}$`)

func (s *Server) sync(w http.ResponseWriter, r *http.Request, c caller) {
	device := r.Header.Get("X-Device-Id")
	if !deviceRe.MatchString(device) {
		fail(w, http.StatusBadRequest, "Send the X-Device-Id header (8 to 64 letters, digits or . _ : -).")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, s.MaxBodyBytes)
	var body struct {
		Events []Event `json:"events"`
	}
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil {
		fail(w, http.StatusBadRequest, "The request body must be JSON: { \"events\": [...] }.")
		return
	}
	if len(body.Events) == 0 {
		fail(w, http.StatusBadRequest, "Send at least one event.")
		return
	}
	if len(body.Events) > s.MaxEvents {
		fail(w, http.StatusRequestEntityTooLarge, "Too many events in one upload. Send them in smaller batches.")
		return
	}
	seen := map[string]bool{}
	for _, e := range body.Events {
		if !uuidRe.MatchString(e.ID) {
			fail(w, http.StatusBadRequest, "Every event needs a UUID id.")
			return
		}
		if seen[strings.ToLower(e.ID)] {
			fail(w, http.StatusBadRequest, "The same event id appears twice in one upload.")
			return
		}
		seen[strings.ToLower(e.ID)] = true
	}

	results := make([]Result, 0, len(body.Events))
	stopped := false
	for _, e := range body.Events {
		if stopped {
			results = append(results, Result{ID: e.ID, Status: "retry", Message: "Not sent yet."})
			continue
		}
		res, abort := s.apply(r.Context(), c, device, e)
		if abort != 0 {
			fail(w, abort, res.Message)
			return
		}
		results = append(results, res)
		if res.Status == "retry" {
			stopped = true // keep the queue in order
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": results})
}

// apply handles one event. A non-zero abort status means the whole upload must stop (for example an expired token).
func (s *Server) apply(ctx context.Context, c caller, device string, e Event) (Result, int) {
	id := strings.ToLower(e.ID)
	unlock, err := s.Store.Lock(ctx, id)
	if err != nil {
		s.Log.Error("lock", "event", id, "error", err)
		return Result{ID: e.ID, Status: "retry", Message: "The server is busy. It will try again."}, 0
	}
	defer unlock()

	if prev, err := s.Store.FindEvent(ctx, id); err == nil {
		if prev.UserID != c.user.ID {
			return Result{ID: e.ID, Status: "rejected", HTTPStatus: http.StatusConflict, Message: "This change id has already been used."}, 0
		}
		r := fromRecord(e.ID, prev)
		r.Duplicate = true
		return r, 0
	} else if !errors.Is(err, store.ErrNotFound) {
		s.Log.Error("find event", "event", id, "error", err)
		return Result{ID: e.ID, Status: "retry", Message: "The server is busy. It will try again."}, 0
	}

	method, path, payload, problem := s.route(ctx, e)
	var resp apiclient.Response
	if problem != nil {
		resp = *problem
	} else {
		resp, err = s.API.Do(ctx, method, path, c.token, c.ip, payload)
		if err != nil {
			s.Log.Warn("eduos-api unreachable", "event", id, "error", err)
			return Result{ID: e.ID, Status: "retry", Message: "Could not reach the EduOS service. It will try again."}, 0
		}
	}
	switch {
	case resp.Status == http.StatusUnauthorized:
		return Result{Message: "Your session has expired. Please sign in again."}, http.StatusUnauthorized
	case resp.Status == http.StatusTooManyRequests || resp.Status >= 500:
		return Result{ID: e.ID, Status: "retry", HTTPStatus: resp.Status, Message: "The EduOS service is busy. It will try again."}, 0
	}

	rec := store.Event{ID: id, UserID: c.user.ID, DeviceID: device, Type: e.Type, OccurredAt: e.OccurredAt, HTTPStatus: resp.Status}
	if rec.OccurredAt.IsZero() {
		rec.OccurredAt = s.Now()
	}
	if resp.Status >= 200 && resp.Status < 300 {
		rec.Status, rec.Result = "APPLIED", json.RawMessage(`{"ok":true}`)
	} else {
		rec.Status, rec.Result = "REJECTED", errorBody(resp.Body)
	}
	if err := s.Store.SaveEvent(ctx, rec); err != nil {
		// The change went through but was not recorded; a retry would be refused or repeat harmlessly.
		s.Log.Error("save event", "event", id, "error", err)
	}
	return fromRecord(e.ID, &rec), 0
}

// route maps an event to the eduos-api call that applies it. problem is set when the event itself is invalid.
func (s *Server) route(ctx context.Context, e Event) (method, path string, payload any, problem *apiclient.Response) {
	bad := func(msg string) (string, string, any, *apiclient.Response) {
		b, _ := json.Marshal(map[string]any{"statusCode": 400, "message": msg})
		return "", "", nil, &apiclient.Response{Status: http.StatusBadRequest, Body: b}
	}
	var obj map[string]json.RawMessage
	if err := json.Unmarshal(e.Payload, &obj); err != nil {
		return bad("The change could not be read.")
	}
	switch e.Type {
	case "attendance.register":
		return http.MethodPut, "/attendance/register", e.Payload, nil
	case "scores.save":
		return http.MethodPut, "/scoresheets", e.Payload, nil
	case "scores.submit":
		var p struct {
			ClassSubjectID string `json:"classSubjectId"`
		}
		_ = json.Unmarshal(e.Payload, &p)
		if !uuidRe.MatchString(p.ClassSubjectID) {
			return bad("The change does not say which subject to submit.")
		}
		sheet, err := s.Store.SheetID(ctx, p.ClassSubjectID)
		if errors.Is(err, store.ErrNotFound) {
			return bad("No scores have been entered for this subject this term.")
		}
		if err != nil {
			return http.MethodPost, "", nil, &apiclient.Response{Status: http.StatusServiceUnavailable}
		}
		return http.MethodPost, "/scoresheets/" + sheet + "/submit", nil, nil
	}
	return bad("This app version sent a change the server does not understand. Update the app.")
}

func errorBody(b []byte) json.RawMessage {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return json.RawMessage(`{"message":"Refused by the EduOS service."}`)
	}
	out := map[string]any{"message": m["message"]}
	if code, ok := m["code"]; ok {
		out["code"] = code
	}
	if list, ok := out["message"].([]any); ok && len(list) > 0 {
		out["message"] = list[0]
	}
	j, _ := json.Marshal(out)
	return j
}

func fromRecord(id string, e *store.Event) Result {
	if e.Status == "APPLIED" {
		return Result{ID: id, Status: "applied", HTTPStatus: e.HTTPStatus}
	}
	var m struct {
		Message string `json:"message"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(e.Result, &m)
	return Result{ID: id, Status: "rejected", HTTPStatus: e.HTTPStatus, Message: m.Message, Code: m.Code}
}
