package cmd

import "github.com/spf13/cobra"

// newAppCmd returns `kstack app`, which runs d.App.
func newAppCmd(d Deps) *cobra.Command {
	return &cobra.Command{
		Use:   "app",
		Short: "Open the Kstack desktop app, installing it if it is missing",
		Long: "Open the Kstack desktop app.\n\n" +
			"If Kstack is not installed, kstack app downloads this platform's file from the newest\n" +
			"desktop release, shows its progress, installs it, then opens the app. On Linux it\n" +
			"installs the .deb with apt-get or the .rpm with dnf, through sudo.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return d.App(cmd.Context(), cmd.OutOrStdout())
		},
	}
}
