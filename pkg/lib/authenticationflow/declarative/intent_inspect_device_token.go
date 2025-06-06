package declarative

import (
	"context"
	"errors"
	"net/http"

	authflow "github.com/authgear/authgear-server/pkg/lib/authenticationflow"
	"github.com/authgear/authgear-server/pkg/lib/authn/mfa"
)

func init() {
	authflow.RegisterIntent(&IntentInspectDeviceToken{})
}

type IntentInspectDeviceToken struct {
	UserID string `json:"user_id,omitempty"`
}

var _ authflow.Intent = &IntentInspectDeviceToken{}
var _ authflow.Milestone = &IntentInspectDeviceToken{}
var _ MilestoneFlowSelectAuthenticationMethod = &IntentInspectDeviceToken{}
var _ MilestoneFlowAuthenticate = &IntentInspectDeviceToken{}
var _ MilestoneDeviceTokenInspected = &IntentInspectDeviceToken{}

func (*IntentInspectDeviceToken) Kind() string {
	return "IntentInspectDeviceToken"
}

func (*IntentInspectDeviceToken) Milestone() {}
func (i *IntentInspectDeviceToken) MilestoneFlowSelectAuthenticationMethod(flows authflow.Flows) (MilestoneDidSelectAuthenticationMethod, authflow.Flows, bool) {
	return authflow.FindMilestoneInCurrentFlow[MilestoneDidSelectAuthenticationMethod](flows)
}
func (i *IntentInspectDeviceToken) MilestoneFlowAuthenticate(flows authflow.Flows) (MilestoneDidAuthenticate, authflow.Flows, bool) {
	return authflow.FindMilestoneInCurrentFlow[MilestoneDidAuthenticate](flows)
}
func (*IntentInspectDeviceToken) MilestoneDeviceTokenInspected() {}

func (*IntentInspectDeviceToken) CanReactTo(ctx context.Context, deps *authflow.Dependencies, flows authflow.Flows) (authflow.InputSchema, error) {
	if len(flows.Nearest.Nodes) == 0 {
		return nil, nil
	}

	return nil, authflow.ErrEOF
}

func (i *IntentInspectDeviceToken) ReactTo(ctx context.Context, deps *authflow.Dependencies, flows authflow.Flows, input authflow.Input) (authflow.ReactToResult, error) {
	if len(flows.Nearest.Nodes) == 0 {
		deviceTokenCookie, err := deps.Cookies.GetCookie(deps.HTTPRequest, deps.MFADeviceTokenCookie.Def)
		// End this flow if there is no cookie.
		if errors.Is(err, http.ErrNoCookie) {
			return authflow.NewNodeSimple(&NodeSentinel{}), nil
		} else if err != nil {
			return nil, err
		}

		deviceToken := deviceTokenCookie.Value

		err = deps.MFA.VerifyDeviceToken(ctx, i.UserID, deviceToken)
		if errors.Is(err, mfa.ErrDeviceTokenNotFound) {
			deviceTokenCookie = deps.Cookies.ClearCookie(deps.MFADeviceTokenCookie.Def)
			return authflow.NewNodeSimple(&NodeDoClearDeviceTokenCookie{
				Cookie: deviceTokenCookie,
			}), nil
		} else if err != nil {
			return nil, err
		}

		return authflow.NewNodeSimple(&NodeDoUseDeviceToken{}), nil
	}

	return nil, authflow.ErrIncompatibleInput
}
