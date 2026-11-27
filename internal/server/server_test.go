package server

import (
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Osun-eduos/eduos-sync/internal/apiclient"
	"github.com/Osun-eduos/eduos-sync/internal/auth"
	"github.com/Osun-eduos/eduos-sync/internal/store"
	"github.com/golang-jwt/jwt/v5"
)

// One signing key for the whole test run, as eduos-api would hold; the server gets only its public half.
var signingKey, publicKeyPEM = func() (*ecdsa.PrivateKey, string) {
	k, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		panic(err)
	}
	der, err := x509.MarshalPKIXPublicKey(&k.PublicKey)
	if err != nil {
		panic(err)
	}
	return k, string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}))
}()

func verifier() *auth.Verifier {
	v, err := auth.NewVerifier(publicKeyPEM)
	if err != nil {
		panic(err)
	}
	return v
}

type fakeStore struct {
	mu     sync.Mutex
	events map[string]store.Event
	sheets map[string]string
}

func (f *fakeStore) Ping(context.Context) error { return nil }
func (f *fakeStore) FindEvent(_ context.Context, id string) (*store.Event, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if e, ok := f.events[id]; ok {
		return &e, nil
	}
	return nil, store.ErrNotFound
}
func (f *fakeStore) SaveEvent(_ context.Context, e store.Event) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.events[e.ID] = e
	return nil
}
func (f *fakeStore) Lock(context.Context, string) (func(), error) { return func() {}, nil }
func (f *fakeStore) SheetID(_ context.Context, cs string) (string, error) {
	if id, ok := f.sheets[cs]; ok {
		return id, nil
	}
	return "", store.ErrNotFound
}
func (f *fakeStore) Snapshot(_ context.Context, userID string, _ int, _ time.Time, name func(string) string) (*store.Snapshot, error) {
	return &store.Snapshot{User: store.SnapUser{ID: userID}, Subjects: []store.SnapSubj{{Code: "MTH", Name: name("MTH")}}}, nil
}

type call struct{ method, path string }

type fakeAPI struct {
	calls   []call
	respond func(method, path string) (apiclient.Response, error)
}

func (f *fakeAPI) Do(_ context.Context, method, path, _, _ string, _ any) (apiclient.Response, error) {
	f.calls = append(f.calls, call{method, path})
	return f.respond(method, path)
}

type fakeRefs struct{}

func (fakeRefs) Get(context.Context, string) (*apiclient.Reference, error) {
	return &apiclient.Reference{Subjects: map[string]string{"MTH": "Mathematics"}, Grading: json.RawMessage(`{}`), Attendance: json.RawMessage(`{}`)}, nil
}

func token(t *testing.T, sub, role string, mcp bool) string {
	t.Helper()
	s, _ := jwt.NewWithClaims(jwt.SigningMethodES256, jwt.MapClaims{"sub": sub, "role": role, "sid": "s1", "mcp": mcp, "iss": "eduos-api", "exp": time.Now().Add(time.Minute).Unix()}).SignedString(signingKey)
	return s
}

func setup(respond func(method, path string) (apiclient.Response, error)) (*Server, *fakeStore, *fakeAPI) {
	st := &fakeStore{events: map[string]store.Event{}, sheets: map[string]string{"11111111-1111-4111-8111-111111111111": "sheet-1"}}
	api := &fakeAPI{respond: respond}
	return &Server{Store: st, API: api, Refs: fakeRefs{}, Verifier: verifier(), RegisterDays: 14, MaxEvents: 5, MaxBodyBytes: 1 << 20, Now: time.Now, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}, st, api
}

func ok(string, string) (apiclient.Response, error) {
	return apiclient.Response{Status: 200, Body: []byte(`{}`)}, nil
}

func do(t *testing.T, s *Server, tok, device string, body any) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/v1/sync", bytes.NewReader(b))
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	if device != "" {
		req.Header.Set("X-Device-Id", device)
	}
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func ev(id, typ string, payload string) map[string]any {
	return map[string]any{"id": id, "type": typ, "occurredAt": time.Now().Format(time.RFC3339), "payload": json.RawMessage(payload)}
}

