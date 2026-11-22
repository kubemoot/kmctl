package cmd

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/kubemoot/kmctl/internal/client"
	"github.com/kubemoot/kmctl/internal/clierr"
	"github.com/spf13/cobra"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
)

// dashboardTokenLifetime is how long a token printed for the dashboard lives by default.
const dashboardTokenLifetime = 8 * time.Hour

func newDashboardCommand(f *client.Factory) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dashboard",
		Short: "Work with the Kubemoot dashboard",
		Args:  cobra.NoArgs,
	}
	cmd.AddCommand(newDashboardTokenCommand(f))
	return cmd
}

func newDashboardTokenCommand(f *client.Factory) *cobra.Command {
	var lifetime time.Duration
	cmd := &cobra.Command{
		Use:   "token",
		Short: "Print a bearer token to sign in to the Kubemoot dashboard",
		Long: `Print a Kubernetes bearer token to paste into the dashboard's sign-in page. The
dashboard checks it with the API server and then acts as that Kubernetes user, the same
user kmctl acts as, so your own RBAC decides what it shows and which crews you may ask.

With --token-service-account NAMESPACE/NAME, kmctl asks the API server for a token for
that ServiceAccount (the same minting "kmctl conversation ask" uses; you need create on
serviceaccounts/token, and the audit log records who asked). It lives eight hours
unless --duration says otherwise.

Without it, kmctl prints the bearer token your kubeconfig already holds. A kubeconfig that
signs in with a client certificate or a plugin has none to print: name a ServiceAccount.

The token is printed to stdout and nowhere else; treat the output like a password.`,
		Example: `  kmctl dashboard token --token-service-account kubemoot/dashboard-user
  kmctl dashboard token --token-service-account kubemoot/dashboard-user --duration 1h`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			token, err := dashboardToken(cmd, f, lifetime)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(cmd.OutOrStdout(), token)
			return err
		},
	}
	pf := cmd.Flags()
	pf.StringVar(&f.Caller.ServiceAccount, "token-service-account", "",
		"NAMESPACE/NAME of a ServiceAccount to mint the token for")
	pf.StringSliceVar(&f.Caller.Audiences, "token-audience", nil, "Audience of the minted token (default: the API server's)")
	pf.DurationVar(&lifetime, "duration", dashboardTokenLifetime, "How long a minted token lives (at least 10m)")
	return cmd
}

// dashboardToken mints a token for the named ServiceAccount, or returns the kubeconfig's own.
func dashboardToken(cmd *cobra.Command, f *client.Factory, lifetime time.Duration) (string, error) {
	if err := f.Caller.Validate(); err != nil {
		return "", err
	}
	if f.Caller.ServiceAccount == "" {
		return kubeconfigToken(f)
	}
	if lifetime < client.MintedTokenTTL {
		return "", fmt.Errorf("--duration %s: the API server accepts nothing shorter than %s", lifetime, client.MintedTokenTTL)
	}
	ns, name, _ := strings.Cut(f.Caller.ServiceAccount, "/")
	cs, err := f.Clientset()
	if err != nil {
		return "", err
	}
	token, err := client.MintTokenFor(cmd.Context(), cs, ns, name, f.Caller.Audiences, lifetime)
	if apierrors.IsForbidden(err) {
		return "", clierr.New(fmt.Sprintf("you are not allowed to mint a token for service account %s", f.Caller.ServiceAccount),
			"Minting takes create on serviceaccounts/token for that account; check yours with: kubectl auth can-i create serviceaccounts/token -n "+ns, err)
	}
	return token, err
}

// kubeconfigToken is the bearer token the active kubeconfig context holds, when it holds one.
func kubeconfigToken(f *client.Factory) (string, error) {
	cfg, err := f.RESTConfig()
	if err != nil {
		return "", err
	}
	if cfg.BearerToken != "" {
		return cfg.BearerToken, nil
	}
	if cfg.BearerTokenFile != "" {
		raw, err := os.ReadFile(cfg.BearerTokenFile)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(raw)), nil
	}
	return "", clierr.New("your kubeconfig holds no bearer token to print",
		"It signs in with a client certificate or a plugin. Name a ServiceAccount you may create tokens for: kmctl dashboard token --token-service-account NAMESPACE/NAME, or use kubectl create token", nil)
}
