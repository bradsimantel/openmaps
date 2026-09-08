package api

import "net/http"

// RoutingAdmission shares a fixed request budget across snapshot replacements.
// There is no pending request queue. Hold admission through response encoding.
func RoutingAdmission(next http.Handler, concurrency int) http.Handler {
	if concurrency < 1 || concurrency > 64 {
		panic("routing concurrency must be 1-64")
	}
	slots := make(chan struct{}, concurrency)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/directions/v2:computeRoutes" {
			next.ServeHTTP(w, r)
			return
		}
		if r.Context().Err() != nil {
			return
		}
		select {
		case slots <- struct{}{}:
			defer func() { <-slots }()
			next.ServeHTTP(w, r)
		default:
			w.Header().Set("Retry-After", "1")
			failure(w, http.StatusTooManyRequests, "RESOURCE_EXHAUSTED", "Concurrent routing request limit reached; retry later.")
		}
	})
}
