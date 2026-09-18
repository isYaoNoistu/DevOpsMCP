package client

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResponseEnvelopes(t *testing.T) {
	tests := []struct {
		name string
		body string
		want *string
		err  string
	}{
		{name: "legacy", body: `{"dat":"old","err":""}`, want: ptr("old")},
		{name: "render", body: `{"data":"new","error":null}`, want: ptr("new")},
		{name: "render object error", body: `{"data":null,"error":{"message":"denied"}}`, err: "denied"},
		{name: "error only", body: `{"error":{"message":"denied without data"}}`, err: "denied without data"},
		{name: "error wins over incompatible data", body: `{"data":{"password":"do-not-print"},"error":{"message":"denied"}}`, err: "denied"},
		{name: "missing envelope", body: `{"status":"ok"}`, err: "missing data envelope"},
		{name: "null is present", body: `{"data":null,"error":null}`, want: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			c, err := NewClient("token", srv.URL, "test")
			if err != nil {
				t.Fatal(err)
			}
			got, err := DoGet[*string](c, context.Background(), "/", nil)
			if tt.err != "" {
				if err == nil || !strings.Contains(err.Error(), tt.err) {
					t.Fatalf("error = %v, want containing %q", err, tt.err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if tt.want == nil {
				if got != nil {
					t.Fatalf("got %q, want nil", *got)
				}
				return
			}
			if got == nil || *got != *tt.want {
				t.Fatalf("got %v, want %q", got, *tt.want)
			}
		})
	}
}

func TestPostUsesRenderEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Fatalf("method = %s", r.Method)
		}
		_, _ = w.Write([]byte(`{"data":{"id":7},"error":null}`))
	}))
	defer srv.Close()
	c, _ := NewClient("token", srv.URL, "test")
	got, err := DoPost[map[string]int](c, context.Background(), "/", map[string]any{})
	if err != nil || got["id"] != 7 {
		t.Fatalf("got %v, err %v", got, err)
	}
}

func TestDecodeErrorDoesNotEchoResponseBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"credential":"super-secret"}`))
	}))
	defer srv.Close()
	c, _ := NewClient("token", srv.URL, "test")
	_, err := DoGet[map[string]any](c, context.Background(), "/", nil)
	if err == nil {
		t.Fatal("expected malformed response error")
	}
	if strings.Contains(err.Error(), "super-secret") {
		t.Fatalf("response body leaked in error: %v", err)
	}
}

func ptr(s string) *string { return &s }
