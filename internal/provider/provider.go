// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/solcreek/terraform-provider-livekit/internal/livekit"
)

// Ensure the provider satisfies the framework interface.
var _ provider.Provider = (*livekitProvider)(nil)

// sessionExpiryWarning is how close to its expiry a session from the CLI
// configuration gets a warning.
const sessionExpiryWarning = 7 * 24 * time.Hour

type livekitProvider struct {
	version string
	// now is the clock, replaceable in tests.
	now func() time.Time
}

type providerModel struct {
	SessionToken      types.String `tfsdk:"session_token"`
	CLIConfigPath     types.String `tfsdk:"cli_config_path"`
	CLIUser           types.String `tfsdk:"cli_user"`
	APIURL            types.String `tfsdk:"api_url"`
	ProjectID         types.String `tfsdk:"project_id"`
	TimeoutSeconds    types.Int64  `tfsdk:"timeout_seconds"`
	RequestsPerMinute types.Int64  `tfsdk:"requests_per_minute"`
}

// providerData is handed to every resource and data source.
type providerData struct {
	client *livekit.Client
	// projectID is the default project, "" when none is configured.
	projectID string
}

// New returns a provider factory for the given version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &livekitProvider{version: version, now: time.Now}
	}
}

func (p *livekitProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "livekit"
	resp.Version = p.version
}

func (p *livekitProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage LiveKit Cloud projects through the LiveKit Cloud API: project settings, " +
			"webhooks, API keys, members, SIP trunks and dispatch rules, and phone numbers. Authenticates with a LiveKit Cloud user session, which reaches every " +
			"project the user is a member of.",
		Attributes: map[string]schema.Attribute{
			"session_token": schema.StringAttribute{
				MarkdownDescription: "A LiveKit Cloud user session token. Falls back to the `LIVEKIT_SESSION_TOKEN` " +
					"environment variable, and then to the session that `lk cloud auth` saved in the LiveKit CLI " +
					"configuration. A session lasts about 30 days.",
				Optional:  true,
				Sensitive: true,
			},
			"cli_config_path": schema.StringAttribute{
				MarkdownDescription: "LiveKit CLI configuration to read the session from when no token is set. " +
					"Defaults to `~/.livekit/cli-config.yaml`. Falls back to `LIVEKIT_CLI_CONFIG`.",
				Optional: true,
			},
			"cli_user": schema.StringAttribute{
				MarkdownDescription: "ID or email of the signed-in user in the LiveKit CLI configuration whose " +
					"session to use. Defaults to the CLI's default user.",
				Optional: true,
			},
			"api_url": schema.StringAttribute{
				MarkdownDescription: "LiveKit Cloud API base URL. Defaults to `https://beta-api.livekit.cloud`. " +
					"Falls back to `LIVEKIT_API_URL`, the variable the LiveKit CLI reads too.",
				Optional: true,
			},
			"project_id": schema.StringAttribute{
				MarkdownDescription: "Default project (`p_...`) for resources and data sources that do not set " +
					"their own `project_id`. Falls back to `LIVEKIT_PROJECT_ID`.",
				Optional: true,
			},
			"timeout_seconds": schema.Int64Attribute{
				MarkdownDescription: "Per-request HTTP timeout in seconds. Defaults to 60.",
				Optional:            true,
			},
			"requests_per_minute": schema.Int64Attribute{
				MarkdownDescription: "How many API requests the provider sends a minute, after a burst of 50. " +
					"Defaults to 55: the API allows a session about 60 in any minute and refuses the rest. " +
					"`0` turns pacing off.",
				Optional:   true,
				Validators: []validator.Int64{int64validator.AtLeast(0)},
			},
		},
	}
}

