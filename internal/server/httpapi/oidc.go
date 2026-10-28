package httpapi

import (
	"context"
	"errors"
	"net/http"
	"time"

	"connectrpc.com/connect"
	authv1 "github.com/ca-x/cognate/gen/skillhub/auth/v1"
	"github.com/ca-x/cognate/gen/skillhub/auth/v1/authv1connect"
	"github.com/ca-x/cognate/internal/server/oidclogin"
)

const oidcStateCookie = "skillhub_oidc_state"
const oidcResultCookie = "skillhub_oidc_result"

func (handler *authHandler) GetLoginOptions(context.Context, *connect.Request[authv1.GetLoginOptionsRequest]) (*connect.Response[authv1.GetLoginOptionsResponse], error) {
	return connect.NewResponse(&authv1.GetLoginOptionsResponse{OidcEnabled: handler.oidc != nil}), nil
}

func (handler *authHandler) StartOIDCLogin(ctx context.Context, request *connect.Request[authv1.StartOIDCLoginRequest]) (*connect.Response[authv1.StartOIDCLoginResponse], error) {
	if handler.oidc == nil {
		return nil, oidcNotConfigured()
	}
	var binding string
	if request.Msg.LinkCurrentAccount {
		if _, err := handler.authenticate(ctx, request.Header()); err != nil {
			return nil, err
		}
		binding = bearer(request.Header())
	}
	started, err := handler.oidc.Begin(ctx, binding)
	if err != nil {
		return nil, oidcError(ctx, err)
	}
	response := connect.NewResponse(&authv1.StartOIDCLoginResponse{AuthorizationUrl: started.AuthorizationURL})
	response.Header().Add("Set-Cookie", handler.oidcCookie(oidcStateCookie, started.BrowserSecret, oidclogin.CallbackPath, 300).String())
	return response, nil
}

func (handler *authHandler) CompleteOIDCLogin(ctx context.Context, request *connect.Request[authv1.CompleteOIDCLoginRequest]) (*connect.Response[authv1.CompleteOIDCLoginResponse], error) {
	if handler.oidc == nil {
		return nil, oidcNotConfigured()
	}
	proof := uniqueCookie(request.Header(), oidcResultCookie)
	token, user, err := handler.oidc.Redeem(ctx, proof)
	if err != nil {
		return nil, oidcError(ctx, err)
	}
	response := connect.NewResponse(&authv1.CompleteOIDCLoginResponse{SessionToken: token.Reveal(), ExpiresAtUnix: token.ExpiresAt().Unix(), UserId: user})
	response.Header().Add("Set-Cookie", handler.oidcCookie(oidcResultCookie, "", authv1connect.AuthServiceCompleteOIDCLoginProcedure, -1).String())
	return response, nil
}

func (handler *authHandler) oidcCallback(writer http.ResponseWriter, request *http.Request) {
	parameters := request.URL.Query()
	proof := uniqueCookie(request.Header, oidcStateCookie)
	http.SetCookie(writer, handler.oidcCookie(oidcStateCookie, "", oidclogin.CallbackPath, -1))
	if len(parameters["state"]) != 1 || len(parameters["code"]) != 1 || len(parameters["error"]) != 0 {
		http.Redirect(writer, request, "/?oidc=failed", http.StatusSeeOther)
		return
	}
	result, err := handler.oidc.Complete(request.Context(), parameters.Get("state"), proof, parameters.Get("code"))
	if err != nil {
		http.Redirect(writer, request, "/?oidc=failed", http.StatusSeeOther)
		return
	}
	http.SetCookie(writer, handler.oidcCookie(oidcResultCookie, result, authv1connect.AuthServiceCompleteOIDCLoginProcedure, 60))
	http.Redirect(writer, request, "/?oidc=complete", http.StatusSeeOther)
}

func (handler *authHandler) oidcCookie(name, value, path string, maxAge int) *http.Cookie {
	cookie := &http.Cookie{Name: name, Value: value, Path: path, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: handler.oidc.SecureCookies(), MaxAge: maxAge}
	if maxAge > 0 {
		cookie.Expires = time.Now().Add(time.Duration(maxAge) * time.Second)
	}
	return cookie
}

func uniqueCookie(header http.Header, name string) string {
	request := &http.Request{Header: header}
	var value string
	found := false
	for _, cookie := range request.Cookies() {
		if cookie.Name == name {
			if found {
				return ""
			}
			found = true
			value = cookie.Value
		}
	}
	return value
}

func oidcNotConfigured() error {
	return connect.NewError(connect.CodeFailedPrecondition, errors.New("OpenID Connect is not configured"))
}

func oidcError(ctx context.Context, err error) error {
	if canceled := contextError(ctx, err); canceled != nil {
		return canceled
	}
	if errors.Is(err, oidclogin.ErrBusy) {
		return connect.NewError(connect.CodeResourceExhausted, errors.New("OpenID Connect capacity exhausted"))
	}
	return connect.NewError(connect.CodeUnauthenticated, errors.New("OpenID Connect login could not be completed"))
}
