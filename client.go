package daguito

import (
	"errors"
	"net/http"
)

// ClientOptions configures NewClient.
type ClientOptions struct {
	// APIURL is the Daguito ingest gateway root. When empty, NewClient falls
	// back to DefaultAPIURL.
	APIURL string
	APIKey string
	// HTTPClient is an optional override. When nil, NewClient uses
	// http.DefaultClient.
	HTTPClient *http.Client
}

// Client is the top-level admin client. It bundles the sub-services
// against the Daguito admin REST surface:
//
//	client.AccountKeys.List(ctx)
//	client.PublicKeys.Create(ctx, flowID, daguito.CreatePublicKeyInput{...})
//	client.Budgets.GetOrg(ctx)
//	client.Flows.UpsertAgent(ctx, daguito.AgentFlowSpec{...})
//
// Auth uses a `dgsk_acc_…` account key (the dashboard session JWT is also
// accepted server-side). The existing top-level helpers — RunWebhook,
// WebhookStreamSession, KnowledgeSession, UploadFile — are unchanged; the
// Client is purely additive for management workflows.
type Client struct {
	APIURL string
	APIKey string

	AccountKeys *AccountKeysService
	PublicKeys  *PublicKeysService
	Budgets     *BudgetsService
	Flows       *FlowsService
	Templates   *TemplatesService
	Models      *ModelsService
}

// NewClient constructs a *Client. APIURL defaults to DefaultAPIURL (the ingest
// gateway) when empty. Returns an error if APIKey is empty — it is required and
// the typo is much easier to spot at the constructor than three calls later.
func NewClient(opts ClientOptions) (*Client, error) {
	if opts.APIURL == "" {
		opts.APIURL = DefaultAPIURL
	}
	if opts.APIKey == "" {
		return nil, errors.New("daguito.NewClient: APIKey is required")
	}
	transport := newAdminTransport(opts.APIURL, opts.APIKey, opts.HTTPClient)
	return &Client{
		APIURL:      opts.APIURL,
		APIKey:      opts.APIKey,
		AccountKeys: &AccountKeysService{transport: transport},
		PublicKeys:  &PublicKeysService{transport: transport},
		Budgets:     &BudgetsService{transport: transport},
		Flows:       &FlowsService{transport: transport},
		Templates:   &TemplatesService{transport: transport},
		Models:      &ModelsService{transport: transport},
	}, nil
}
