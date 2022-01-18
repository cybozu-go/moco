package cmd

import (
	"flag"
	"os"

	// Import all Kubernetes client auth plugins (e.g. Azure, GCP, OIDC, etc.)
	_ "k8s.io/client-go/plugin/pkg/client/auth"

	"github.com/cybozu-go/moco"
	mocov1beta2 "github.com/cybozu-go/moco/api/v1beta2"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/klog/v2"
	"k8s.io/kubectl/pkg/cmd/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	kubeConfigFlags *genericclioptions.ConfigFlags
	kubeClient      client.Client
	factory         util.Factory
	namespace       string
)

func init() {
	klog.InitFlags(nil)
	pflag.CommandLine.AddGoFlagSet(flag.CommandLine)
	kubeConfigFlags = genericclioptions.NewConfigFlags(true)
	kubeConfigFlags.AddFlags(rootCmd.PersistentFlags())
}

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:     "kubectl-moco",
	Version: moco.Version,
	Short:   "the utility command for MOCO",
	Long:    "the utility command for MOCO.",
	PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
		cmd.SilenceUsage = true

		factory = util.NewFactory(util.NewMatchVersionFlags(kubeConfigFlags))
		restConfig, err := factory.ToRESTConfig()
		if err != nil {
			return err
		}

		scheme := runtime.NewScheme()
		err = clientgoscheme.AddToScheme(scheme)
		if err != nil {
			return err
		}

		err = mocov1beta2.AddToScheme(scheme)
		if err != nil {
			return err
		}

		kubeClient, err = client.New(restConfig, client.Options{Scheme: scheme})
		if err != nil {
			return err
		}

		namespace, _, err = kubeConfigFlags.ToRawKubeConfigLoader().Namespace()
		return err
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	defer klog.Flush()
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}