func results(t *testing.T, out map[string]any) []map[string]any {
	t.Helper()
	raw, _ := json.Marshal(out["results"])
	var r []map[string]any
	_ = json.Unmarshal(raw, &r)
	return r
}

const (
	id1 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa1"
	id2 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa2"
	id3 = "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaa3"
	dev = "phone-0001"
	reg = `{"classId":"c1","date":"2026-11-24","marks":[]}`
)

func TestAccess(t *testing.T) {
	s, _, _ := setup(ok)
	body := map[string]any{"events": []any{ev(id1, "attendance.register", reg)}}
	if code, _ := do(t, s, "", dev, body); code != 401 {
		t.Errorf("no token: %d", code)
	}
	if code, _ := do(t, s, token(t, "u1", "PARENT", false), dev, body); code != 403 {
		t.Errorf("parent: %d", code)
	}
	if code, out := do(t, s, token(t, "u1", "TEACHER", true), dev, body); code != 403 || out["code"] != "PASSWORD_CHANGE_REQUIRED" {
		t.Errorf("temporary password: %d %v", code, out)
	}
	if code, _ := do(t, s, token(t, "u1", "TEACHER", false), "", body); code != 400 {
		t.Errorf("no device id: %d", code)
	}
}

func TestValidation(t *testing.T) {
	s, _, _ := setup(ok)
	tok := token(t, "u1", "TEACHER", false)
	if code, _ := do(t, s, tok, dev, map[string]any{"events": []any{}}); code != 400 {
		t.Errorf("empty: %d", code)
	}
	if code, _ := do(t, s, tok, dev, map[string]any{"events": []any{ev("not-a-uuid", "attendance.register", reg)}}); code != 400 {
		t.Errorf("bad id: %d", code)
	}
	if code, _ := do(t, s, tok, dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg), ev(id1, "attendance.register", reg)}}); code != 400 {
		t.Errorf("repeated id: %d", code)
	}
	many := []any{}
	for i := 0; i < 6; i++ {
		many = append(many, ev(strings.Replace(id1, "a1", "b"+string(rune('0'+i)), 1), "attendance.register", reg))
	}
	if code, _ := do(t, s, tok, dev, map[string]any{"events": many}); code != 413 {
		t.Errorf("too many: %d", code)
	}
	if code, _ := do(t, s, tok, dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg)}, "extra": 1}); code != 400 {
		t.Errorf("unknown field: %d", code)
	}
}

func TestAppliesOnce(t *testing.T) {
	s, st, api := setup(ok)
	tok := token(t, "u1", "TEACHER", false)
	body := map[string]any{"events": []any{ev(id1, "attendance.register", reg), ev(id2, "scores.save", `{"classSubjectId":"x","scores":[]}`)}}
	code, out := do(t, s, tok, dev, body)
	r := results(t, out)
	if code != 200 || r[0]["status"] != "applied" || r[1]["status"] != "applied" {
		t.Fatalf("first upload: %d %v", code, out)
	}
	if len(api.calls) != 2 || api.calls[0] != (call{"PUT", "/attendance/register"}) || api.calls[1] != (call{"PUT", "/scoresheets"}) {
		t.Errorf("calls: %v", api.calls)
	}
	// The phone lost the response and sends the same events again.
	_, out = do(t, s, tok, dev, body)
	r = results(t, out)
	if r[0]["status"] != "applied" || r[0]["duplicate"] != true || len(api.calls) != 2 {
		t.Errorf("retry applied again: %v calls=%d", r, len(api.calls))
	}
	if st.events[id1].DeviceID != dev || st.events[id1].Status != "APPLIED" {
		t.Errorf("recorded: %+v", st.events[id1])
	}
	// Another teacher cannot reuse the id.
	_, out = do(t, s, token(t, "u2", "TEACHER", false), dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg)}})
	if r := results(t, out); r[0]["status"] != "rejected" || r[0]["httpStatus"] != float64(409) {
		t.Errorf("other user: %v", r)
	}
}

