package graphql

import (
	"context"
	"net/url"

	"github.com/graphql-go/graphql"
	"sigs.k8s.io/yaml"

	relay "github.com/authgear/authgear-server/pkg/graphqlgo/relay"

	"github.com/authgear/authgear-server/pkg/api/apierrors"
	"github.com/authgear/authgear-server/pkg/api/event/nonblocking"
	"github.com/authgear/authgear-server/pkg/lib/config/configsource"
	"github.com/authgear/authgear-server/pkg/portal/appresource"
	"github.com/authgear/authgear-server/pkg/portal/model"
	"github.com/authgear/authgear-server/pkg/portal/session"
	"github.com/authgear/authgear-server/pkg/util/graphqlutil"
)

var createDomainInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "CreateDomainInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"appID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.ID),
			Description: "Target app ID.",
		},
		"domain": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Domain name.",
		},
	},
})

var createDomainPayload = graphql.NewObject(graphql.ObjectConfig{
	Name: "CreateDomainPayload",
	Fields: graphql.Fields{
		"domain": &graphql.Field{Type: graphql.NewNonNull(domain)},
		"app":    &graphql.Field{Type: graphql.NewNonNull(nodeApp)},
	},
})

var _ = registerMutationField(
	"createDomain",
	&graphql.Field{
		Description: "Create domain for target app",
		Type:        graphql.NewNonNull(createDomainPayload),
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(createDomainInput),
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx := p.Context

			// Access Control: authenticated user.
			sessionInfo := session.GetValidSessionInfo(ctx)
			if sessionInfo == nil {
				return nil, Unauthenticated.New("only authenticated users can create domain")
			}

			input := p.Args["input"].(map[string]interface{})
			appNodeID := input["appID"].(string)
			domain := input["domain"].(string)

			resolvedNodeID := relay.FromGlobalID(appNodeID)
			if resolvedNodeID == nil || resolvedNodeID.Type != typeApp {
				return nil, apierrors.NewInvalid("invalid app ID")
			}
			appID := resolvedNodeID.ID

			gqlCtx := GQLContext(ctx)

			// Access Control: collaborator.
			_, err := gqlCtx.AuthzService.CheckAccessOfViewer(ctx, appID)
			if err != nil {
				return nil, err
			}

			app, err := gqlCtx.AppService.Get(ctx, appID)
			if err != nil {
				return nil, err
			}
			fc := app.Context.Config.FeatureConfig
			if fc.CustomDomain.Disabled {
				return nil, apierrors.NewInvalid("custom domain is not supported")
			}

			domainModel, err := gqlCtx.DomainService.CreateCustomDomain(ctx, appID, domain)
			if err != nil {
				return nil, err
			}

			err = gqlCtx.AuditService.Log(ctx, app, &nonblocking.ProjectDomainCreatedEventPayload{
				Domain:   domainModel.Domain,
				DomainID: domainModel.ID,
			})
			if err != nil {
				return nil, err
			}

			gqlCtx.Domains.Prime(domainModel.ID, domainModel)
			return graphqlutil.NewLazyValue(map[string]interface{}{
				"domain": gqlCtx.Domains.Load(ctx, domainModel.ID),
				"app":    gqlCtx.Apps.Load(ctx, appID),
			}).Value, nil
		},
	},
)

var deleteDomainInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "DeleteDomainInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"appID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.ID),
			Description: "Target app ID.",
		},
		"domainID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Domain ID.",
		},
	},
})

var deleteDomainPayload = graphql.NewObject(graphql.ObjectConfig{
	Name: "DeleteDomainPayload",
	Fields: graphql.Fields{
		"app": &graphql.Field{Type: graphql.NewNonNull(nodeApp)},
	},
})

var _ = registerMutationField(
	"deleteDomain",
	&graphql.Field{
		Description: "Delete domain of target app",
		Type:        graphql.NewNonNull(deleteDomainPayload),
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(deleteDomainInput),
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx := p.Context

			// Access Control: authenticated user.
			sessionInfo := session.GetValidSessionInfo(ctx)
			if sessionInfo == nil {
				return nil, Unauthenticated.New("only authenticated users can delete domain")
			}

			input := p.Args["input"].(map[string]interface{})
			appNodeID := input["appID"].(string)
			domainID := input["domainID"].(string)

			resolvedNodeID := relay.FromGlobalID(appNodeID)
			if resolvedNodeID == nil || resolvedNodeID.Type != typeApp {
				return nil, apierrors.NewInvalid("invalid app ID")
			}
			appID := resolvedNodeID.ID

			gqlCtx := GQLContext(ctx)

			// Access Control: collaborator.
			_, err := gqlCtx.AuthzService.CheckAccessOfViewer(ctx, appID)
			if err != nil {
				return nil, err
			}

			app, err := gqlCtx.AppService.Get(ctx, appID)
			if err != nil {
				return nil, err
			}

			domains, err := gqlCtx.DomainService.ListDomains(ctx, appID)
			if err != nil {
				return nil, err
			}

			var deletedDomain string
			var defaultDomain string
			err = gqlCtx.GlobalDatabase.WithTx(ctx, func(ctx context.Context) error {
				err = gqlCtx.DomainService.DeleteDomain(ctx, appID, domainID)
				if err != nil {
					return err
				}

				// Update public origin if matches the deleted domain.
				for _, d := range domains {
					if d.ID == domainID {
						deletedDomain = d.Domain
					} else if !d.IsCustom {
						defaultDomain = d.Domain
					}
				}
				if deletedDomain != "" && defaultDomain != "" {
					err = deleteDomainUpdatePublicOrigin(ctx, gqlCtx, app, deletedDomain, defaultDomain)
					if err != nil {
						return err
					}
				}

				return nil
			})

			err = gqlCtx.AuditService.Log(ctx, app, &nonblocking.ProjectDomainDeletedEventPayload{
				Domain:   deletedDomain,
				DomainID: domainID,
			})
			if err != nil {
				return nil, err
			}

			return graphqlutil.NewLazyValue(map[string]interface{}{
				"app": gqlCtx.Apps.Load(ctx, appID),
			}).Value, nil
		},
	},
)

