package declarative

import (
	"context"
	"fmt"

	"github.com/authgear/oauthrelyingparty/pkg/api/oauthrelyingparty"
	"github.com/iawaknahc/jsonschema/pkg/jsonpointer"

	"github.com/authgear/authgear-server/pkg/api"
	"github.com/authgear/authgear-server/pkg/api/apierrors"
	"github.com/authgear/authgear-server/pkg/api/model"
	authflow "github.com/authgear/authgear-server/pkg/lib/authenticationflow"
	"github.com/authgear/authgear-server/pkg/lib/authn/authenticator"
	"github.com/authgear/authgear-server/pkg/lib/authn/identity"
	"github.com/authgear/authgear-server/pkg/lib/authn/otp"
	"github.com/authgear/authgear-server/pkg/lib/config"
	"github.com/authgear/authgear-server/pkg/lib/infra/mail"
	"github.com/authgear/authgear-server/pkg/lib/oauthrelyingparty/wechat"
	"github.com/authgear/authgear-server/pkg/lib/uiparam"
	"github.com/authgear/authgear-server/pkg/util/errorutil"
	"github.com/authgear/authgear-server/pkg/util/phone"
	"github.com/authgear/authgear-server/pkg/util/stringutil"
	"github.com/authgear/authgear-server/pkg/util/uuid"
)

func authenticatorIsDefault(ctx context.Context, deps *authflow.Dependencies, userID string, authenticatorKind model.AuthenticatorKind) (isDefault bool, err error) {
	ais, err := deps.Authenticators.List(ctx,
		userID,
		authenticator.KeepKind(authenticatorKind),
		authenticator.KeepDefault,
	)
	if err != nil {
		return
	}

	isDefault = len(ais) == 0
	return
}

func flowRootObject(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	switch flowReference.Type {
	case authflow.FlowTypeSignup:
		return flowRootObjectForSignupFlow(deps, flowReference)
	case authflow.FlowTypePromote:
		return flowRootObjectForPromoteFlow(deps, flowReference)
	case authflow.FlowTypeLogin:
		return flowRootObjectForLoginFlow(deps, flowReference)
	case authflow.FlowTypeSignupLogin:
		return flowRootObjectForSignupLoginFlow(deps, flowReference)
	case authflow.FlowTypeReauth:
		return flowRootObjectForReauthFlow(deps, flowReference)
	case authflow.FlowTypeAccountRecovery:
		return flowRootObjectForAccountRecoveryFlow(deps, flowReference)
	default:
		panic(fmt.Errorf("unexpected flow type: %v", flowReference.Type))
	}
}

func flowRootObjectForSignupFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.SignupFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GenerateSignupFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func flowRootObjectForPromoteFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.PromoteFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GeneratePromoteFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func flowRootObjectForLoginFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.LoginFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GenerateLoginFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func flowRootObjectForSignupLoginFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.SignupLoginFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GenerateSignupLoginFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func flowRootObjectForReauthFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.ReauthFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GenerateReauthFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func flowRootObjectForAccountRecoveryFlow(deps *authflow.Dependencies, flowReference authflow.FlowReference) (config.AuthenticationFlowObject, error) {
	var root config.AuthenticationFlowObject

	for _, f := range deps.Config.AuthenticationFlow.AccountRecoveryFlows {
		f := f
		if f.Name == flowReference.Name {
			root = f
			break
		}
	}
	if root == nil && flowReference.Name == nameGeneratedFlow {
		root = GenerateAccountRecoveryFlowConfig(deps.Config)
	}

	if root == nil {
		return nil, ErrFlowNotFound
	}

	return root, nil
}

func findFlowRootObjectInFlow(deps *authflow.Dependencies, flows authflow.Flows) (config.AuthenticationFlowObject, error) {
	var nearestPublicFlow authflow.PublicFlow
	_ = authflow.TraverseIntentFromEndToRoot(func(intent authflow.Intent) error {
		if nearestPublicFlow != nil {
			return nil
		}
		if publicFlow, ok := intent.(authflow.PublicFlow); ok {
			nearestPublicFlow = publicFlow
		}
		return nil
	}, flows.Root)
	if nearestPublicFlow == nil {
		panic("failed to find flow root object: no public flow available")
	}
	return nearestPublicFlow.FlowRootObject(deps)
}

