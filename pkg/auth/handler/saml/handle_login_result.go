package saml

import (
	"context"
	"errors"

	"github.com/authgear/authgear-server/pkg/lib/authn/authenticationinfo"
	"github.com/authgear/authgear-server/pkg/lib/infra/db/appdb"
	"github.com/authgear/authgear-server/pkg/lib/saml/samlprotocol"
	"github.com/authgear/authgear-server/pkg/lib/saml/samlsession"
	"github.com/authgear/authgear-server/pkg/util/clock"
)

type LoginResultHandler struct {
	Clock       clock.Clock
	Database    *appdb.Handle
	SAMLService HandlerSAMLService
}

func (h *LoginResultHandler) handleLoginResult(
	ctx context.Context,
	authInfo *authenticationinfo.T,
	samlSessionEntry *samlsession.SAMLSessionEntry,
) (response samlprotocol.Respondable, err error) {
	now := h.Clock.NowUTC()
	callbackURL := samlSessionEntry.CallbackURL

	err = h.Database.WithTx(ctx, func(ctx context.Context) error {
		authnRequest, _ := samlSessionEntry.AuthnRequest()

		resp, err := h.SAMLService.IssueLoginSuccessResponse(
			ctx,
			callbackURL,
			samlSessionEntry.ServiceProviderID,
			*authInfo,
			authnRequest,
		)
		if err != nil {
			return err
		}
		response = resp
		return nil
	})
	if err != nil {
		var missingNameIDErr *samlprotocol.MissingNameIDError
		if errors.As(err, &missingNameIDErr) {
			errResult := NewSAMLErrorResult(err,
				samlprotocol.NewServerErrorResponse(
					now,
					h.SAMLService.IdpEntityID(),
					"missing nameid",
					missingNameIDErr.GetDetailElements(),
				),
			)
			return nil, errResult
		}

		var unsupportedAttributeTypeErr *samlprotocol.UnsupportedAttributeTypeError
		if errors.As(err, &unsupportedAttributeTypeErr) {
			errResult := NewSAMLErrorResult(err,
				samlprotocol.NewServerErrorResponse(
					now,
					h.SAMLService.IdpEntityID(),
					"unsupported attribute type",
					unsupportedAttributeTypeErr.GetDetailElements(),
				),
			)
			return nil, errResult
		}

		return nil, err
	}

	return response, nil
}
