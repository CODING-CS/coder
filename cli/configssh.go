package cli

import (
	"github.com/spf13/cobra"
)

// const sshStartToken = "# ------------START-CODER-----------"
// const sshStartMessage = `# This was generated by "coder config-ssh".
// #
// # To remove this blob, run:
// #
// #    coder config-ssh --remove
// #
// # You should not hand-edit this section, unless you are deleting it.`
// const sshEndToken = "# ------------END-CODER------------"

func configSSH() *cobra.Command {
	cmd := &cobra.Command{
		Use: "config-ssh",
		RunE: func(cmd *cobra.Command, args []string) error {
			return nil
		},
	}

	return cmd
}