func (p *livekitProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}
	for attr, v := range map[string]types.String{
		"session_token": cfg.SessionToken, "cli_config_path": cfg.CLIConfigPath, "cli_user": cfg.CLIUser,
		"api_url": cfg.APIURL, "project_id": cfg.ProjectID,
	} {
		if v.IsUnknown() {
			resp.Diagnostics.AddAttributeError(path.Root(attr), "Unknown provider configuration",
				fmt.Sprintf("`%s` must be known when the provider is configured; it cannot depend on a value that is only known after apply.", attr))
		}
	}
	if resp.Diagnostics.HasError() {
		return
	}

	token := firstNonEmpty(cfg.SessionToken.ValueString(), os.Getenv("LIVEKIT_SESSION_TOKEN"))
	sessionAPIURL := ""
	if token == "" {
		cfgPath := firstNonEmpty(cfg.CLIConfigPath.ValueString(), os.Getenv("LIVEKIT_CLI_CONFIG"), defaultCLIConfigPath())
		s, err := readCLISession(cfgPath, cfg.CLIUser.ValueString())
		switch {
		case errors.Is(err, errNoCLIConfig):
			resp.Diagnostics.AddError("Missing LiveKit Cloud credentials",
				"Set the provider `session_token` argument or the LIVEKIT_SESSION_TOKEN environment variable, "+
					"or sign in with `lk cloud auth` so the provider can use the LiveKit CLI's session.")
			return
		case err != nil:
			resp.Diagnostics.AddError("Unable to read the LiveKit CLI session", err.Error())
			return
		}
		if !s.expiry.IsZero() {
			left := s.expiry.Sub(p.now())
			if left <= 0 {
				resp.Diagnostics.AddError("LiveKit CLI session expired",
					fmt.Sprintf("The session of %s in %s expired on %s. Sign in again with `lk cloud auth`.",
						s.user, s.cfgPath, s.expiry.UTC().Format(time.DateOnly)))
				return
			}
			if left < sessionExpiryWarning {
				resp.Diagnostics.AddWarning("LiveKit CLI session expires soon",
					fmt.Sprintf("The session of %s expires on %s. Sign in again with `lk cloud auth` before then.",
						s.user, s.expiry.UTC().Format(time.DateOnly)))
			}
		}
		token, sessionAPIURL = s.token, s.apiURL
	}

	timeout := 60 * time.Second
	if !cfg.TimeoutSeconds.IsNull() && cfg.TimeoutSeconds.ValueInt64() > 0 {
		timeout = time.Duration(cfg.TimeoutSeconds.ValueInt64()) * time.Second
	}

	opts := []livekit.Option{
		livekit.WithBaseURL(firstNonEmpty(cfg.APIURL.ValueString(), os.Getenv("LIVEKIT_API_URL"), sessionAPIURL)),
		livekit.WithTimeout(timeout),
		livekit.WithUserAgent("terraform-provider-livekit/" + p.version),
	}
	if !cfg.RequestsPerMinute.IsNull() {
		opts = append(opts, livekit.WithRateLimit(int(cfg.RequestsPerMinute.ValueInt64()), livekit.DefaultBurst))
	}
	client := livekit.New(token, opts...)

	data := &providerData{
		client:    client,
		projectID: firstNonEmpty(cfg.ProjectID.ValueString(), os.Getenv("LIVEKIT_PROJECT_ID")),
	}
	resp.ResourceData = data
	resp.DataSourceData = data
}

func (p *livekitProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		newProjectSettingsResource,
		newWebhookResource,
		newAPIKeyResource,
		newSIPInboundTrunkResource,
		newSIPOutboundTrunkResource,
		newSIPDispatchRuleResource,
		newProjectMemberResource,
		newPhoneNumberResource,
	}
}

func (p *livekitProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		newProjectDataSource,
		newWebhookEventsDataSource,
		newAPIKeysDataSource,
		newProjectMembersDataSource,
		newWorkspaceMembersDataSource,
		newAvailablePhoneNumbersDataSource,
	}
}

// configureData extracts the provider data in a resource or data source
// Configure method. It returns nil before the provider is configured.
func configureData(raw any, diags *diag.Diagnostics) *providerData {
	if raw == nil {
		return nil
	}
	d, ok := raw.(*providerData)
	if !ok {
		diags.AddError("Unexpected provider data", fmt.Sprintf("Expected *providerData, got %T. Please report this issue to the provider developers.", raw))
		return nil
	}
	return d
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
