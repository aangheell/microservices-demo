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
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/sirupsen/logrus"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

const (
	// Must match the claims issued by userservice.
	jwtIssuer       = "userservice"
	jwtAudience     = "frontend"
	jwtCookieMaxAge = 24 * 60 * 60
	jwtSecretMinLen = 32

	csrfFormField = "csrf_token"
)

var (
	// Set in main when USER_SERVICE_ADDR is configured.
	userAuthEnabled bool
	jwtSecret       []byte
)

type userClaims struct {
	Email string `json:"email"`
	jwt.RegisteredClaims
}

// currentUser returns the claims of the signed-in user, or nil if the request
// carries no JWT cookie or the token fails verification.
func currentUser(r *http.Request) *userClaims {
	if !userAuthEnabled {
		return nil
	}
	c, err := r.Cookie(cookieUserJWT)
	if err != nil || c.Value == "" {
		return nil
	}
	claims, err := parseUserToken(c.Value, jwtSecret, time.Now())
	if err != nil {
		return nil
	}
	return claims
}

func parseUserToken(token string, secret []byte, now time.Time) (*userClaims, error) {
	claims := &userClaims{}
	_, err := jwt.ParseWithClaims(token, claims,
		func(*jwt.Token) (interface{}, error) { return secret, nil },
		jwt.WithValidMethods([]string{jwt.SigningMethodHS256.Alg()}),
		jwt.WithIssuer(jwtIssuer),
		jwt.WithAudience(jwtAudience),
		jwt.WithExpirationRequired(),
		jwt.WithTimeFunc(func() time.Time { return now }),
	)
	if err != nil {
		return nil, err
	}
	if claims.Subject == "" {
		return nil, jwt.ErrTokenInvalidClaims
	}
	return claims, nil
}

func isLoggedIn(r *http.Request) bool {
	return currentUser(r) != nil
}

// isSecureRequest reports whether the client reached us over HTTPS, either
// directly or through a TLS-terminating proxy.
func isSecureRequest(r *http.Request) bool {
	return r.TLS != nil || strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// csrfToken returns the request's CSRF token, issuing a new cookie if needed.
// Login and register forms use the double-submit cookie pattern: the token is
// set as a cookie and echoed in a hidden form field, and both must match.
func csrfToken(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie(cookieCSRF); err == nil && len(c.Value) >= 32 {
		return c.Value
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err) // crypto/rand never fails on supported platforms
	}
	token := base64.RawURLEncoding.EncodeToString(b)
	http.SetCookie(w, &http.Cookie{
		Name:     cookieCSRF,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
	})
	return token
}

func validCSRF(r *http.Request) bool {
	c, err := r.Cookie(cookieCSRF)
	if err != nil || c.Value == "" {
		return false
	}
	form := r.PostFormValue(csrfFormField)
	return subtle.ConstantTimeCompare([]byte(c.Value), []byte(form)) == 1
}

func setUserCookie(w http.ResponseWriter, r *http.Request, token string) {
	http.SetCookie(w, &http.Cookie{
		Name:     cookieUserJWT,
		Value:    token,
		MaxAge:   jwtCookieMaxAge,
		HttpOnly: true,
		Secure:   isSecureRequest(r),
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})
}

// renderAuthForm renders the login or register page. Error messages are only
// ever chosen by the server, never taken from the request.
func renderAuthForm(w http.ResponseWriter, r *http.Request, log logrus.FieldLogger, tmpl string, code int, errMsg string) {
	token := csrfToken(w, r)
	w.WriteHeader(code)
	if err := templates.ExecuteTemplate(w, tmpl, injectCommonTemplateData(r, map[string]interface{}{
		"show_currency": false,
		"error":         errMsg,
		"csrf_token":    token,
	})); err != nil {
		log.Errorf("could not render %s template: %v", tmpl, err)
	}
}

// authErrorMessage maps a userservice error to an HTTP status and a message
// that is safe to show to the user.
func authErrorMessage(err error, fallback string) (int, string) {
	st, _ := status.FromError(err)
	switch st.Code() {
	case codes.Unauthenticated:
		return http.StatusUnauthorized, "Invalid email or password."
	case codes.ResourceExhausted:
		return http.StatusTooManyRequests, "Too many failed attempts. Please try again later."
	case codes.AlreadyExists:
		return http.StatusConflict, "An account with this email already exists."
	case codes.InvalidArgument:
		// Validation messages are written by userservice, not the client.
		return http.StatusBadRequest, capitalize(st.Message()) + "."
	default:
		return http.StatusServiceUnavailable, fallback
	}
}

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

func (fe *frontendServer) loginHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)
	if r.Method == http.MethodGet {
		renderAuthForm(w, r, log, "login", http.StatusOK, "")
		return
	}
	if !validCSRF(r) {
		renderAuthForm(w, r, log, "login", http.StatusForbidden, "Your session expired. Please try again.")
		return
	}
	resp, err := pb.NewUserServiceClient(fe.userSvcConn).Login(r.Context(), &pb.LoginRequest{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
	})
	if err != nil {
		log.Warnf("login failed: %v", status.Code(err))
		code, msg := authErrorMessage(err, "Sign in is temporarily unavailable.")
		renderAuthForm(w, r, log, "login", code, msg)
		return
	}
	setUserCookie(w, r, resp.GetJwtToken())
	fe.adoptGuestCart(r, log, resp.GetJwtToken())
	http.Redirect(w, r, baseUrl+"/", http.StatusSeeOther)
}

func (fe *frontendServer) registerHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)
	if r.Method == http.MethodGet {
		renderAuthForm(w, r, log, "register", http.StatusOK, "")
		return
	}
	if !validCSRF(r) {
		renderAuthForm(w, r, log, "register", http.StatusForbidden, "Your session expired. Please try again.")
		return
	}
	resp, err := pb.NewUserServiceClient(fe.userSvcConn).Register(r.Context(), &pb.RegisterRequest{
		Email:    r.PostFormValue("email"),
		Password: r.PostFormValue("password"),
		Name:     r.PostFormValue("name"),
	})
	if err != nil {
		log.Warnf("register failed: %v", status.Code(err))
		code, msg := authErrorMessage(err, "Registration is temporarily unavailable.")
		renderAuthForm(w, r, log, "register", code, msg)
		return
	}
	setUserCookie(w, r, resp.GetJwtToken())
	fe.adoptGuestCart(r, log, resp.GetJwtToken())
	http.Redirect(w, r, baseUrl+"/", http.StatusSeeOther)
}

// adoptGuestCart moves what the visitor put in the cart before signing in
// into their account's cart, so it follows them to other devices. Failure
// is logged but never blocks the sign-in: the items stay in the guest cart.
func (fe *frontendServer) adoptGuestCart(r *http.Request, log logrus.FieldLogger, token string) {
	claims, err := parseUserToken(token, jwtSecret, time.Now())
	if err != nil {
		log.Warnf("not merging guest cart, token from userservice rejected: %v", err)
		return
	}
	if err := mergeCarts(r.Context(), fe, guestCartKey(sessionID(r)), userCartKey(claims.Subject)); err != nil {
		log.Warnf("could not merge guest cart: %v", err)
	}
}
