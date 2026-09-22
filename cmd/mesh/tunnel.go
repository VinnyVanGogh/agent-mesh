package main

import (
	"context"
	"fmt"
	"os"
	"strconv"

	"github.com/VinnyVanGogh/agent-mesh/internal/bridge"
	"github.com/spf13/cobra"
)

var tunnelCmd = &cobra.Command{
	Use:     "tunnel <remote-port> [local-port]",
	Aliases: []string{"tun"},
	Short:   "Dev server port forwarding over SSH bridge",
	Long: `Establish dev server port forwarding from the remote enterprise host to local machine.

Examples:
  mesh tunnel 3000                 # forwards remote 3000 -> localhost:3000
  mesh tunnel 5173 5173 --open     # forwards Vite and opens browser
  mesh tunnel 8080 --host workbox  # forwards custom remote host 8080 -> 8080`,
	Args: cobra.RangeArgs(1, 2),
	Run: func(cmd *cobra.Command, args []string) {
		remotePort, err := strconv.Atoi(args[0])
		if err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Invalid remote port:\033[0m %s\n", args[0])
			os.Exit(1)
		}

		localPort := remotePort
		if len(args) > 1 {
			localPort, err = strconv.Atoi(args[1])
			if err != nil {
				fmt.Fprintf(os.Stderr, "\033[1;31m✖ Invalid local port:\033[0m %s\n", args[1])
				os.Exit(1)
			}
		}

		openBrowser, _ := cmd.Flags().GetBool("open")
		host, _ := cmd.Flags().GetString("host")

		if host == "" && cfg != nil && cfg.RemoteHost != "" {
			host = cfg.RemoteHost
		}

		ctx := context.Background()
		if err := bridge.StartTunnel(ctx, host, remotePort, localPort, openBrowser); err != nil {
			fmt.Fprintf(os.Stderr, "\033[1;31m✖ Tunnel error:\033[0m %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	tunnelCmd.Flags().BoolP("open", "o", false, "Open browser at http://localhost:<localPort> once forward is established")
	tunnelCmd.Flags().String("host", "", "Remote host (defaults to config remote_host or company-mbp)")

	rootCmd.AddCommand(tunnelCmd)
	bridgeCmd.AddCommand(tunnelCmd)
}