func deleteDomainUpdatePublicOrigin(ctx context.Context, gqlCtx *Context, app *model.App, deletedDomain string, defaultDomain string) error {
	rawAppConf, _, err := gqlCtx.AppService.LoadRawAppConfig(ctx, app)
	if err != nil {
		return err
	}

	if rawAppConf.HTTP == nil || rawAppConf.HTTP.PublicOrigin == "" {
		return nil
	}

	u, err := url.Parse(rawAppConf.HTTP.PublicOrigin)
	if err != nil {
		// Ignore invalid public origin
		return nil
	}

	if u.Host != deletedDomain {
		// Ignore if public origin does not match deleted domain.
		return nil
	}

	// Replace public origin with default domain.
	u = &url.URL{
		Scheme: "https",
		Host:   defaultDomain,
	}
	rawAppConf.HTTP.PublicOrigin = u.String()

	data, err := yaml.Marshal(rawAppConf)
	if err != nil {
		return err
	}

	err = gqlCtx.AppService.UpdateResources0(ctx, app, []appresource.Update{{
		Path: configsource.AuthgearYAML,
		Data: data,
	}})
	if err != nil {
		return err
	}

	return nil
}

var verifyDomainInput = graphql.NewInputObject(graphql.InputObjectConfig{
	Name: "VerifyDomainInput",
	Fields: graphql.InputObjectConfigFieldMap{
		"appID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.ID),
			Description: "Target app ID.",
		},
		"domainID": &graphql.InputObjectFieldConfig{
			Type:        graphql.NewNonNull(graphql.String),
			Description: "Domain ID.",
		},
	},
})

var verifyDomainPayload = graphql.NewObject(graphql.ObjectConfig{
	Name: "VerifyDomainPayload",
	Fields: graphql.Fields{
		"domain": &graphql.Field{Type: graphql.NewNonNull(domain)},
		"app":    &graphql.Field{Type: graphql.NewNonNull(nodeApp)},
	},
})

var _ = registerMutationField(
	"verifyDomain",
	&graphql.Field{
		Description: "Request verification of a domain of target app",
		Type:        graphql.NewNonNull(verifyDomainPayload),
		Args: graphql.FieldConfigArgument{
			"input": &graphql.ArgumentConfig{
				Type: graphql.NewNonNull(verifyDomainInput),
			},
		},
		Resolve: func(p graphql.ResolveParams) (interface{}, error) {
			ctx := p.Context

			// Access Control: authenticated user.
			sessionInfo := session.GetValidSessionInfo(ctx)
			if sessionInfo == nil {
				return nil, Unauthenticated.New("only authenticated users can verify domain")
			}

			input := p.Args["input"].(map[string]interface{})
			appNodeID := input["appID"].(string)
			domainID := input["domainID"].(string)

			resolvedNodeID := relay.FromGlobalID(appNodeID)
			if resolvedNodeID == nil || resolvedNodeID.Type != typeApp {
				return nil, apierrors.NewInvalid("invalid app ID")
			}
			appID := resolvedNodeID.ID

			gqlCtx := GQLContext(ctx)

			// Access Control: collaborator.
			_, err := gqlCtx.AuthzService.CheckAccessOfViewer(ctx, appID)
			if err != nil {
				return nil, err
			}

			app, err := gqlCtx.AppService.Get(ctx, appID)
			if err != nil {
				return nil, err
			}

			domain, err := gqlCtx.DomainService.VerifyDomain(ctx, appID, domainID)
			if err != nil {
				return nil, err
			}

			gqlCtx.Domains.Prime(domain.ID, domain)

			err = gqlCtx.AuditService.Log(ctx, app, &nonblocking.ProjectDomainVerifiedEventPayload{
				Domain:   domain.Domain,
				DomainID: domain.ID,
			})
			if err != nil {
				return nil, err
			}

			return graphqlutil.NewLazyValue(map[string]interface{}{
				"domain": gqlCtx.Domains.Load(ctx, domain.ID),
				"app":    gqlCtx.Apps.Load(ctx, appID),
			}).Value, nil
		},
	},
)