func TestRejectionsAreFinal(t *testing.T) {
	s, _, api := setup(func(string, string) (apiclient.Response, error) {
		return apiclient.Response{Status: 403, Body: []byte(`{"message":"Class teachers can change registers for the last 7 days."}`)}, nil
	})
	tok := token(t, "u1", "TEACHER", false)
	body := map[string]any{"events": []any{ev(id1, "attendance.register", reg)}}
	_, out := do(t, s, tok, dev, body)
	r := results(t, out)
	if r[0]["status"] != "rejected" || !strings.Contains(r[0]["message"].(string), "last 7 days") {
		t.Fatalf("rejection: %v", r)
	}
	_, out = do(t, s, tok, dev, body)
	if r := results(t, out); r[0]["status"] != "rejected" || r[0]["duplicate"] != true || len(api.calls) != 1 {
		t.Errorf("repeat: %v calls=%d", r, len(api.calls))
	}
}

func TestOutageKeepsOrder(t *testing.T) {
	n := 0
	s, st, _ := setup(func(string, string) (apiclient.Response, error) {
		n++
		if n == 2 {
			return apiclient.Response{}, errors.New("connection refused")
		}
		return apiclient.Response{Status: 200}, nil
	})
	tok := token(t, "u1", "TEACHER", false)
	_, out := do(t, s, tok, dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg), ev(id2, "attendance.register", reg), ev(id3, "attendance.register", reg)}})
	r := results(t, out)
	if r[0]["status"] != "applied" || r[1]["status"] != "retry" || r[2]["status"] != "retry" || n != 2 {
		t.Errorf("results: %v calls=%d", r, n)
	}
	if _, ok := st.events[id2]; ok {
		t.Error("a failed event was recorded")
	}
	s2, _, _ := setup(func(string, string) (apiclient.Response, error) { return apiclient.Response{Status: 503}, nil })
	_, out = do(t, s2, tok, dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg)}})
	if r := results(t, out); r[0]["status"] != "retry" {
		t.Errorf("503: %v", r)
	}
}

func TestExpiredTokenStopsUpload(t *testing.T) {
	s, _, _ := setup(func(string, string) (apiclient.Response, error) { return apiclient.Response{Status: 401}, nil })
	code, _ := do(t, s, token(t, "u1", "TEACHER", false), dev, map[string]any{"events": []any{ev(id1, "attendance.register", reg)}})
	if code != 401 {
		t.Errorf("code %d", code)
	}
}

func TestSubmitAndUnknownTypes(t *testing.T) {
	s, _, api := setup(ok)
	tok := token(t, "u1", "TEACHER", false)
	_, out := do(t, s, tok, dev, map[string]any{"events": []any{
		ev(id1, "scores.submit", `{"classSubjectId":"11111111-1111-4111-8111-111111111111"}`),
		ev(id2, "scores.submit", `{"classSubjectId":"22222222-2222-4222-8222-222222222222"}`),
		ev(id3, "homework.set", `{}`),
	}})
	r := results(t, out)
	if r[0]["status"] != "applied" || api.calls[0] != (call{"POST", "/scoresheets/sheet-1/submit"}) {
		t.Errorf("submit: %v %v", r[0], api.calls)
	}
	if r[1]["status"] != "rejected" || !strings.Contains(r[1]["message"].(string), "No scores") {
		t.Errorf("no sheet: %v", r[1])
	}
	if r[2]["status"] != "rejected" || !strings.Contains(r[2]["message"].(string), "Update the app") {
		t.Errorf("unknown: %v", r[2])
	}
	if len(api.calls) != 1 {
		t.Errorf("calls %v", api.calls)
	}
}

func TestSnapshot(t *testing.T) {
	s, _, _ := setup(ok)
	req := httptest.NewRequest(http.MethodGet, "/v1/snapshot", nil)
	req.Header.Set("Authorization", "Bearer "+token(t, "u1", "TEACHER", false))
	w := httptest.NewRecorder()
	s.Routes().ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"name":"Mathematics"`) || !strings.Contains(w.Body.String(), `"today"`) {
		t.Errorf("snapshot: %d %s", w.Code, w.Body.String())
	}
}

func TestToday(t *testing.T) {
	s, _, _ := setup(ok)
	s.Now = func() time.Time { return time.Date(2026, 11, 24, 23, 30, 0, 0, time.UTC) } // 00:30 on the 25th in Lagos
	if got := s.today().Format("2006-01-02"); got != "2026-11-25" {
		t.Errorf("today %s", got)
	}
}
