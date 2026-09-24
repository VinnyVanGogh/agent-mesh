package main

import (
	"context"
	"fmt"
	"os"

	"github.com/VinnyVanGogh/staypoint/internal/mcp"
	"github.com/spf13/cobra"
)

var mcpCmd = &cobra.Command{
	Use:   "mcp",
	Short: "Start Model Context Protocol (MCP) JSON-RPC 2.0 stdio server",
	Long:  "Run the pure Go Model Context Protocol (MCP) server over standard I/O for LLM client integration.",
	Run: func(cmd *cobra.Command, args []string) {
		ctx := cmd.Context()
		if ctx == nil {
			ctx = context.Background()
		}

		var opts []mcp.Option
		if cfg != nil {
			opts = append(opts, mcp.WithConfig(cfg))
		}

		server := mcp.NewServer(opts...)
		defer server.Close()

		if err := server.Serve(ctx, os.Stdin, os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "MCP server exited with error: %v\n", err)
			os.Exit(1)
		}
	},
}

func init() {
	rootCmd.AddCommand(mcpCmd)
}
