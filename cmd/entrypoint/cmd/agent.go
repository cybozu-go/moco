package cmd

import (
	"fmt"
	"net/http"
	"os"

	"github.com/cybozu-go/moco"
	"github.com/cybozu-go/moco/agent"
	"github.com/cybozu-go/well"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

const (
	addressFlag = "address"
)

var agentCmd = &cobra.Command{
	Use:   "agent",
	Short: "Start MySQL agent service",
	Long:  `Start MySQL agent service.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		mux := http.NewServeMux()
		podName := os.Getenv(moco.PodNameEnvName)
		if podName == "" {
			return fmt.Errorf("%s is empty", moco.PodNameEnvName)
		}
		agent := agent.New(podName)
		mux.HandleFunc("/rotate", agent.RotateLog)
		mux.HandleFunc("/clone", agent.Clone)

		serv := &well.HTTPServer{
			Server: &http.Server{
				Addr:    viper.GetString(addressFlag),
				Handler: mux,
			},
		}

		err := serv.ListenAndServe()
		if err != nil {
			return err
		}
		err = well.Wait()

		if err != nil && !well.IsSignaled(err) {
			return err
		}

		return nil
	},
}

func init() {
	rootCmd.AddCommand(agentCmd)

	agentCmd.Flags().String(addressFlag, fmt.Sprintf(":%d", moco.AgentPort), "Listening address and port.")

	err := viper.BindPFlags(agentCmd.Flags())
	if err != nil {
		panic(err)
	}
}
