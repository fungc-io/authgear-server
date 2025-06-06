package authflowv2

import (
	"context"
	"net/http"

	handlerwebapp "github.com/authgear/authgear-server/pkg/auth/handler/webapp"
	"github.com/authgear/authgear-server/pkg/auth/handler/webapp/viewmodels"
	"github.com/authgear/authgear-server/pkg/auth/webapp"
	"github.com/authgear/authgear-server/pkg/lib/authenticationflow/declarative"
	"github.com/authgear/authgear-server/pkg/util/httproute"
	"github.com/authgear/authgear-server/pkg/util/template"
)

var TemplateWebAuthflowVerifyBotProtectionHTML = template.RegisterHTML(
	"web/authflowv2/verify_bot_protection.html",
	handlerwebapp.Components...,
)

func ConfigureAuthflowV2VerifyBotProtectionRoute(route httproute.Route) httproute.Route {
	return route.
		WithMethods("OPTIONS", "POST", "GET").
		WithPathPattern(AuthflowV2RouteVerifyBotProtection)
}

type AuthflowV2VerifyBotProtectionHandler struct {
	Controller    *handlerwebapp.AuthflowController
	BaseViewModel *viewmodels.BaseViewModeler
	Renderer      handlerwebapp.Renderer
}

func (h *AuthflowV2VerifyBotProtectionHandler) GetData(w http.ResponseWriter, r *http.Request, screen *webapp.AuthflowScreenWithFlowResponse) (map[string]interface{}, error) {
	data := make(map[string]interface{})
	baseViewModel := h.BaseViewModel.ViewModelForAuthFlow(r, w)
	viewmodels.Embed(data, baseViewModel)
	return data, nil
}

func (h *AuthflowV2VerifyBotProtectionHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var handlers handlerwebapp.AuthflowControllerHandlers
	handlers.Get(func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		data, err := h.GetData(w, r, screen)
		if err != nil {
			return err
		}

		h.Renderer.RenderHTML(w, r, TemplateWebAuthflowVerifyBotProtectionHTML, data)
		return nil
	})

	handlers.PostAction("", func(ctx context.Context, s *webapp.Session, screen *webapp.AuthflowScreenWithFlowResponse) error {
		err := handlerwebapp.ValidateBotProtectionInput(ctx, r.Form)
		if err != nil {
			return err
		}

		index := *screen.Screen.TakenBranchIndex
		channel := screen.Screen.TakenChannel
		data := screen.StateTokenFlowResponse.Action.Data.(declarative.StepAuthenticateData)
		option := data.Options[index]

		input := map[string]interface{}{
			"authentication": option.Authentication,
			"index":          index,
		}

		// Only set channel if not empty because this screen might be used by flows other than
		if channel != "" {
			input["channel"] = channel
		}

		handlerwebapp.InsertBotProtection(r.Form, input)

		// advance with previous screen inherit
		result, err := h.Controller.AdvanceWithInput(ctx, r, s, screen, input, &handlerwebapp.AdvanceOptions{
			InheritTakenBranchState: true,
		})
		if err != nil {
			return err
		}
		result.WriteResponse(w, r)
		return nil
	})

	h.Controller.HandleStep(r.Context(), w, r, &handlers)
}
