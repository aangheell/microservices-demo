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

	"github.com/sirupsen/logrus"
	pb "github.com/GoogleCloudPlatform/microservices-demo/src/frontend/genproto"
)

func (fe *frontendServer) loginHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)
	if r.Method == http.MethodGet {
		if err := templates.ExecuteTemplate(w, "login", injectCommonTemplateData(r, map[string]interface{}{
			"show_currency": false,
			"error":         r.URL.Query().Get("error"),
		})); err != nil {
			log.Errorf("could not render login template: %v", err)
		}
		return
	}
	resp, err := pb.NewUserServiceClient(fe.userSvcConn).Login(r.Context(), &pb.LoginRequest{
		Email:    r.FormValue("email"),
		Password: r.FormValue("password"),
	})
	if err != nil {
		log.Warnf("login failed: %v", err)
		http.Redirect(w, r, baseUrl+"/login?error=invalid+credentials", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieUserJWT,
		Value:    resp.GetJwtToken(),
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})
	http.Redirect(w, r, baseUrl+"/", http.StatusSeeOther)
}

func (fe *frontendServer) registerHandler(w http.ResponseWriter, r *http.Request) {
	log := r.Context().Value(ctxKeyLog{}).(logrus.FieldLogger)
	if r.Method == http.MethodGet {
		if err := templates.ExecuteTemplate(w, "register", injectCommonTemplateData(r, map[string]interface{}{
			"show_currency": false,
			"error":         r.URL.Query().Get("error"),
		})); err != nil {
			log.Errorf("could not render register template: %v", err)
		}
		return
	}
	resp, err := pb.NewUserServiceClient(fe.userSvcConn).Register(r.Context(), &pb.RegisterRequest{
		Email:    r.FormValue("email"),
		Password: r.FormValue("password"),
		Name:     r.FormValue("name"),
	})
	if err != nil {
		log.Warnf("register failed: %v", err)
		http.Redirect(w, r, baseUrl+"/register?error=registration+failed", http.StatusSeeOther)
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name:     cookieUserJWT,
		Value:    resp.GetJwtToken(),
		MaxAge:   cookieMaxAge,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Path:     "/",
	})
	http.Redirect(w, r, baseUrl+"/", http.StatusSeeOther)
}

// isLoggedIn returns true if the user has a valid JWT cookie.
func isLoggedIn(r *http.Request) bool {
	_, err := r.Cookie(cookieUserJWT)
	return err == nil
}
