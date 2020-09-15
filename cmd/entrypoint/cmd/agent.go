package cmd

import (
	"net/http"

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
		agent := agent.New()
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

	agentCmd.Flags().String(addressFlag, ":8080", "Listening address and port.")

	err := viper.BindPFlags(agentCmd.Flags())
	if err != nil {
		panic(err)
	}
}
