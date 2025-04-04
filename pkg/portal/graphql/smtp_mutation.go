package graphql

import (
	"github.com/graphql-go/graphql"

	relay "github.com/authgear/authgear-server/pkg/graphqlgo/relay"

	"github.com/authgear/authgear-server/pkg/api/apierrors"
	"github.com/authgear/authgear-server/pkg/portal/smtp"
)

var sendTestSMTPConfigurationEmailInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "sendTestSMTPConfigurationEmailInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"appID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.ID),
			Description: "App ID to test.",
		},
		"to": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "The recipient email address.",
		},
		"smtpHost": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "SMTP Host.",
		},
		"smtpPort": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.Int),
			Description: "SMTP Port.",
		},
		"smtpUsername": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "SMTP Username.",
		},
		"smtpPassword": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "SMTP Password.",
		},
		"smtpSender": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "SMTP Sender.",
		},
	},
})

var _ = registerMutationField(
	"sendTestSMTPConfigurationEmail",
	&graphql.Field{
		Description: "Send test STMP configuration email",
		Type:        graphql.Boolean,
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(sendTestSMTPConfigurationEmailInput),
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			input := p.Args["input"].(map[string]interface{})
			appNodeID := input["appID"].(string)
			to := input["to"].(string)
			smtpHost := input["smtpHost"].(string)
			smtpPort := input["smtpPort"].(int)
			smtpUsername := input["smtpUsername"].(string)
			smtpPassword := input["smtpPassword"].(string)
			smtpSender := input["smtpSender"].(string)

			resolvedNodeID := relay.FromGlobalID(appNodeID)
			if resolvedNodeID == nil || resolvedNodeID.Type != typeApp {
				return nil, apierrors.NewInvalid("invalid app ID")
			}
			appID := resolvedNodeID.ID

			ctx := p.Context
			gqlCtx := GQLContext(ctx)

			// Access control: collaborator.
			_, err := gqlCtx.AuthzService.CheckAccessOfViewer(ctx, appID)
			if err != nil {
				return nil, err
			}

			app, err := gqlCtx.AppService.Get(ctx, appID)
			if err != nil {
				return nil, err
			}

			err = gqlCtx.SMTPService.SendTestEmail(ctx, app, smtp.SendTestEmailOptions{
				To:           to,
				SMTPHost:     smtpHost,
				SMTPPort:     smtpPort,
				SMTPUsername: smtpUsername,
				SMTPPassword: smtpPassword,
				SMTPSender:   smtpSender,
			})
			if err != nil {
				return nil, err
			}

			return nil, nil
		},
	},
)
