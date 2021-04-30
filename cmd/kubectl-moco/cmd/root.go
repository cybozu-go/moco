package cmd

import (
	"flag"
	"fmt"
	"os"

	"github.com/cybozu-go/moco"
	mocov1beta1 "github.com/cybozu-go/moco/api/v1beta1"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/cli-runtime/pkg/genericclioptions"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/klog"
	"k8s.io/kubectl/pkg/cmd/util"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var (
	kubeConfigFlags *genericclioptions.ConfigFlags
	kubeClient      client.Client
	factory         util.Factory
	restConfig      *rest.Config
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

		var err error
		factory = util.NewFactory(util.NewMatchVersionFlags(kubeConfigFlags))
		restConfig, err = factory.ToRESTConfig()
		if err != nil {
			return err
		}

		scheme := runtime.NewScheme()
		err = clientgoscheme.AddToScheme(scheme)
		if err != nil {
			return err
		}

		err = mocov1beta1.AddToScheme(scheme)
		if err != nil {
			return err
		}

		kubeClient, err = client.New(restConfig, client.Options{Scheme: scheme})
		if err != nil {
			return err
		}

		rawConfig, err := kubeConfigFlags.ToRawKubeConfigLoader().RawConfig()
		if err != nil {
			return err
		}

		namespace = rawConfig.Contexts[rawConfig.CurrentContext].Namespace
		if kubeConfigFlags.Namespace != nil {
			namespace = *kubeConfigFlags.Namespace
		}
		if len(namespace) == 0 {
			namespace = "default"
		}

		return nil
	},
}

// Execute adds all child commands to the root command and sets flags appropriately.
// This is called by main.main(). It only needs to happen once to the rootCmd.
func Execute() {
	defer klog.Flush()
	if err := rootCmd.Execute(); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
}
