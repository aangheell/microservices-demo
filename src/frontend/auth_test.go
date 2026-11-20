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
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

var testSecret = []byte("0123456789abcdef0123456789abcdef")

func signToken(t *testing.T, method jwt.SigningMethod, key interface{}, mutate func(jwt.MapClaims)) string {
	t.Helper()
	now := time.Now()
	claims := jwt.MapClaims{
		"sub":   "6f1c2a52-0000-4000-8000-000000000001",
		"email": "alice@example.com",
		"iss":   jwtIssuer,
		"aud":   jwtAudience,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	if mutate != nil {
		mutate(claims)
	}
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestParseUserToken(t *testing.T) {
	valid := signToken(t, jwt.SigningMethodHS256, testSecret, nil)
	claims, err := parseUserToken(valid, testSecret, time.Now())
	if err != nil {
		t.Fatalf("valid token rejected: %v", err)
	}
	if claims.Email != "alice@example.com" || claims.Subject == "" {
		t.Fatalf("unexpected claims: %+v", claims)
	}

	cases := map[string]string{
		"garbage":         "not-a-jwt",
		"wrong secret":    signToken(t, jwt.SigningMethodHS256, []byte("another-secret-another-secret-xx"), nil),
		"alg none":        signToken(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, nil),
		"other HMAC alg":  signToken(t, jwt.SigningMethodHS512, testSecret, nil),
		"expired":         signToken(t, jwt.SigningMethodHS256, testSecret, func(c jwt.MapClaims) { c["exp"] = time.Now().Add(-time.Minute).Unix() }),
		"no expiry":       signToken(t, jwt.SigningMethodHS256, testSecret, func(c jwt.MapClaims) { delete(c, "exp") }),
		"wrong issuer":    signToken(t, jwt.SigningMethodHS256, testSecret, func(c jwt.MapClaims) { c["iss"] = "evil" }),
		"wrong audience":  signToken(t, jwt.SigningMethodHS256, testSecret, func(c jwt.MapClaims) { c["aud"] = "other" }),
		"missing subject": signToken(t, jwt.SigningMethodHS256, testSecret, func(c jwt.MapClaims) { delete(c, "sub") }),
	}
	for name, tok := range cases {
		if _, err := parseUserToken(tok, testSecret, time.Now()); err == nil {
			t.Errorf("%s: token was accepted", name)
		}
	}
}

func TestIsLoggedInRequiresValidToken(t *testing.T) {
	userAuthEnabled, jwtSecret = true, testSecret
	defer func() { userAuthEnabled, jwtSecret = false, nil }()

	withCookie := func(v string) *http.Request {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.AddCookie(&http.Cookie{Name: cookieUserJWT, Value: v})
		return r
	}
	if isLoggedIn(httptest.NewRequest(http.MethodGet, "/", nil)) {
		t.Error("no cookie should not be logged in")
	}
	if isLoggedIn(withCookie("anything")) {
		t.Error("arbitrary cookie value should not be logged in")
	}
	if !isLoggedIn(withCookie(signToken(t, jwt.SigningMethodHS256, testSecret, nil))) {
		t.Error("valid token should be logged in")
	}

	userAuthEnabled = false
	if isLoggedIn(withCookie(signToken(t, jwt.SigningMethodHS256, testSecret, nil))) {
		t.Error("nobody is logged in when accounts are disabled")
	}
}

func TestCSRF(t *testing.T) {
	w := httptest.NewRecorder()
	token := csrfToken(w, httptest.NewRequest(http.MethodGet, "/login", nil))
	cookies := w.Result().Cookies()
	if len(cookies) != 1 || cookies[0].Name != cookieCSRF || cookies[0].Value != token {
		t.Fatalf("expected a CSRF cookie carrying the token, got %+v", cookies)
	}

	post := func(cookie, field string) *http.Request {
		r := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader(url.Values{csrfFormField: {field}}.Encode()))
		r.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if cookie != "" {
			r.AddCookie(&http.Cookie{Name: cookieCSRF, Value: cookie})
		}
		return r
	}
	if !validCSRF(post(token, token)) {
		t.Error("matching token rejected")
	}
	if validCSRF(post(token, "forged")) {
		t.Error("mismatched token accepted")
	}
	if validCSRF(post("", token)) {
		t.Error("missing cookie accepted")
	}
	if validCSRF(post("", "")) {
		t.Error("empty token accepted")
	}

	// An existing cookie is reused rather than rotated on every page view.
	r := httptest.NewRequest(http.MethodGet, "/login", nil)
	r.AddCookie(&http.Cookie{Name: cookieCSRF, Value: token})
	w = httptest.NewRecorder()
	if csrfToken(w, r) != token || len(w.Result().Cookies()) != 0 {
		t.Error("existing CSRF cookie was not reused")
	}
}

func TestAuthErrorMessage(t *testing.T) {
	cases := []struct {
		err  error
		code int
		msg  string
	}{
		{status.Error(codes.Unauthenticated, "invalid email or password"), http.StatusUnauthorized, "Invalid email or password."},
		{status.Error(codes.ResourceExhausted, "x"), http.StatusTooManyRequests, "Too many failed attempts. Please try again later."},
		{status.Error(codes.InvalidArgument, "password must be at least 8 characters"), http.StatusBadRequest, "Password must be at least 8 characters."},
		{status.Error(codes.Internal, "db exploded: secret details"), http.StatusServiceUnavailable, "fallback"},
		{errors.New("plain error"), http.StatusServiceUnavailable, "fallback"},
	}
	for _, c := range cases {
		code, msg := authErrorMessage(c.err, "fallback")
		if code != c.code || msg != c.msg {
			t.Errorf("%v: got (%d, %q), want (%d, %q)", c.err, code, msg, c.code, c.msg)
		}
	}
}

func TestUserCookieSecureFlag(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/login", nil)
	w := httptest.NewRecorder()
	setUserCookie(w, r, "tok")
	if c := w.Result().Cookies()[0]; c.Secure || !c.HttpOnly || c.SameSite != http.SameSiteStrictMode {
		t.Errorf("plain HTTP cookie flags wrong: %+v", c)
	}

	r.Header.Set("X-Forwarded-Proto", "https")
	w = httptest.NewRecorder()
	setUserCookie(w, r, "tok")
	if c := w.Result().Cookies()[0]; !c.Secure {
		t.Error("cookie behind HTTPS proxy should be Secure")
	}
}
