package authflowv2

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"net/url"

	"github.com/authgear/oauthrelyingparty/pkg/api/oauthrelyingparty"

	handlerwebapp "github.com/authgear/authgear-server/pkg/auth/handler/webapp"
	v2viewmodels "github.com/authgear/authgear-server/pkg/auth/handler/webapp/authflowv2/viewmodels"
	"github.com/authgear/authgear-server/pkg/auth/handler/webapp/viewmodels"
	"github.com/authgear/authgear-server/pkg/auth/webapp"
	authflow "github.com/authgear/authgear-server/pkg/lib/authenticationflow"
	"github.com/authgear/authgear-server/pkg/lib/config"
	"github.com/authgear/authgear-server/pkg/lib/meter"
	"github.com/authgear/authgear-server/pkg/util/httputil"
	"github.com/authgear/authgear-server/pkg/util/template"
	"github.com/authgear/authgear-server/pkg/util/validation"
)

var TemplateWebAuthflowLoginHTML = template.RegisterHTML(
	"web/authflowv2/login.html",
	handlerwebapp.Components...,
)

var AuthflowLoginLoginIDSchema = validation.NewSimpleSchema(`
	{
		"type": "object",
		"properties": {
			"x_login_id": { "type": "string" },
			"x_login_id_input_type": { "type": "string" }
		},
		"required": ["x_login_id", "x_login_id_input_type"]
	}
`)

type AuthflowLoginEndpointsProvider interface {
	SSOCallbackURL(alias string) *url.URL
}

type AuthflowLoginViewModel struct {
	AllowLoginOnly bool
}

func NewAuthflowLoginViewModel(allowLoginOnly bool) AuthflowLoginViewModel {
	return AuthflowLoginViewModel{
		AllowLoginOnly: allowLoginOnly,
	}
}

type AuthflowV2LoginHandler struct {
	SignupLoginHandler   InternalAuthflowV2SignupLoginHandler
	UIConfig             *config.UIConfig
	AuthenticationConfig *config.AuthenticationConfig
	Controller           *handlerwebapp.AuthflowController
	BaseViewModel        *viewmodels.BaseViewModeler
	AuthflowViewModel    *viewmodels.AuthflowViewModeler
	Renderer             handlerwebapp.Renderer
	MeterService         handlerwebapp.MeterService
	TutorialCookie       handlerwebapp.TutorialCookie
	ErrorService         handlerwebapp.ErrorService
	Endpoints            AuthflowLoginEndpointsProvider
}

func (h *AuthflowV2LoginHandler) GetData(w http.ResponseWriter, r *http.Request, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse, allowLoginOnly bool) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	baseViewModel := h.BaseViewModel.ViewModelForAuthFlow(r, w)
	if h.TutorialCookie.Pop(r, w, httputil.SignupLoginTutorialCookieName) {
		baseViewModel.SetTutorial(httputil.SignupLoginTutorialCookieName)
	}
	viewmodels.Embed(data, baseViewModel)
	authflowViewModel := h.AuthflowViewModel.NewWithAuthflow(s, screen.StateTokenFlowResponse, r)
	viewmodels.Embed(data, authflowViewModel)
	viewmodels.Embed(data, v2viewmodels.NewOAuthErrorViewModel(baseViewModel.RawError))
	viewmodels.Embed(data, NewAuthflowLoginViewModel(allowLoginOnly))
	return data, nil
}

func (h *AuthflowV2LoginHandler) GetInlinePreviewData(w http.ResponseWriter, r *http.Request) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	baseViewModel := h.BaseViewModel.ViewModelForInlinePreviewAuthFlow(r, w)
	viewmodels.Embed(data, baseViewModel)
	authflowViewModel := h.AuthflowViewModel.NewWithConfig()
	viewmodels.Embed(data, authflowViewModel)
	viewmodels.Embed(data, v2viewmodels.NewOAuthErrorViewModel(baseViewModel.RawError))
	viewmodels.Embed(data, NewAuthflowLoginViewModel(false))
	return data, nil
}

