package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRoutingAdmission(t *testing.T) {
	entered, release, done := make(chan struct{}), make(chan struct{}), make(chan struct{})
	h := RoutingAdmission(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			w.WriteHeader(200)
			return
		}
		entered <- struct{}{}
		<-release
		w.WriteHeader(200)
	}), 1)
	request := func() *http.Request { return httptest.NewRequest("POST", "/directions/v2:computeRoutes", nil) }
	go func() { h.ServeHTTP(httptest.NewRecorder(), request()); close(done) }()
	<-entered
	w := httptest.NewRecorder()
	h.ServeHTTP(w, request())
	if w.Code != 429 || w.Header().Get("Retry-After") != "1" || !strings.Contains(w.Body.String(), "RESOURCE_EXHAUSTED") {
		t.Fatal(w)
	}
	health := httptest.NewRecorder()
	h.ServeHTTP(health, httptest.NewRequest("GET", "/healthz", nil))
	if health.Code != 200 {
		t.Fatal(health)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	h.ServeHTTP(httptest.NewRecorder(), request().WithContext(ctx))
	close(release)
	<-done
	// A released permit admits the next request; no canceled waiter can consume it.
	finished := make(chan struct{})
	go func() { h.ServeHTTP(httptest.NewRecorder(), request()); close(finished) }()
	<-entered
	<-finished
}
