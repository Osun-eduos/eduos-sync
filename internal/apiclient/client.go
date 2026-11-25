// Package apiclient calls eduos-api on behalf of a signed-in teacher. All business rules (who may change what,
// edit windows, class rolls, approval status) stay in eduos-api; the sync service applies changes through it.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	base string
	http *http.Client
}

func New(base string) *Client {
	return &Client{base: base, http: &http.Client{Timeout: 20 * time.Second}}
}

// Response is what eduos-api returned. Err is set only when the API could not be reached.
type Response struct {
	Status int
	Body   []byte
}

// Do sends a request with the teacher's own access token.
func (c *Client) Do(ctx context.Context, method, path, token, clientIP string, body any) (Response, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return Response{}, err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, r)
	if err != nil {
		return Response{}, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "eduos-sync")
	if clientIP != "" {
		req.Header.Set("X-Forwarded-For", clientIP)
	}
	res, err := c.http.Do(req)
	if err != nil {
		return Response{}, fmt.Errorf("eduos-api unreachable: %w", err)
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return Response{Status: res.StatusCode, Body: b}, nil
}

// Reference holds state-wide lists the app needs offline: subject names, grading scales and attendance rules.
// They change rarely, so they are fetched from eduos-api and kept for an hour.
type Reference struct {
	Subjects   map[string]string `json:"-"`
	Grading    json.RawMessage   `json:"grading"`
	Attendance json.RawMessage   `json:"attendance"`
}

type ReferenceCache struct {
	api     *Client
	mu      sync.Mutex
	value   *Reference
	fetched time.Time
	ttl     time.Duration
}

func NewReferenceCache(api *Client) *ReferenceCache { return &ReferenceCache{api: api, ttl: time.Hour} }

func (c *ReferenceCache) Get(ctx context.Context, token string) (*Reference, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.value != nil && time.Since(c.fetched) < c.ttl {
		return c.value, nil
	}
	get := func(path string) ([]byte, error) {
		r, err := c.api.Do(ctx, http.MethodGet, path, token, "", nil)
		if err != nil {
			return nil, err
		}
		if r.Status != http.StatusOK {
			return nil, fmt.Errorf("eduos-api %s returned %d", path, r.Status)
		}
		return r.Body, nil
	}
	subjects, err := get("/subjects")
	if err != nil {
		return nil, err
	}
	grading, err := get("/grading")
	if err != nil {
		return nil, err
	}
	rules, err := get("/attendance/rules")
	if err != nil {
		return nil, err
	}
	var list []struct{ Code, Name string }
	if err := json.Unmarshal(subjects, &list); err != nil {
		return nil, err
	}
	ref := &Reference{Subjects: map[string]string{}, Grading: grading, Attendance: rules}
	for _, s := range list {
		ref.Subjects[s.Code] = s.Name
	}
	c.value, c.fetched = ref, time.Now()
	return ref, nil
}