func (h *AuthflowV2LoginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if h.UIConfig.SignupLoginFlowEnabled && !h.AuthenticationConfig.PublicSignupDisabled {
		// Login will be same as signup
		h.SignupLoginHandler.ServeHTTP(w, r, AuthflowV2SignupServeOptions{
			FlowType:         authflow.FlowTypeSignupLogin,
			CanSwitchToLogin: false,
			UIVariant:        AuthflowV2SignupUIVariantSignupLogin,
		})
		return
	}

	opts := webapp.SessionOptions{
		RedirectURI: h.Controller.RedirectURI(r),
	}

	oauthPostAction := func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse, providerAlias string) error {
		callbackURL := h.Endpoints.SSOCallbackURL(providerAlias).String()
		input := map[string]interface{}{
			"identification": "oauth",
			"alias":          providerAlias,
			"redirect_uri":   callbackURL,
			"response_mode":  oauthrelyingparty.ResponseModeFormPost,
		}

		err := handlerwebapp.HandleIdentificationBotProtection(ctx, config.AuthenticationFlowIdentificationOAuth, screen.StateTokenFlowResponse, r.Form, input)
		if err != nil {
			return err
		}

		result, err := h.Controller.ReplaceScreen(ctx, r, s, authflow.FlowTypeSignupLogin, input)
		if err != nil {
			return err
		}

		result.WriteResponse(w, r)
		return nil
	}

	var handlers handlerwebapp.AuthflowControllerHandlers
	handlers.Get(func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		oauthProviderAlias := s.OAuthProviderAlias
		allowLoginOnly := s.UserIDHint != ""

		visitorID := webapp.GetVisitorID(ctx)
		if visitorID == "" {
			// visitor id should be generated by VisitorIDMiddleware
			return fmt.Errorf("webapp: missing visitor id")
		}

		err := h.MeterService.TrackPageView(ctx, visitorID, meter.PageTypeLogin)
		if err != nil {
			return err
		}

		hasErr := h.ErrorService.HasError(ctx, r)
		// If x_oauth_provider_alias is provided via authz endpoint
		// redirect the user to the oauth provider
		// If there is error in the ErrorCookie, the user will stay in the login
		// page to see the error message and the redirection won't be performed
		if !hasErr && oauthProviderAlias != "" {
			return oauthPostAction(ctx, s, screen, oauthProviderAlias)
		}

		data, err := h.GetData(w, r, s, screen, allowLoginOnly)
		if err != nil {
			return err
		}

		h.Renderer.RenderHTML(w, r, TemplateWebAuthflowLoginHTML, data)
		return nil
	})

	handlers.PostAction("oauth", func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		providerAlias := r.Form.Get("x_provider_alias")
		return oauthPostAction(ctx, s, screen, providerAlias)
	})

	handlers.PostAction("login_id", func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		err := AuthflowLoginLoginIDSchema.Validator().ValidateValue(ctx, handlerwebapp.FormToJSON(r.Form))
		if err != nil {
			return err
		}

		loginID := r.Form.Get("x_login_id")
		loginIDInputType := r.Form.Get("x_login_id_input_type")
		identification := webapp.GetMostAppropriateIdentification(ctx, screen.StateTokenFlowResponse, loginID, loginIDInputType)
		input := map[string]interface{}{
			"identification": identification,
			"login_id":       loginID,
		}

		err = handlerwebapp.HandleIdentificationBotProtection(ctx, identification, screen.StateTokenFlowResponse, r.Form, input)
		if err != nil {
			return err
		}

		result, err := h.Controller.AdvanceWithInput(ctx, r, s, screen, input, nil)
		if err != nil {
			return err
		}

		result.WriteResponse(w, r)
		return nil
	})

	handlers.PostAction("ldap", func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		serverName := r.FormValue("x_server_name")
		q := url.Values{}
		q.Set("q_server_name", serverName)

		result := h.Controller.AdvanceDirectly(AuthflowV2RouteLDAPLogin, screen, q)

		result.WriteResponse(w, r)
		return nil
	})

	handlers.PostAction("passkey", func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		assertionResponseStr := r.Form.Get("x_assertion_response")

		var assertionResponseJSON interface{}
		err := json.Unmarshal([]byte(assertionResponseStr), &assertionResponseJSON)
		if err != nil {
			return err
		}

		input := map[string]interface{}{
			"identification":     "passkey",
			"assertion_response": assertionResponseJSON,
		}

		err = handlerwebapp.HandleIdentificationBotProtection(ctx, config.AuthenticationFlowIdentificationPasskey, screen.StateTokenFlowResponse, r.Form, input)
		if err != nil {
			return err
		}

		result, err := h.Controller.AdvanceWithInput(ctx, r, s, screen, input, nil)
		if err != nil {
			return err
		}

		result.WriteResponse(w, r)
		return nil
	})

	handlers.InlinePreview(func(ctx context.Context, w http.ResponseWriter, r *http.Request) error {
		data, err := h.GetInlinePreviewData(w, r)
		if err != nil {
			return err
		}
		h.Renderer.RenderHTML(w, r, TemplateWebAuthflowLoginHTML, data)
		return nil
	})

	h.Controller.HandleStartOfFlow(r.Context(), w, r, opts, authflow.FlowTypeLogin, &handlers, nil)
}
