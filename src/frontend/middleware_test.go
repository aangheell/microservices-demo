// Copyright 2024 Google LLC
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
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestRejectCrossSiteWrites(t *testing.T) {
	ok := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	h := rejectCrossSiteWrites(ok)

	cases := []struct {
		name    string
		method  string
		headers map[string]string
		want    int
	}{
		{"GET is never blocked", "GET", map[string]string{"Sec-Fetch-Site": "cross-site"}, 200},
		{"same-origin form", "POST", map[string]string{"Sec-Fetch-Site": "same-origin"}, 200},
		{"cross-site form", "POST", map[string]string{"Sec-Fetch-Site": "cross-site"}, 403},
		{"same-site subdomain", "POST", map[string]string{"Sec-Fetch-Site": "same-site"}, 403},
		{"old browser, same origin", "POST", map[string]string{"Origin": "http://shop.example"}, 200},
		{"old browser, other origin", "POST", map[string]string{"Origin": "https://evil.example"}, 403},
		{"opaque origin", "POST", map[string]string{"Origin": "null"}, 403},
		{"non-browser client", "POST", nil, 200},
	}
	for _, c := range cases {
		r := httptest.NewRequest(c.method, "http://shop.example/logout", nil)
		for k, v := range c.headers {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != c.want {
			t.Errorf("%s: got %d, want %d", c.name, w.Code, c.want)
		}
	}
}

func TestSessionCookieFlags(t *testing.T) {
	h := ensureSessionID(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/", nil))
	cs := w.Result().Cookies()
	if len(cs) != 1 {
		t.Fatalf("expected one session cookie, got %d", len(cs))
	}
	c := cs[0]
	if c.Name != cookieSessionID || !c.HttpOnly || c.SameSite != http.SameSiteLaxMode || c.Path != "/" {
		t.Errorf("session cookie flags wrong: %+v", c)
	}
}
