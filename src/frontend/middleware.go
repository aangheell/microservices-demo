// Copyright 2018 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
)

type ctxKeyLog struct{}
type ctxKeyRequestID struct{}

type logHandler struct {
	log  *logrus.Logger
	next http.Handler
}

type responseRecorder struct {
	b      int
	status int
	w      http.ResponseWriter
}

func (r *responseRecorder) Header() http.Header { return r.w.Header() }

func (r *responseRecorder) Write(p []byte) (int, error) {
	if r.status == 0 {
		r.status = http.StatusOK
	}
	n, err := r.w.Write(p)
	r.b += n
	return n, err
}

func (r *responseRecorder) WriteHeader(statusCode int) {
	r.status = statusCode
	r.w.WriteHeader(statusCode)
}

func (lh *logHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	requestID, _ := uuid.NewRandom()
	ctx = context.WithValue(ctx, ctxKeyRequestID{}, requestID.String())

	start := time.Now()
	rr := &responseRecorder{w: w}
	log := lh.log.WithFields(logrus.Fields{
		"http.req.path":   r.URL.Path,
		"http.req.method": r.Method,
		"http.req.id":     requestID.String(),
	})
	if v, ok := r.Context().Value(ctxKeySessionID{}).(string); ok {
		log = log.WithField("session", v)
	}
	log.Debug("request started")
	defer func() {
		log.WithFields(logrus.Fields{
			"http.resp.took_ms": int64(time.Since(start) / time.Millisecond),
			"http.resp.status":  rr.status,
			"http.resp.bytes":   rr.b}).Debugf("request complete")
	}()

	ctx = context.WithValue(ctx, ctxKeyLog{}, log)
	r = r.WithContext(ctx)
	lh.next.ServeHTTP(rr, r)
}

func ensureSessionID(next http.Handler) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var sessionID string
		c, err := r.Cookie(cookieSessionID)
		if err == http.ErrNoCookie {
			if os.Getenv("ENABLE_SINGLE_SHARED_SESSION") == "true" {
				// Hard coded user id, shared across sessions
				sessionID = "12345678-1234-1234-1234-123456789123"
			} else {
				u, _ := uuid.NewRandom()
				sessionID = u.String()
			}
			http.SetCookie(w, &http.Cookie{
				Name:   cookieSessionID,
				Value:  sessionID,
				MaxAge: cookieMaxAge,
				// Site-wide, so logout (which expires cookies at "/") really
				// drops it and the next visitor gets a fresh guest cart.
				Path:     "/",
				HttpOnly: true, // no script needs it; keeps it away from XSS
				Secure:   isSecureRequest(r),
				SameSite: http.SameSiteLaxMode,
			})
		} else if err != nil {
			return
		} else {
			sessionID = c.Value
		}
		ctx := context.WithValue(r.Context(), ctxKeySessionID{}, sessionID)
		r = r.WithContext(ctx)
		next.ServeHTTP(w, r)
	}
}

// rejectCrossSiteWrites blocks state-changing requests (POST etc.) that a
// browser sends on behalf of another site: cross-site request forgery, e.g.
// a page elsewhere silently logging users out or filling their cart.
// Browsers label every request with Sec-Fetch-Site and send Origin on
// cross-origin POSTs; clients that send neither (curl, the load generator)
// aren't browsers carrying a victim's cookies, so they pass.
func rejectCrossSiteWrites(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet || r.Method == http.MethodHead || r.Method == http.MethodOptions {
			next.ServeHTTP(w, r)
			return
		}
		if !sameOriginRequest(r) {
			http.Error(w, "cross-site request rejected", http.StatusForbidden)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func sameOriginRequest(r *http.Request) bool {
	switch r.Header.Get("Sec-Fetch-Site") {
	case "same-origin", "none": // "none": typed URL or bookmark
		return true
	case "":
		// Older browser: fall back to Origin.
	default: // same-site, cross-site
		return false
	}
	origin := r.Header.Get("Origin")
	if origin == "" {
		return true
	}
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && u.Host == r.Host
}
