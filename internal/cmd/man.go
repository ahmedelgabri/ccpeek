package cmd

import (
	"github.com/spf13/cobra"
	"github.com/spf13/cobra/doc"
)

var manCmd = &cobra.Command{
	Use:    "man [directory]",
	Short:  "Generate man pages",
	Args:   cobra.ExactArgs(1),
	Hidden: true,
	RunE: func(cmd *cobra.Command, args []string) error {
		header := &doc.GenManHeader{
			Title:   "CCPEAK",
			Section: "1",
		}
		return doc.GenManTree(rootCmd, header, args[0])
	},
}

func init() {
	rootCmd.AddCommand(manCmd)
}
