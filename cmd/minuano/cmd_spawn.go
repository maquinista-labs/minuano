package main

import (
	"fmt"
	"os"

	"github.com/otavio/minuano/internal/agent"
	"github.com/otavio/minuano/internal/tmux"
	"github.com/spf13/cobra"
)

var spawnCapability string

var spawnCmd = &cobra.Command{
	Use:   "spawn <name>",
	Short: "Spawn a single named agent",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := connectDB(); err != nil {
			return err
		}

		session := getSessionName()
		if err := tmux.EnsureSession(session); err != nil {
			return err
		}

		claudeMD, err := findClaudeMD()
		if err != nil {
			return err
		}

		dbURL := dbURL
		if dbURL == "" {
			dbURL = os.Getenv("DATABASE_URL")
		}

		env := map[string]string{
			"DATABASE_URL": dbURL,
		}

		name := args[0]
		a, err := agent.Spawn(pool, session, name, claudeMD, env)
		if err != nil {
			return fmt.Errorf("spawning %s: %w", name, err)
		}

		fmt.Printf("Spawned: %s  →  %s:%s\n", a.ID, a.TmuxSession, a.TmuxWindow)
		return nil
	},
}

func init() {
	spawnCmd.Flags().StringVar(&spawnCapability, "capability", "", "agent capability")
	rootCmd.AddCommand(spawnCmd)
}