// nolint: gocognit
func getAuthenticationOptionsForLogin(ctx context.Context, deps *authflow.Dependencies, flows authflow.Flows, userID string, step *config.AuthenticationFlowLoginFlowStep) (
	options []AuthenticateOption,
	deviceTokenEnabled bool,
	err error,
) {
	options = []AuthenticateOption{}

	identities, err := deps.Identities.ListByUser(ctx, userID)
	if err != nil {
		return nil, false, err
	}

	authenticators, err := deps.Authenticators.List(ctx, userID)
	if err != nil {
		return nil, false, err
	}

	secondaryAuthenticators := authenticator.ApplyFilters(authenticators, authenticator.KeepKind(model.AuthenticatorKindSecondary))
	userRecoveryCodes, err := deps.MFA.ListRecoveryCodes(ctx, userID)
	if err != nil {
		return nil, false, err
	}
	passkeyAuthenticators := authenticator.ApplyFilters(
		authenticators,
		authenticator.KeepType(model.AuthenticatorTypePasskey),
	)
	secondaryPasswordAuthenticators := authenticator.ApplyFilters(
		secondaryAuthenticators,
		authenticator.KeepType(model.AuthenticatorTypePassword),
	)
	secondaryTOTPAuthenticators := authenticator.ApplyFilters(
		secondaryAuthenticators,
		authenticator.KeepType(model.AuthenticatorTypeTOTP),
	)

	userHasRecoveryCode := len(userRecoveryCodes) > 0
	userHasPasskey := len(passkeyAuthenticators) > 0
	userHasSecondaryPassword := len(secondaryPasswordAuthenticators) > 0
	userHasTOTP := len(secondaryTOTPAuthenticators) > 0

	findIdentity := func(targetStepName string) (*identity.Info, error) {
		// Find the target step from the root.
		targetStepFlow, err := authflow.FindTargetStep(flows.Root, targetStepName)
		if err != nil {
			return nil, err
		}

		target, ok := targetStepFlow.Intent.(IntentLoginFlowStepAuthenticateTarget)
		if !ok {
			return nil, InvalidTargetStep.NewWithInfo("invalid target_step", apierrors.Details{
				"target_step": targetStepName,
			})
		}

		info := target.GetIdentity(ctx, deps, flows.Replace(targetStepFlow))

		return info, nil
	}

	useAuthenticationOptionAddRecoveryCodes := func(options []AuthenticateOption, userHasRecoveryCode bool, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		if userHasRecoveryCode {
			options = append(options, NewAuthenticateOptionRecoveryCode(botProtection,
				deps.Config.BotProtection))
		}

		return options
	}

	useAuthenticationOptionAddPrimaryPassword := func(options []AuthenticateOption, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		// We always add primary_password even though the end-user does not actually has one.
		// Showing this branch is necessary to convince the frontend to show a primary password page, where
		// the end-user can trigger account recovery flow and create a new password.
		options = append(options, NewAuthenticateOptionPassword(
			config.AuthenticationFlowAuthenticationPrimaryPassword,
			botProtection,
			deps.Config.BotProtection,
		),
		)
		return options
	}

	useAuthenticationOptionAddSecondaryPassword := func(options []AuthenticateOption, userHasSecondaryPassword bool, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		// We only add secondary_password if user has one,
		// because user can do nothing if user didn't setup a secondary password

		if userHasSecondaryPassword {
			options = append(options, NewAuthenticateOptionPassword(
				config.AuthenticationFlowAuthenticationSecondaryPassword,
				botProtection,
				deps.Config.BotProtection,
			))
		}

		return options
	}

	useAuthenticationOptionAddTOTP := func(options []AuthenticateOption, userHasTOTP bool, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		// We only add totp if user has one,
		// because user can do nothing if user didn't setup a totp

		if userHasTOTP {
			options = append(options, NewAuthenticateOptionTOTP(botProtection,
				deps.Config.BotProtection))
		}

		return options
	}

	useAuthenticationOptionAddPasskey := func(options []AuthenticateOption, deps *authflow.Dependencies, userHasPasskey bool, userID string, botProtection *config.AuthenticationFlowBotProtection) ([]AuthenticateOption, error) {
		// We only add passkey if user has one
		if userHasPasskey {
			requestOptions, err := deps.PasskeyRequestOptionsService.MakeModalRequestOptionsWithUser(ctx, userID)
			if err != nil {
				return nil, err
			}

			options = append(options, NewAuthenticateOptionPasskey(requestOptions, botProtection,
				deps.Config.BotProtection))
			return options, nil
		}

		return options, nil
	}

	useAuthenticationOptionAddPrimaryOOBOTPOfIdentity := func(options []AuthenticateOption, deps *authflow.Dependencies, authentication config.AuthenticationFlowAuthentication, info *identity.Info, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		option, ok := NewAuthenticateOptionOOBOTPFromIdentity(deps.Config.Authenticator.OOB, info, botProtection,
			deps.Config.BotProtection)
		if !ok {
			return options
		}

		if option.Authentication != authentication {
			return options
		}

		options = append(options, *option)
		return options
	}

	useAuthenticationOptionAddPrimaryOOBOTPOfAllIdentities := func(options []AuthenticateOption, deps *authflow.Dependencies, authentication config.AuthenticationFlowAuthentication, infos []*identity.Info, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		for _, info := range infos {
			options = useAuthenticationOptionAddPrimaryOOBOTPOfIdentity(options, deps, authentication, info, botProtection)
		}

		return options
	}

	useAuthenticationOptionAddSecondaryOOBOTP := func(options []AuthenticateOption, deps *authflow.Dependencies, authentication config.AuthenticationFlowAuthentication, infos []*authenticator.Info, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		for _, info := range infos {
			if option, ok := NewAuthenticateOptionOOBOTPFromAuthenticator(deps.Config.Authenticator.OOB, info, botProtection,
				deps.Config.BotProtection); ok {
				if option.Authentication == authentication {
					options = append(options, *option)
				}
			}
		}
		return options
	}

	for _, branch := range step.OneOf {
		switch branch.Authentication {
		case config.AuthenticationFlowAuthenticationDeviceToken:
			if len(secondaryAuthenticators) > 0 {
				deviceTokenEnabled = true
			}
		case config.AuthenticationFlowAuthenticationRecoveryCode:
			options = useAuthenticationOptionAddRecoveryCodes(options, userHasRecoveryCode, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryPassword:
			options = useAuthenticationOptionAddPrimaryPassword(options, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryPasskey:
			options, err = useAuthenticationOptionAddPasskey(options, deps, userHasPasskey, userID, branch.BotProtection)
			if err != nil {
				return nil, false, err
			}
		case config.AuthenticationFlowAuthenticationSecondaryPassword:
			options = useAuthenticationOptionAddSecondaryPassword(options, userHasSecondaryPassword, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationSecondaryTOTP:
			options = useAuthenticationOptionAddTOTP(options, userHasTOTP, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryOOBOTPEmail:
			fallthrough
		case config.AuthenticationFlowAuthenticationPrimaryOOBOTPSMS:
			if targetStepName := branch.TargetStep; targetStepName != "" {
				info, err := findIdentity(targetStepName)
				if err != nil {
					return nil, false, err
				}

				options = useAuthenticationOptionAddPrimaryOOBOTPOfIdentity(options, deps, branch.Authentication, info, branch.BotProtection)
			} else {
				options = useAuthenticationOptionAddPrimaryOOBOTPOfAllIdentities(options, deps, branch.Authentication, identities, branch.BotProtection)
			}
		case config.AuthenticationFlowAuthenticationSecondaryOOBOTPEmail:
			fallthrough
		case config.AuthenticationFlowAuthenticationSecondaryOOBOTPSMS:
			options = useAuthenticationOptionAddSecondaryOOBOTP(options, deps, branch.Authentication, authenticators, branch.BotProtection)
		}
	}

	return options, deviceTokenEnabled, nil
}

// nolint:gocognit
func getAuthenticationOptionsForReauth(ctx context.Context, deps *authflow.Dependencies, flows authflow.Flows, userID string, step *config.AuthenticationFlowReauthFlowStep) ([]AuthenticateOption, error) {
	options := []AuthenticateOption{}

	identities, err := deps.Identities.ListByUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	authenticators, err := deps.Authenticators.List(ctx, userID)
	if err != nil {
		return nil, err
	}

	checkHasAuthenticator := func(kind model.AuthenticatorKind, typ model.AuthenticatorType) bool {
		as := authenticator.ApplyFilters(
			authenticators,
			authenticator.KeepKind(kind),
			authenticator.KeepType(typ),
		)
		return len(as) > 0
	}

	useAuthenticationOptionAddPrimaryPassword := func(options []AuthenticateOption, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		if checkHasAuthenticator(
			model.AuthenticatorKindPrimary,
			model.AuthenticatorTypePassword,
		) {
			options = append(options, NewAuthenticateOptionPassword(
				config.AuthenticationFlowAuthenticationPrimaryPassword, botProtection, deps.Config.BotProtection),
			)
		}
		return options
	}

	useAuthenticationOptionAddSecondaryPassword := func(options []AuthenticateOption, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		if checkHasAuthenticator(
			model.AuthenticatorKindSecondary,
			model.AuthenticatorTypePassword,
		) {
			options = append(options, NewAuthenticateOptionPassword(
				config.AuthenticationFlowAuthenticationSecondaryPassword, botProtection, deps.Config.BotProtection),
			)
		}
		return options
	}

	useAuthenticationOptionAddTOTP := func(options []AuthenticateOption, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		if checkHasAuthenticator(
			model.AuthenticatorKindSecondary,
			model.AuthenticatorTypeTOTP,
		) {
			options = append(options, NewAuthenticateOptionTOTP(botProtection, deps.Config.BotProtection))
		}
		return options
	}

	useAuthenticationOptionAddPasskey := func(options []AuthenticateOption, botProtection *config.AuthenticationFlowBotProtection) ([]AuthenticateOption, error) {
		if checkHasAuthenticator(
			model.AuthenticatorKindPrimary,
			model.AuthenticatorTypePasskey,
		) {
			requestOptions, err := deps.PasskeyRequestOptionsService.MakeModalRequestOptionsWithUser(ctx, userID)
			if err != nil {
				return nil, err
			}

			options = append(options, NewAuthenticateOptionPasskey(requestOptions, botProtection, deps.Config.BotProtection))
		}

		return options, nil
	}

	useAuthenticationOptionAddPrimaryOOBOTP := func(options []AuthenticateOption, authentication config.AuthenticationFlowAuthentication, typ model.AuthenticatorType, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		for _, info := range identities {
			option, ok := NewAuthenticateOptionOOBOTPFromIdentity(deps.Config.Authenticator.OOB, info, botProtection, deps.Config.BotProtection)
			if ok && option.Authentication == authentication {
				options = append(options, *option)
			}
		}
		return options
	}

	useAuthenticationOptionAddSecondaryOOBOTP := func(options []AuthenticateOption, authentication config.AuthenticationFlowAuthentication, typ model.AuthenticatorType, botProtection *config.AuthenticationFlowBotProtection) []AuthenticateOption {
		as := authenticator.ApplyFilters(
			authenticators,
			authenticator.KeepKind(model.AuthenticatorKindSecondary),
			authenticator.KeepType(typ),
		)
		for _, info := range as {
			option, ok := NewAuthenticateOptionOOBOTPFromAuthenticator(deps.Config.Authenticator.OOB, info, botProtection, deps.Config.BotProtection)
			if ok && option.Authentication == authentication {
				options = append(options, *option)
			}
		}
		return options
	}

	for _, branch := range step.OneOf {
		switch branch.Authentication {
		case config.AuthenticationFlowAuthenticationPrimaryPassword:
			options = useAuthenticationOptionAddPrimaryPassword(options, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryPasskey:
			options, err = useAuthenticationOptionAddPasskey(options, branch.BotProtection)
			if err != nil {
				return nil, err
			}
		case config.AuthenticationFlowAuthenticationSecondaryPassword:
			options = useAuthenticationOptionAddSecondaryPassword(options, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationSecondaryTOTP:
			options = useAuthenticationOptionAddTOTP(options, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryOOBOTPEmail:
			options = useAuthenticationOptionAddPrimaryOOBOTP(options, branch.Authentication, model.AuthenticatorTypeOOBEmail, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationPrimaryOOBOTPSMS:
			options = useAuthenticationOptionAddPrimaryOOBOTP(options, branch.Authentication, model.AuthenticatorTypeOOBSMS, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationSecondaryOOBOTPEmail:
			options = useAuthenticationOptionAddSecondaryOOBOTP(options, branch.Authentication, model.AuthenticatorTypeOOBEmail, branch.BotProtection)
		case config.AuthenticationFlowAuthenticationSecondaryOOBOTPSMS:
			options = useAuthenticationOptionAddSecondaryOOBOTP(options, branch.Authentication, model.AuthenticatorTypeOOBSMS, branch.BotProtection)
		}
	}

	return options, nil
}

func identityFillDetailsMany(err error, spec *identity.Spec, existingSpecs []*identity.Spec) error {
	details := errorutil.Details{}

	if spec != nil {
		details["IdentityTypeIncoming"] = apierrors.APIErrorDetail.Value(spec.Type)
		switch spec.Type {
		case model.IdentityTypeLoginID:
			details["LoginIDTypeIncoming"] = apierrors.APIErrorDetail.Value(spec.LoginID.Type)
		case model.IdentityTypeOAuth:
			details["OAuthProviderTypeIncoming"] = apierrors.APIErrorDetail.Value(spec.OAuth.ProviderID.Type)
		}
	}

	if len(existingSpecs) > 0 {
		// Fill IdentityTypeExisting, LoginIDTypeExisting, OAuthProviderTypeExisting for backward compatibility
		// Use first spec to fill the fields
		firstExistingSpec := existingSpecs[0]
		details["IdentityTypeExisting"] = apierrors.APIErrorDetail.Value(firstExistingSpec.Type)
		switch firstExistingSpec.Type {
		case model.IdentityTypeLoginID:
			details["LoginIDTypeExisting"] = apierrors.APIErrorDetail.Value(firstExistingSpec.LoginID.Type)
		case model.IdentityTypeOAuth:
			details["OAuthProviderTypeExisting"] = apierrors.APIErrorDetail.Value(firstExistingSpec.OAuth.ProviderID.Type)
		}

		specDetails := []map[string]interface{}{}
		for _, existingSpec := range existingSpecs {
			existingSpec := existingSpec
			thisDetail := map[string]interface{}{}
			thisDetail["IdentityType"] = existingSpec.Type
			switch existingSpec.Type {
			case model.IdentityTypeLoginID:
				thisDetail["LoginIDType"] = existingSpec.LoginID.Type
			case model.IdentityTypeOAuth:
				thisDetail["OAuthProviderType"] = existingSpec.OAuth.ProviderID.Type
			}
			specDetails = append(specDetails, thisDetail)
		}
		details["ExistingIdentities"] = apierrors.APIErrorDetail.Value(specDetails)
	}

	return errorutil.WithDetails(err, details)
}

func identityFillDetails(err error, spec *identity.Spec, existingSpec *identity.Spec) error {
	existings := []*identity.Spec{}
	if existingSpec != nil {
		existings = append(existings, existingSpec)
	}

	return identityFillDetailsMany(err, spec, existings)
}

func getChannels(claimName model.ClaimName, oobConfig *config.AuthenticatorOOBConfig) []model.AuthenticatorOOBChannel {
	channels := []model.AuthenticatorOOBChannel{}

	switch claimName {
	case model.ClaimEmail:
		channels = append(channels, model.AuthenticatorOOBChannelEmail)
	case model.ClaimPhoneNumber:
		switch oobConfig.SMS.PhoneOTPMode {
		case config.AuthenticatorPhoneOTPModeSMSOnly:
			channels = append(channels, model.AuthenticatorOOBChannelSMS)
		case config.AuthenticatorPhoneOTPModeWhatsappOnly:
			channels = append(channels, model.AuthenticatorOOBChannelWhatsapp)
		case config.AuthenticatorPhoneOTPModeWhatsappSMS:
			channels = append(channels, model.AuthenticatorOOBChannelWhatsapp)
			channels = append(channels, model.AuthenticatorOOBChannelSMS)
		}
	}

	return channels
}

func getOTPForm(purpose otp.Purpose, claimName model.ClaimName, cfg *config.AuthenticatorOOBEmailConfig) otp.Form {
	switch purpose {
	case otp.PurposeVerification:
		// Always use code.
		return otp.FormCode
	case otp.PurposeForgotPassword:
		// Always use link.
		return otp.FormLink
	case otp.PurposeOOBOTP:
		switch claimName {
		case model.ClaimEmail:
			if cfg.EmailOTPMode == config.AuthenticatorEmailOTPModeLoginLinkOnly {
				return otp.FormLink
			}
			return otp.FormCode
		case model.ClaimPhoneNumber:
			return otp.FormCode
		default:
			panic(fmt.Errorf("unexpected claim name: %v", claimName))
		}
	default:
		panic(fmt.Errorf("unexpected purpose: %v", purpose))
	}
}

func newIdentityInfo(ctx context.Context, deps *authflow.Dependencies, newUserID string, spec *identity.Spec) (newIden *identity.Info, err error) {
	// FIXME(authflow): allow bypassing email blocklist for Admin API.
	info, err := deps.Identities.New(ctx, newUserID, spec, identity.NewIdentityOptions{})
	if err != nil {
		return nil, err
	}

	_, err = deps.Identities.CheckDuplicatedByUniqueKey(ctx, info)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func findExactOneIdentityInfo(ctx context.Context, deps *authflow.Dependencies, spec *identity.Spec) (*identity.Info, error) {
	bucketSpec := AccountEnumerationPerIPRateLimitBucketSpec(
		deps.Config.Authentication,
		string(deps.RemoteIP),
	)

	reservation, failedReservation, err := deps.RateLimiter.Reserve(ctx, bucketSpec)
	if err != nil {
		return nil, err
	}
	if err := failedReservation.Error(); err != nil {
		return nil, err
	}
	defer deps.RateLimiter.Cancel(ctx, reservation)

	exactMatch, otherMatches, err := deps.Identities.SearchBySpec(ctx, spec)
	if err != nil {
		return nil, err
	}

	if exactMatch == nil {
		// Prevent canceling the reservation if exact match is not found.
		reservation.PreventCancel()

		var otherSpec *identity.Spec
		if len(otherMatches) > 0 {
			s := otherMatches[0].ToSpec()
			otherSpec = &s
		}
		return nil, identityFillDetails(api.ErrUserNotFound, spec, otherSpec)
	}

	return exactMatch, nil
}

type HandleOAuthAuthorizationResponseOptions struct {
	Alias       string
	RedirectURI string
}

func handleOAuthAuthorizationResponse(ctx context.Context, deps *authflow.Dependencies, opts HandleOAuthAuthorizationResponseOptions, inputOAuth inputTakeOAuthAuthorizationResponse) (*identity.Spec, error) {
	providerConfig, err := deps.OAuthProviderFactory.GetProviderConfig(opts.Alias)
	if err != nil {
		return nil, err
	}

	// TODO(authflow): support nonce but do not save nonce in cookies.
	// Nonce in the current implementation is stored in cookies.
	// In the Authentication Flow API, cookies are not sent in Safari in third-party context.
	emptyNonce := ""
	authInfo, err := deps.OAuthProviderFactory.GetUserProfile(ctx,
		opts.Alias,
		oauthrelyingparty.GetUserProfileOptions{
			Query:       inputOAuth.GetQuery(),
			RedirectURI: opts.RedirectURI,
			Nonce:       emptyNonce,
		},
	)
	if err != nil {
		return nil, err
	}

	providerID := providerConfig.ProviderID()
	identitySpec := &identity.Spec{
		Type: model.IdentityTypeOAuth,
		OAuth: &identity.OAuthSpec{
			ProviderID:     providerID,
			SubjectID:      authInfo.ProviderUserID,
			RawProfile:     authInfo.ProviderRawProfile,
			StandardClaims: authInfo.StandardAttributes,
		},
	}

	return identitySpec, nil
}

type GetOAuthDataOptions struct {
	RedirectURI  string
	Alias        string
	ResponseMode string
}

func getOAuthData(ctx context.Context, deps *authflow.Dependencies, opts GetOAuthDataOptions) (data OAuthData, err error) {
	providerConfig, err := deps.OAuthProviderFactory.GetProviderConfig(opts.Alias)
	if err != nil {
		return
	}

	uiParam := uiparam.GetUIParam(ctx)

	param := oauthrelyingparty.GetAuthorizationURLOptions{
		RedirectURI:  opts.RedirectURI,
		ResponseMode: opts.ResponseMode,
		Prompt:       uiParam.Prompt,
	}

	authorizationURL, err := deps.OAuthProviderFactory.GetAuthorizationURL(ctx, opts.Alias, param)
	if err != nil {
		return
	}

	data = NewOAuthData(OAuthData{
		Alias:                 opts.Alias,
		OAuthProviderType:     providerConfig.Type(),
		OAuthAuthorizationURL: authorizationURL,
		WechatAppType:         wechat.ProviderConfig(providerConfig).AppType(),
	})
	return
}

func getMaskedOTPTarget(claimName model.ClaimName, claimValue string) string {
	switch claimName {
	case model.ClaimEmail:
		return mail.MaskAddress(claimValue)
	case model.ClaimPhoneNumber:
		return phone.Mask(claimValue)
	default:
		panic(fmt.Errorf("unexpected claim name: %v", claimName))
	}
}

func getOOBAuthenticatorType(authentication config.AuthenticationFlowAuthentication) model.AuthenticatorType {
	switch authentication {
	case config.AuthenticationFlowAuthenticationPrimaryOOBOTPEmail:
		return model.AuthenticatorTypeOOBEmail
	case config.AuthenticationFlowAuthenticationPrimaryOOBOTPSMS:
		return model.AuthenticatorTypeOOBSMS
	case config.AuthenticationFlowAuthenticationSecondaryOOBOTPEmail:
		return model.AuthenticatorTypeOOBEmail
	case config.AuthenticationFlowAuthenticationSecondaryOOBOTPSMS:
		return model.AuthenticatorTypeOOBSMS

	default:
		panic(fmt.Errorf("unexpected authentication method: %v", authentication))
	}
}

func createAuthenticatorSpec(ctx context.Context, deps *authflow.Dependencies, userID string, authentication config.AuthenticationFlowAuthentication, target string) (*authenticator.Spec, error) {
	spec := &authenticator.Spec{
		UserID: userID,
		OOBOTP: &authenticator.OOBOTPSpec{},
	}

	spec.Type = getOOBAuthenticatorType(authentication)

	switch authentication {
	case config.AuthenticationFlowAuthenticationPrimaryOOBOTPEmail:
		spec.Kind = model.AuthenticatorKindPrimary
		spec.OOBOTP.Email = target

	case config.AuthenticationFlowAuthenticationPrimaryOOBOTPSMS:
		spec.Kind = model.AuthenticatorKindPrimary
		spec.OOBOTP.Phone = target

	case config.AuthenticationFlowAuthenticationSecondaryOOBOTPEmail:
		spec.Kind = model.AuthenticatorKindSecondary
		spec.OOBOTP.Email = target

	case config.AuthenticationFlowAuthenticationSecondaryOOBOTPSMS:
		spec.Kind = model.AuthenticatorKindSecondary
		spec.OOBOTP.Phone = target

	default:
		panic(fmt.Errorf("unexpected authentication method: %v", authentication))
	}

	isDefault, err := authenticatorIsDefault(ctx, deps, userID, spec.Kind)
	if err != nil {
		return nil, err
	}
	spec.IsDefault = isDefault

	return spec, nil
}

func createAuthenticator(ctx context.Context, deps *authflow.Dependencies, userID string, authentication config.AuthenticationFlowAuthentication, target string) (*authenticator.Info, error) {
	spec, err := createAuthenticatorSpec(ctx, deps, userID, authentication, target)
	if err != nil {
		return nil, err
	}

	authenticatorID := uuid.New()
	info, err := deps.Authenticators.NewWithAuthenticatorID(ctx, authenticatorID, spec)
	if err != nil {
		return nil, err
	}

	return info, nil
}

func isNodeRestored(nodePointer jsonpointer.T, restoreTo jsonpointer.T) bool {
	return !authflow.JSONPointerSubtract(nodePointer, restoreTo).More()
}

func makeLoginIDSpec(identification config.AuthenticationFlowIdentification, userInput stringutil.UserInputString) *identity.Spec {
	spec := &identity.Spec{
		Type: model.IdentityTypeLoginID,
		LoginID: &identity.LoginIDSpec{
			Value: userInput,
		},
	}
	switch identification {
	case config.AuthenticationFlowIdentificationEmail:
		spec.LoginID.Type = model.LoginIDKeyTypeEmail
		spec.LoginID.Key = string(spec.LoginID.Type)
	case config.AuthenticationFlowIdentificationPhone:
		spec.LoginID.Type = model.LoginIDKeyTypePhone
		spec.LoginID.Key = string(spec.LoginID.Type)
	case config.AuthenticationFlowIdentificationUsername:
		spec.LoginID.Type = model.LoginIDKeyTypeUsername
		spec.LoginID.Key = string(spec.LoginID.Type)
	default:
		panic(fmt.Errorf("unexpected identification method: %v", identification))
	}

	return spec
}
